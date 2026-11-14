package probeurl

import (
	"testing"

	"github.com/openpaily/paily-check/internal/client"
)

func TestForPicksCFTargetForAnycastServer(t *testing.T) {
	// 172.66/104.21 are the entry addresses our pool rotates through.
	for _, server := range []string{"172.66.203.0", "104.21.88.0", "162.159.140.1"} {
		got := For(client.Node{Server: server}, "https://www.gstatic.com/generate_204")
		if got != CFGenerate204 {
			t.Errorf("server %s: got %q, want CF target", server, got)
		}
	}
}

func TestForPicksCFTargetForWorkerHostname(t *testing.T) {
	raw := map[string]interface{}{"servername": "lencf.dpdns.org"}
	if got := For(client.Node{Server: "203.0.113.9", Raw: raw}, "fallback"); got != CFGenerate204 {
		t.Errorf("servername dpdns.org: got %q, want CF target", got)
	}
	raw = map[string]interface{}{"host": "abc.workers.dev"}
	if got := For(client.Node{Server: "203.0.113.9", Raw: raw}, "fallback"); got != CFGenerate204 {
		t.Errorf("host workers.dev: got %q, want CF target", got)
	}
}

func TestForKeepsFallbackForOtherNodes(t *testing.T) {
	raw := map[string]interface{}{"servername": "example.com"}
	if got := For(client.Node{Server: "203.0.113.9", Raw: raw}, "fallback"); got != "fallback" {
		t.Errorf("non-CF node: got %q, want fallback", got)
	}
	if got := For(client.Node{Server: "not-an-ip"}, "fallback"); got != "fallback" {
		t.Errorf("unparsable server: got %q, want fallback", got)
	}
}
