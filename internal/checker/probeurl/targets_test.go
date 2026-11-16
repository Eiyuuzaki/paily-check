package probeurl

import (
	"testing"

	"github.com/openpaily/paily-check/internal/client"
)

func TestTargetsCFSingle(t *testing.T) {
	n := client.Node{Server: "104.16.1.1"}
	ts := Targets(n, "https://www.gstatic.com/generate_204")
	if len(ts) != 1 || ts[0] != CFGenerate204 {
		t.Fatalf("cf targets=%v", ts)
	}
}

func TestTargetsNonCFOrdered(t *testing.T) {
	n := client.Node{Server: "1.2.3.4"}
	ts := Targets(n, "https://www.gstatic.com/generate_204")
	if len(ts) < 3 || ts[0] != CFGenerate204 || ts[len(ts)-1] != "https://www.gstatic.com/generate_204" {
		t.Fatalf("noncf targets=%v", ts)
	}
}

func TestTargetsNonCFNoDupFallback(t *testing.T) {
	n := client.Node{Server: "1.2.3.4"}
	ts := Targets(n, CFGenerate204)
	if len(ts) != 2 {
		t.Fatalf("want 2 (cf+apple), got %v", ts)
	}
}
