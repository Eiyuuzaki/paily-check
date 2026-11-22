package initial

import (
	"context"
	"encoding/base64"
	"maps"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/openpaily/paily-check/internal/client"
	"github.com/openpaily/paily-check/internal/interfaces"
	"github.com/openpaily/paily-check/internal/macros/ping"
	"github.com/openpaily/paily-check/internal/vendor"
)

const (
	echProbeURL      = "https://www.google.com/generate_204"
	echMaxCandidates = 40
	echConcurrency   = 2
	echNodeTimeout   = 30 * time.Second
)

type echDependencies struct {
	lookup func(context.Context, string) ([]byte, error)
	probe  func(context.Context, map[string]any, string, time.Duration) (int, bool, error)
}

func productionECH(v interfaces.Vendor) echDependencies {
	return echDependencies{
		lookup: vendor.ResolveECH,
		probe: func(ctx context.Context, raw map[string]any, target string, timeout time.Duration) (int, bool, error) {
			return ping.StrictHTTPS204(ctx, v.Build(raw), target, timeout)
		},
	}
}

func hasSourceECH(raw map[string]any) bool {
	_, clash := raw["ech-opts"]
	_, sing := raw["ech"]
	return clash || sing
}

func echHostname(raw map[string]any) string {
	if hasSourceECH(raw) || raw["tls"] != true || raw["network"] != "ws" {
		return ""
	}
	if proxyType, _ := raw["type"].(string); proxyType != "vless" && proxyType != "vmess" {
		return ""
	}
	for _, key := range []string{"reality-opts", "shadow-tls-opts", "restls-opts", "jls-opts", "dialer-proxy", "smux"} {
		if _, ok := raw[key]; ok {
			return ""
		}
	}
	if ws, ok := raw["ws-opts"].(map[string]any); ok {
		for _, key := range []string{"max-early-data", "v2ray-http-upgrade-fast-open"} {
			if _, ok := ws[key]; ok {
				return ""
			}
		}
		path, _ := ws["path"].(string)
		if u, err := url.Parse(path); err != nil || u.Query().Has("ed") {
			return ""
		}
	}
	server, _ := raw["server"].(string)
	if _, err := netip.ParseAddr(server); err != nil {
		return ""
	}
	host, _ := raw["servername"].(string)
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if _, err := netip.ParseAddr(host); err == nil || !strings.Contains(host, ".") || strings.ContainsAny(host, " /:?&#\t\r\n") {
		return ""
	}
	return host
}

func newAssessment(node client.Node, target, decision string) *client.ECHAssessment {
	hash, err := node.ECHRawSHA256()
	if err != nil {
		return nil
	}
	return &client.ECHAssessment{Version: 1, Decision: decision, RawSHA256: hash, ProbeURL: target, CheckedAt: time.Now().UTC().Truncate(time.Second)}
}

func recordOrdinarySuccess(node client.Node, result *client.CheckResult, target string) {
	if target == "" || hasSourceECH(node.Raw) || node.Raw["tls"] != true {
		return
	}
	if evidence := newAssessment(node, target, client.ECHPlain); evidence != nil {
		evidence.PlainAttempts, evidence.PlainSuccess = 1, 1
		result.ECH = evidence
	}
}

func runECHComparisons(ctx context.Context, nodes []client.Node, results []client.CheckResult, limit, timeoutMS int, deps echDependencies) {
	limit = min(limit, echMaxCandidates)
	if limit <= 0 {
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, echConcurrency)
	for i, node := range nodes {
		if ctx.Err() != nil || limit == 0 {
			break
		}
		r := &results[i]
		reason := r.Initial.FailReason
		if r.ECH != nil || r.Initial.LatencyMS != -1 || (reason != "conn_reset" && reason != "tls_handshake") {
			continue
		}
		host := echHostname(node.DialRaw())
		if host == "" {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		limit--
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			assessECH(ctx, node, r, host, timeoutMS, deps)
		}()
	}
	wg.Wait()
}

func assessECH(ctx context.Context, node client.Node, r *client.CheckResult, host string, timeoutMS int, deps echDependencies) {
	ctx, cancel := context.WithTimeout(ctx, echNodeTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return
	}
	evidence := newAssessment(node, echProbeURL, client.ECHUnknown)
	if evidence == nil {
		return
	}
	r.ECH = evidence
	if timeoutMS <= 0 {
		timeoutMS = 3000
	}
	timeout := time.Duration(min(timeoutMS, 5000)) * time.Millisecond
	plain := node.DialRaw()
	var enabled map[string]any
	var config string
	var echLatency int
	for i := range 4 {
		if ctx.Err() != nil {
			return
		}
		if i%2 == 0 {
			evidence.PlainAttempts++
			latency, dialFailure, err := deps.probe(ctx, plain, echProbeURL, timeout)
			if err == nil && ctx.Err() == nil {
				evidence.PlainSuccess++
				evidence.Decision = client.ECHPlain
				r.Initial = client.InitialResult{LatencyMS: latency}
				return
			}
			if !dialFailure {
				return
			}
			continue
		}
		if enabled == nil {
			configBytes, err := deps.lookup(ctx, host)
			if err != nil || len(configBytes) == 0 || ctx.Err() != nil {
				return
			}
			config = base64.StdEncoding.EncodeToString(configBytes)
			enabled = maps.Clone(plain)
			enabled["ech-opts"] = map[string]any{"enable": true, "config": config}
		}
		evidence.ECHAttempts++
		latency, _, err := deps.probe(ctx, enabled, echProbeURL, timeout)
		if err == nil && ctx.Err() == nil {
			evidence.ECHSuccess++
			echLatency += latency
		}
	}
	if ctx.Err() == nil && evidence.PlainAttempts == 2 && evidence.ECHSuccess == 2 {
		evidence.Decision, evidence.Config = client.ECHRequired, config
		evidence.CheckedAt = time.Now().UTC().Truncate(time.Second)
		r.ProbeRaw = enabled
		r.Initial = client.InitialResult{LatencyMS: echLatency / 2}
	}
}
