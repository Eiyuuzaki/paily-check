package geo

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openpaily/paily-check/internal/engine/request"
	"github.com/openpaily/paily-check/internal/interfaces"
)

const nativeGeoDefaultTimeout = 5 * time.Second

type GeoNativeAPIConfig struct {
	TimeoutMS int
	Providers []string
}

type NativeGeoEvidence struct {
	Info     *interfaces.GeoInfo
	Provider string
	// Votes is how many providers independently agreed on Info.CountryCode, and
	// AgreedWith names them. Exposed so a single-source answer is visible as such
	// instead of looking as certain as a unanimous one.
	Votes      int
	AgreedWith []string
}

// nativeGeoQuorum is how many independent providers must agree before their
// country is accepted without waiting for the rest.
//
// Racing for the first answer let network timing decide the region: whichever
// provider replied fastest won, and the fastest one here is cloudflare-trace,
// whose loc comes from Cloudflare's own IP database — the source this project
// has repeatedly measured as wrong for Cloudflare ranges (2026-11-11: a US
// egress labelled DE; 2026-11-24: 2605:52c0:: labelled LAX/US while sitting
// outside the official anycast list). Agreement between two sources is much
// harder to produce by accident than one source being fast, so requiring two
// suppresses a single bad database without discarding it when it is right.
const nativeGeoQuorum = 2

type nativeGeoProvider struct {
	name    string
	url     string
	parse   func([]byte) (*interfaces.GeoInfo, error)
	headers map[string]string
}

func RunNativeGeoCheck(ctx context.Context, v interfaces.Vendor, cfg GeoNativeAPIConfig) *interfaces.GeoInfo {
	return RunNativeGeoEvidence(ctx, v, cfg).Info
}

func RunNativeGeoEvidence(ctx context.Context, v interfaces.Vendor, cfg GeoNativeAPIConfig) NativeGeoEvidence {
	providers := buildNativeGeoProviders(cfg.Providers)
	if len(providers) == 0 {
		return NativeGeoEvidence{}
	}

	timeout := nativeGeoDefaultTimeout
	if cfg.TimeoutMS > 0 {
		timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Every provider gets to answer: the decision is made from the whole set of
	// replies, not from whichever arrived first. The channel is never closed, so
	// late replies after an early return are dropped rather than panicking.
	resultCh := make(chan NativeGeoEvidence, len(providers))
	var wg sync.WaitGroup

	for i := range providers {
		provider := providers[i]
		wg.Add(1)
		go func() {
			defer wg.Done()

			info, err := provider.lookup(ctx, v)
			if err != nil || info == nil || !isValidCountryCode(info.CountryCode) {
				return
			}
			resultCh <- NativeGeoEvidence{Info: info, Provider: provider.name}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	quorum := nativeGeoQuorum
	if quorum > len(providers) {
		quorum = len(providers)
	}

	tally := newNativeGeoTally(quorum)
	for {
		select {
		case ev := <-resultCh:
			if opinion, ok := tally.record(ev); ok {
				return opinion
			}
		case <-done:
			return tally.finish(resultCh)
		case <-ctx.Done():
			return tally.finish(resultCh)
		}
	}
}

// nativeGeoTally counts how many providers named each country and decides when
// the answer is settled. Split out from the request plumbing so the decision can
// be tested without a network.
type nativeGeoTally struct {
	quorum    int
	votes     map[string]int
	agreed    map[string][]string
	first     map[string]NativeGeoEvidence
	best      NativeGeoEvidence
	bestVotes int
}

func newNativeGeoTally(quorum int) *nativeGeoTally {
	if quorum < 1 {
		quorum = 1
	}
	return &nativeGeoTally{
		quorum: quorum,
		votes:  make(map[string]int),
		agreed: make(map[string][]string),
		first:  make(map[string]NativeGeoEvidence),
	}
}

// record files one reply. The second result reports that quorum was met, in
// which case it carries the settled opinion and the caller can stop waiting.
func (t *nativeGeoTally) record(ev NativeGeoEvidence) (NativeGeoEvidence, bool) {
	if ev.Info == nil {
		return NativeGeoEvidence{}, false
	}
	code := strings.ToUpper(ev.Info.CountryCode)
	t.votes[code]++
	t.agreed[code] = append(t.agreed[code], ev.Provider)
	if _, ok := t.first[code]; !ok {
		t.first[code] = ev
	}
	if t.votes[code] > t.bestVotes {
		t.bestVotes = t.votes[code]
		t.best = t.first[code]
	}
	if t.votes[code] < t.quorum {
		return NativeGeoEvidence{}, false
	}
	return t.opinion(code), true
}

// finish consumes whatever is already buffered, then returns the best tally.
//
// Quorum is not required: a single reply is still evidence, and returning
// nothing would drop the node from distribution entirely (an empty region is
// discarded downstream), which is the harsher of the two mistakes.
func (t *nativeGeoTally) finish(resultCh <-chan NativeGeoEvidence) NativeGeoEvidence {
	for {
		select {
		case ev := <-resultCh:
			if opinion, ok := t.record(ev); ok {
				return opinion
			}
		default:
			if t.bestVotes == 0 {
				return NativeGeoEvidence{}
			}
			return t.opinion(strings.ToUpper(t.best.Info.CountryCode))
		}
	}
}

func (t *nativeGeoTally) opinion(code string) NativeGeoEvidence {
	ev := t.first[code]
	ev.Votes = t.votes[code]
	ev.AgreedWith = append([]string(nil), t.agreed[code]...)
	return ev
}

func buildNativeGeoProviders(names []string) []nativeGeoProvider {
	all := map[string]nativeGeoProvider{
		"cloudflare-trace": {
			name:    "cloudflare-trace",
			url:     "https://x.com/cdn-cgi/trace",
			parse:   parseCloudflareTrace,
			headers: defaultNativeGeoHeaders(),
		},
		"ipwhois": {
			name:    "ipwhois",
			url:     "https://ipwho.is",
			parse:   parseIPWhoIs,
			headers: defaultNativeGeoHeaders(),
		},
		"myip": {
			name:    "myip",
			url:     "https://api.myip.com",
			parse:   parseMyIP,
			headers: defaultNativeGeoHeaders(),
		},
		"ipapi-co": {
			name:    "ipapi-co",
			url:     "https://ipapi.co/json",
			parse:   parseIPAPICo,
			headers: defaultNativeGeoHeaders(),
		},
		"ident-me": {
			name:    "ident-me",
			url:     "https://ident.me/json",
			parse:   parseIdentMe,
			headers: defaultNativeGeoHeaders(),
		},
		"ip-api": {
			name:    "ip-api",
			url:     "http://ip-api.com/json",
			parse:   parseIPAPI,
			headers: defaultNativeGeoHeaders(),
		},
		"ip-sb": {
			name:    "ip-sb",
			url:     "https://api.ip.sb/geoip",
			parse:   parseIPSB,
			headers: defaultNativeGeoHeaders(),
		},
		"ipinfo": {
			name:    "ipinfo",
			url:     "https://ipinfo.io/json",
			parse:   parseIPInfo,
			headers: defaultNativeGeoHeaders(),
		},
	}

	if len(names) == 0 {
		names = []string{"cloudflare-trace", "ipwhois", "myip", "ipapi-co", "ident-me", "ip-api", "ip-sb", "ipinfo"}
	}

	providers := make([]nativeGeoProvider, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		provider, ok := all[key]
		if !ok {
			continue
		}
		seen[key] = struct{}{}
		providers = append(providers, provider)
	}
	return providers
}

func (p nativeGeoProvider) lookup(ctx context.Context, v interfaces.Vendor) (*interfaces.GeoInfo, error) {
	body, err := doNativeGeoRequest(ctx, v, p.url, p.headers)
	if err != nil {
		return nil, err
	}
	return p.parse(body)
}

func doNativeGeoRequest(ctx context.Context, v interfaces.Vendor, url string, headers map[string]string) ([]byte, error) {
	resp, _, err := request.Unsafe(ctx, v, &interfaces.RequestOptions{
		Method:  http.MethodGet,
		URL:     url,
		Headers: headers,
		Network: interfaces.ROptionsTCP,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func defaultNativeGeoHeaders() map[string]string {
	return map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36",
		"Accept":          "application/json,text/plain,*/*",
		"Accept-Language": "en-US,en;q=0.9",
	}
}

func parseCloudflareTrace(body []byte) (*interfaces.GeoInfo, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	info := &interfaces.GeoInfo{IP: values["ip"], CountryCode: strings.ToUpper(values["loc"])}
	if !isValidCountryCode(info.CountryCode) {
		return nil, fmt.Errorf("trace missing valid loc")
	}
	return info, nil
}

func parseIPWhoIs(body []byte) (*interfaces.GeoInfo, error) {
	var payload struct {
		Success     bool   `json:"success"`
		IP          string `json:"ip"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		RegionCode  string `json:"region_code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !payload.Success {
		return nil, fmt.Errorf("ipwhois unsuccessful")
	}
	return &interfaces.GeoInfo{IP: payload.IP, Country: payload.Country, CountryCode: strings.ToUpper(payload.CountryCode)}, nil
}

func parseMyIP(body []byte) (*interfaces.GeoInfo, error) {
	var payload struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
		Code    string `json:"cc"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if strings.EqualFold(payload.Code, "XX") {
		return nil, fmt.Errorf("unknown myip country")
	}
	return &interfaces.GeoInfo{IP: payload.IP, Country: payload.Country, CountryCode: strings.ToUpper(payload.Code)}, nil
}

func parseIPAPICo(body []byte) (*interfaces.GeoInfo, error) {
	var payload struct {
		Error       bool   `json:"error"`
		IP          string `json:"ip"`
		Country     string `json:"country_name"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		RegionCode  string `json:"region_code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Error {
		return nil, fmt.Errorf("ipapi.co error")
	}
	return &interfaces.GeoInfo{IP: payload.IP, Country: payload.Country, CountryCode: strings.ToUpper(payload.CountryCode)}, nil
}

func parseIdentMe(body []byte) (*interfaces.GeoInfo, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &interfaces.GeoInfo{
		IP:          stringValue(payload, "ip"),
		Country:     stringValue(payload, "country"),
		CountryCode: strings.ToUpper(firstNonEmpty(stringValue(payload, "cc"), stringValue(payload, "country_code"))),
		TimeZone:    stringValue(payload, "tz"),
	}, nil
}

func parseIPAPI(body []byte) (*interfaces.GeoInfo, error) {
	var payload struct {
		Status      string  `json:"status"`
		Query       string  `json:"query"`
		Country     string  `json:"country"`
		CountryCode string  `json:"countryCode"`
		Region      string  `json:"region"`
		RegionName  string  `json:"regionName"`
		Lat         float32 `json:"lat"`
		Lon         float32 `json:"lon"`
		TimeZone    string  `json:"timezone"`
		ISP         string  `json:"isp"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !strings.EqualFold(payload.Status, "success") {
		return nil, fmt.Errorf("ip-api unsuccessful")
	}
	return &interfaces.GeoInfo{IP: payload.Query, Country: payload.Country, CountryCode: strings.ToUpper(payload.CountryCode), TimeZone: payload.TimeZone, ISP: payload.ISP, Lat: payload.Lat, Lon: payload.Lon}, nil
}

func parseIPSB(body []byte) (*interfaces.GeoInfo, error) {
	var payload struct {
		IP          string  `json:"ip"`
		Country     string  `json:"country"`
		CountryCode string  `json:"country_code"`
		Region      string  `json:"region"`
		RegionCode  string  `json:"region_code"`
		TimeZone    string  `json:"timezone"`
		ISP         string  `json:"organization"`
		Lat         float32 `json:"latitude"`
		Lon         float32 `json:"longitude"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &interfaces.GeoInfo{IP: payload.IP, Country: payload.Country, CountryCode: strings.ToUpper(payload.CountryCode), TimeZone: payload.TimeZone, ISP: payload.ISP, Lat: payload.Lat, Lon: payload.Lon}, nil
}

func parseIPInfo(body []byte) (*interfaces.GeoInfo, error) {
	var payload struct {
		IP       string `json:"ip"`
		Country  string `json:"country"`
		Region   string `json:"region"`
		Loc      string `json:"loc"`
		Org      string `json:"org"`
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	info := &interfaces.GeoInfo{IP: payload.IP, CountryCode: strings.ToUpper(payload.Country), Country: payload.Region, Org: payload.Org, TimeZone: payload.Timezone}
	if payload.Loc != "" {
		parts := strings.Split(payload.Loc, ",")
		if len(parts) == 2 {
			fmt.Sscanf(parts[0], "%f", &info.Lat)
			fmt.Sscanf(parts[1], "%f", &info.Lon)
		}
	}
	return info, nil
}

func isValidCountryCode(code string) bool {
	if len(code) != 2 {
		return false
	}
	code = strings.ToUpper(code)
	if code == "XX" {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func stringValue(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok {
		return ""
	}
	str, _ := value.(string)
	return str
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
