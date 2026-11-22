package checker

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/openpaily/paily-check/internal/checker/progress"
	"github.com/openpaily/paily-check/internal/client"
	"github.com/openpaily/paily-check/internal/config"
	"github.com/openpaily/paily-check/internal/interfaces"
)

// Embedded Vendor supplies unused methods; all actual test traffic is local.
type captureVendor struct {
	interfaces.Vendor
	built []map[string]any
}

func (v *captureVendor) Build(raw map[string]any) interfaces.Vendor {
	v.built = append(v.built, maps.Clone(raw))
	return v
}

func (v *captureVendor) Status() interfaces.VendorStatus { return interfaces.VStatusNotReady }
func (v *captureVendor) ProxyInfo() interfaces.ProxyInfo { return interfaces.ProxyInfo{} }
func (v *captureVendor) ProxyProfile() string            { return "{}" }
func (v *captureVendor) DialTCP(context.Context, string, interfaces.RequestOptionsNetwork) (net.Conn, error) {
	return nil, errors.New("fixture: offline")
}

func TestDeepUsesSelectedECHDialRaw(t *testing.T) {
	for _, decision := range []string{client.ECHRequired, client.ECHPlain, "source"} {
		t.Run(decision, func(t *testing.T) {
			node := client.Node{ID: "id", Server: "192.0.2.2", Raw: map[string]any{"server": "2001:db8::1", "name": "fixture"}}
			if decision == "source" {
				node.Raw["ech-opts"] = map[string]any{"enable": false, "config": "source"}
			}
			original := maps.Clone(node.Raw)
			cfg := &config.Config{Deep: config.DeepConfig{Speed: config.SpeedConfig{Enabled: true}}}
			v := &captureVendor{}
			var initialRaw map[string]any
			results := run(context.Background(), []client.Node{node}, v, cfg, func(_ context.Context, nodes []client.Node, vendor interfaces.Vendor, _ *config.InitialConfig, _ *progress.Tracker) []client.CheckResult {
				initialRaw = nodes[0].DialRaw()
				res := client.CheckResult{NodeID: node.ID, Initial: client.InitialResult{LatencyMS: 5}}
				if decision == client.ECHRequired {
					initialRaw["ech-opts"] = map[string]any{"enable": true, "config": "AQID"}
					res.ProbeRaw = initialRaw
				}
				if decision != "source" {
					res.ECH = &client.ECHAssessment{Version: 1, Decision: decision}
				}
				vendor.Build(initialRaw)
				return []client.CheckResult{res}
			})
			if len(v.built) != 5 {
				t.Fatalf("want initial and four deep builds; got %d", len(v.built))
			}
			for i, raw := range v.built {
				if !reflect.DeepEqual(initialRaw, raw) || raw["server"] != "192.0.2.2" {
					t.Fatalf("dial snapshot differs at phase %d: %v", i, raw)
				}
			}
			if !reflect.DeepEqual(node.Raw, original) {
				t.Fatal("source raw changed")
			}
			if got := results[0].Deep.Metadata["ech_assessment"]; decision != "source" && got != results[0].ECH {
				t.Fatalf("missing evidence %v", got)
			}
			payload, err := json.Marshal(results)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), "ProbeRaw") || strings.Contains(string(payload), "fixture") {
				t.Fatalf("internal dial config leaked: %s", payload)
			}
		})
	}
}
