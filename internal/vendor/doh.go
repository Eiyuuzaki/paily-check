package vendor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	D "github.com/miekg/dns"
	"github.com/metacubex/mihomo/component/resolver"
)

// dohResolver is a minimal DoH (DNS-over-HTTPS JSON API) resolver that
// bypasses plaintext-DNS poisoning. GFW poisons dpdns.org A records at the
// DNS layer, so even with ECH hiding the SNI the client never learns the
// real IP. Routing lookups through Ali DoH fixes this.
//
// It also implements ResolveECH: mihomo calls it to fetch the ECHConfigList
// from the DNS HTTPS (type 65) record - the other half of the ECH handshake.
type dohResolver struct {
	endpoint string
	once     sync.Once
	echCache map[string]echCacheEntry
	mu       sync.Mutex
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
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		ctxTimeout, cancel := context.WithTimeout(ctx, 8*time.Second)
		url := d.endpoint + "?name=" + name + "&type=" + rrtypeName(rtype)
		req, err := http.NewRequestWithContext(ctxTimeout, "GET", url, nil)
		if err != nil {
			cancel()
			return nil
		}
		req.Header.Set("accept", "application/dns-json")
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			continue
		}
		var out struct {
			Answer []map[string]interface{} `json:"Answer"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if err == nil {
			return out.Answer
		}
	}
	return nil
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
// ResolveECH extracts the ech= base64 value from the HTTPS (type 65) record.
// mihomo calls this before every ECH handshake.
func (d *dohResolver) ResolveECH(ctx context.Context, host string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.echCache[host]; ok && time.Since(e.fetchedAt) < echCacheTTL {
		return e.data, nil
	}
	answers := d.dnsQuery(ctx, host, 65)
	for _, a := range answers {
		data, _ := a["data"].(string)
		marker := "ech=\""
		i := strings.Index(data, marker)
		if i < 0 {
			continue
		}
		val := data[i+len(marker):]
		j := strings.Index(val, "\"")
		if j < 0 {
			continue
		}
		val = val[:j]
		b, err := base64.StdEncoding.DecodeString(val)
		if err != nil {
			return nil, fmt.Errorf("doh: ech decode: %w", err)
		}
		d.echCache[host] = echCacheEntry{data: b, fetchedAt: time.Now()}
		return b, nil
	}
	return nil, fmt.Errorf("doh: no ECH config in HTTPS record for %s", host)
}

func (d *dohResolver) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	return nil, fmt.Errorf("doh: ExchangeContext not implemented")
}

func (d *dohResolver) Invalid() bool { return false }
func (d *dohResolver) ClearCache()  {}
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