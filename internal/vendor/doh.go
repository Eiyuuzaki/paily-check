package vendor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/component/ech/echparser"
	"github.com/metacubex/mihomo/component/resolver"
	D "github.com/miekg/dns"
	"golang.org/x/sync/singleflight"
)

// dohResolver is a minimal DoH (DNS-over-HTTPS JSON API) resolver that
// bypasses plaintext-DNS poisoning. GFW poisons dpdns.org A records at the
// DNS layer, so even with ECH hiding the SNI the client never learns the
// real IP. Routing lookups through Ali DoH fixes this.
//
// It also implements ResolveECH: mihomo calls it to fetch the ECHConfigList
// from the DNS HTTPS (type 65) record - the other half of the ECH handshake.
type dohResolver struct {
	endpoint  string
	once      sync.Once
	echCache  map[string]echCacheEntry
	mu        sync.Mutex
	echFlight singleflight.Group
}

// echCacheEntry caches the ECHConfigList per host. CF rotates keys rarely
// but does rotate them; a stale key would poison every subsequent handshake,
// so entries expire after 5 minutes and get re-fetched.
type echCacheEntry struct {
	data      []byte
	fetchedAt time.Time
}

const echCacheTTL = 5 * time.Minute

var _ resolver.Resolver = (*dohResolver)(nil)

func newDoHResolver() *dohResolver {
	return &dohResolver{endpoint: "https://223.5.5.5/resolve", echCache: map[string]echCacheEntry{}}
}

// dnsQuery performs a JSON-API DNS lookup with one retry on transport error.
// Ali DoH is highly available, but the NAS uplink is not - a single timed-out
// probe must not translate into a dead verdict for an otherwise healthy node.
func (d *dohResolver) dnsQuery(ctx context.Context, name string, rtype uint16) []map[string]interface{} {
	answers, err := d.queryDNS(ctx, name, rtype)
	if err == nil || ctx.Err() != nil {
		return answers
	}
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil
	case <-timer.C:
	}
	answers, _ = d.queryDNS(ctx, name, rtype)
	return answers
}

func (d *dohResolver) queryDNS(ctx context.Context, name string, rtype uint16) ([]map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	query := url.Values{"name": {name}, "type": {rrtypeName(rtype)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh: HTTP status %d", resp.StatusCode)
	}
	var out struct {
		Status *int
		Answer []map[string]interface{}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Status == nil || (*out.Status != D.RcodeSuccess && *out.Status != D.RcodeNameError) {
		return nil, fmt.Errorf("doh: unsuccessful DNS response")
	}
	return out.Answer, nil
}

func rrtypeName(t uint16) string {
	switch t {
	case 1:
		return "A"
	case 28:
		return "AAAA"
	case 65:
		return "HTTPS"
	default:
		return fmt.Sprintf("TYPE%d", t)
	}
}

func (d *dohResolver) lookupIPs(ctx context.Context, name string, v6 bool) ([]netip.Addr, error) {
	answers := d.dnsQuery(ctx, name, map[bool]uint16{false: 1, true: 28}[v6])
	var ips []netip.Addr
	for _, a := range answers {
		data, _ := a["data"].(string)
		ip, err := netip.ParseAddr(strings.TrimSpace(data))
		if err != nil {
			continue
		}
		if ip.Is6() != v6 {
			continue
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("doh: no records for %s", name)
	}
	return ips, nil
}

func (d *dohResolver) LookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	v4, _ := d.lookupIPs(ctx, host, false)
	v6, err6 := d.lookupIPs(ctx, host, true)
	if len(v4) > 0 {
		return v4, nil
	}
	return v6, err6
}

func (d *dohResolver) LookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	return d.lookupIPs(ctx, host, false)
}

func (d *dohResolver) LookupIPv6(ctx context.Context, host string) ([]netip.Addr, error) {
	return d.lookupIPs(ctx, host, true)
}

var errNoECHConfig = errors.New("doh: no ECH config in HTTPS record")
var echParam = regexp.MustCompile(`(?:^|[	 ])ech=(?:"([^"]*)"|([^	 ]+))`)

// ResolveECH returns a nonempty ECHConfigList and caches definitive answers.
// Coalescing is per host, and callers waiting on another request can cancel.
func (d *dohResolver) ResolveECH(ctx context.Context, host string) ([]byte, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if host == "" {
		return nil, errNoECHConfig
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-d.echFlight.DoChan(host, func() (any, error) { return d.resolveECH(ctx, host) }):
		return result.Val.([]byte), result.Err
	}
}

func (d *dohResolver) resolveECH(ctx context.Context, host string) ([]byte, error) {
	d.mu.Lock()
	e, ok := d.echCache[host]
	d.mu.Unlock()
	if ok && time.Since(e.fetchedAt) < echCacheTTL {
		if len(e.data) == 0 {
			return nil, errNoECHConfig
		}
		return e.data, nil
	}
	answers, err := d.queryDNS(ctx, host, D.TypeHTTPS)
	if err != nil {
		return nil, err
	}
	var list []byte
	malformed := false
	for _, answer := range answers {
		if recordType, _ := answer["type"].(float64); recordType != float64(D.TypeHTTPS) {
			continue
		}
		text, _ := answer["data"].(string)
		matches := echParam.FindStringSubmatch(text)
		if len(matches) != 3 {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(matches[1] + matches[2])
		if err != nil {
			malformed = true
			continue
		}
		configs, err := echparser.ParseECHConfigList(data)
		if err != nil || len(configs) == 0 {
			malformed = true
			continue
		}
		list = data
		break
	}
	if len(list) == 0 && malformed {
		return nil, fmt.Errorf("doh: invalid ECH config in HTTPS record")
	}
	d.mu.Lock()
	d.echCache[host] = echCacheEntry{data: list, fetchedAt: time.Now()}
	d.mu.Unlock()
	if len(list) == 0 {
		return nil, errNoECHConfig
	}
	return list, nil
}

func (d *dohResolver) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	return nil, fmt.Errorf("doh: ExchangeContext not implemented")
}

func (d *dohResolver) Invalid() bool    { return false }
func (d *dohResolver) ClearCache()      {}
func (d *dohResolver) ResetConnection() {}

// installDoHResolver registers the DoH resolver as mihomo default.
// Called once from Build to make every node resolve through encrypted DNS.
var globalDoH = newDoHResolver()

func init() { installDoHResolver() }

func installDoHResolver() {
	globalDoH.once.Do(func() {
		resolver.DefaultResolver = globalDoH
		resolver.ProxyServerHostResolver = globalDoH
		resolver.DirectHostResolver = globalDoH
	})
}
