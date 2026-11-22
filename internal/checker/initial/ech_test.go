package initial

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openpaily/paily-check/internal/client"
)

func testNode() client.Node {
	return client.Node{ID: "node", Server: "192.0.2.2", Raw: map[string]any{
		"type": "vless", "network": "ws", "tls": true, "servername": "edge.example.com", "server": "2001:db8::1", "port": float64(443),
		"ws-opts": map[string]any{"path": "/edge"},
	}}
}

func TestECHHostnameGates(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"no tls", "tls", false}, {"unsupported protocol", "type", "http"}, {"non-ws", "network", "grpc"},
		{"source off", "ech-opts", map[string]any{"enable": false}}, {"source sing-box", "ech", nil},
		{"reality", "reality-opts", nil}, {"shadow TLS", "shadow-tls-opts", nil},
		{"dns entrance", "server", "backend.example.com"}, {"ip sni", "servername", "192.0.2.1"},
		{"chain", "dialer-proxy", "other"}, {"mux", "smux", nil},
		{"early data", "ws-opts", map[string]any{"path": "/ws?ed=256"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := testNode()
			n.Raw[tc.key] = tc.value
			if got := echHostname(n.DialRaw()); got != "" {
				t.Fatalf("eligible host %q", got)
			}
		})
	}
	n := testNode()
	if got := echHostname(n.DialRaw()); got != "edge.example.com" {
		t.Fatalf("host %q", got)
	}
}

func TestAssessECH(t *testing.T) {
	for _, tc := range []struct {
		name, outcomes, decision       string
		lookupErr                      bool
		plain, ech, succeeded, queries int
	}{
		{"required", "dxdx", client.ECHRequired, false, 2, 2, 2, 1},
		{"ordinary first", "x", client.ECHPlain, false, 1, 0, 0, 0},
		{"ordinary second", "dxx", client.ECHPlain, false, 2, 1, 1, 1},
		{"both fail", "dfdf", client.ECHUnknown, false, 2, 2, 0, 1},
		{"one ECH success", "dxdf", client.ECHUnknown, false, 2, 2, 1, 1},
		{"first plain target error", "f", client.ECHUnknown, false, 1, 0, 0, 0},
		{"second plain target error", "dxf", client.ECHUnknown, false, 2, 1, 1, 1},
		{"DoH failure", "d", client.ECHUnknown, true, 1, 0, 0, 1},
		{"cancel after final success", "dxdc", client.ECHUnknown, false, 2, 2, 1, 1},
		{"cancel after first failure", "a", client.ECHUnknown, false, 1, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := testNode()
			r := client.CheckResult{Initial: client.InitialResult{LatencyMS: -1, FailReason: "conn_reset"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			errProbe := errors.New("fixture failure")
			var calls, lookups int
			outcomes := tc.outcomes
			deps := echDependencies{
				lookup: func(_ context.Context, host string) ([]byte, error) {
					lookups++
					if host != "edge.example.com" {
						t.Fatalf("query host %q", host)
					}
					if tc.lookupErr {
						return nil, errProbe
					}
					return []byte{1, 2, 3}, nil
				},
				probe: func(ctx context.Context, raw map[string]any, target string, timeout time.Duration) (int, bool, error) {
					if target != echProbeURL || timeout != 5*time.Second {
						t.Fatalf("unexpected target %q timeout %v", target, timeout)
					}
					if left := time.Until(mustDeadline(t, ctx)); left > echNodeTimeout {
						t.Fatalf("budget %v", left)
					}
					copyRaw := maps.Clone(raw)
					opt, has := copyRaw["ech-opts"]
					delete(copyRaw, "ech-opts")
					if !reflect.DeepEqual(copyRaw, n.DialRaw()) || has != (calls%2 == 1) {
						t.Fatalf("unexpected mode or dialing config at call %d", calls)
					}
					if has && !reflect.DeepEqual(opt, map[string]any{"enable": true, "config": "AQID"}) {
						t.Fatal("wrong explicit ECH config")
					}
					if calls >= len(outcomes) {
						t.Fatalf("extra attempt %d", calls)
					}
					step := outcomes[calls]
					calls++
					switch step {
					case 'd':
						return 0, true, errProbe
					case 'x':
						return 10 + calls, false, nil
					case 'a':
						cancel()
						return 0, true, errProbe
					case 'c':
						cancel()
						return 10, false, nil
					default:
						return 0, false, errProbe
					}
				},
			}
			assessECH(ctx, n, &r, "edge.example.com", 9000, deps)
			e := r.ECH
			if calls != len(outcomes) || lookups != tc.queries || e.Decision != tc.decision || e.PlainAttempts != tc.plain || e.ECHAttempts != tc.ech || e.ECHSuccess != tc.succeeded {
				t.Fatalf("attempts=%d queries=%d evidence=%+v", calls, lookups, e)
			}
			if tc.decision == client.ECHRequired {
				if e.Config != "AQID" || r.Initial.LatencyMS != 13 || r.ProbeRaw["server"] != "192.0.2.2" {
					t.Fatalf("required result %+v", r)
				}
			} else if e.Config != "" || r.ProbeRaw != nil {
				t.Fatalf("unexpected injection %+v", r)
			}
			if _, exists := n.Raw["ech-opts"]; exists || n.Raw["server"] != "2001:db8::1" {
				t.Fatal("source raw mutated")
			}
		})
	}
}

func mustDeadline(t *testing.T, ctx context.Context) time.Time {
	t.Helper()
	d, ok := ctx.Deadline()
	if !ok {
		t.Fatal("probe context has no deadline")
	}
	return d
}

func TestECHComparisonBudget(t *testing.T) {
	nodes := make([]client.Node, 45)
	results := make([]client.CheckResult, len(nodes))
	for i := range nodes {
		nodes[i] = testNode()
		results[i].Initial = client.InitialResult{LatencyMS: -1, FailReason: "tls_handshake"}
	}
	results[0].Initial.FailReason = "dns"
	recordOrdinarySuccess(nodes[1], &results[1], "https://ordinary.example.com")
	var active, maxActive, count atomic.Int32
	deps := echDependencies{probe: func(_ context.Context, _ map[string]any, _ string, _ time.Duration) (int, bool, error) {
		n := active.Add(1)
		for prior := maxActive.Load(); n > prior && !maxActive.CompareAndSwap(prior, n); prior = maxActive.Load() {
		}
		count.Add(1)
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return 1, false, nil
	}}
	runECHComparisons(context.Background(), nodes, results, 0, 3000, deps)
	if n := count.Load(); n != 0 {
		t.Fatalf("disabled comparisons made %d requests", n)
	}
	runECHComparisons(context.Background(), nodes, results, 99, 3000, deps)
	if count.Load() != 40 || maxActive.Load() != 2 {
		t.Fatalf("dialed %d, concurrent %d", count.Load(), maxActive.Load())
	}
	if results[0].ECH != nil || results[1].ECH.ProbeURL != "https://ordinary.example.com" || results[42].ECH != nil {
		t.Fatal("candidate order or gate changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runECHComparisons(ctx, nodes, results, 40, 3000, deps)
	if count.Load() != 40 {
		t.Fatal("started probes after cancellation")
	}
}
