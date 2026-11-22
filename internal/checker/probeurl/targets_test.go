package probeurl

import (
	"testing"

	"github.com/openpaily/paily-check/internal/client"
)

// Every node type must lead with the neutral Fastly target: probing a CF node
// against a CF target measures the CF network against itself, and Apple is not
// neutral from mainland China (its DNS resolves to an Alibaba CDN there).
func TestTargetsNeutralFirstForCF(t *testing.T) {
	n := client.Node{Server: "104.16.1.1"}
	ts := Targets(n, "https://www.gstatic.com/generate_204")
	if len(ts) < 2 || ts[0] != AppleCaptive || ts[1] != NeutralProbe {
		t.Fatalf("cf targets=%v", ts)
	}
	if ts[len(ts)-1] != "https://www.gstatic.com/generate_204" {
		t.Fatalf("gstatic should be the last resort: %v", ts)
	}
}

func TestTargetsNeutralFirstForNonCF(t *testing.T) {
	n := client.Node{Server: "1.2.3.4"}
	ts := Targets(n, "https://www.gstatic.com/generate_204")
	if len(ts) < 3 || ts[0] != AppleCaptive || ts[1] != NeutralProbe {
		t.Fatalf("noncf targets=%v", ts)
	}
	if ts[len(ts)-1] != "https://www.gstatic.com/generate_204" {
		t.Fatalf("gstatic fallback missing: %v", ts)
	}
}

func TestTargetsNoDupFallback(t *testing.T) {
	n := client.Node{Server: "1.2.3.4"}
	for _, fb := range []string{NeutralProbe, AppleCaptive, CFGenerate204} {
		ts := Targets(n, fb)
		if len(ts) != 2 {
			t.Fatalf("fallback %q should not be appended, got %v", fb, ts)
		}
	}
}

// CF must never be the first target for any node: a connect()-based relay trips
// Cloudflare's loop protection on CF-hosted destinations (2026-11-19).
func TestTargetsNeverCFFirst(t *testing.T) {
	for _, srv := range []string{"104.16.1.1", "1.2.3.4", "2606:4700::1"} {
		ts := Targets(client.Node{Server: srv}, "")
		if ts[0] == CFGenerate204 {
			t.Fatalf("server %s leads with CF target: %v", srv, ts)
		}
	}
}
