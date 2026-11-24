package checker

import (
	"testing"

	"github.com/openpaily/paily-check/internal/client"
)

func TestIsIPv6Endpoint(t *testing.T) {
	cases := []struct {
		server string
		want   bool
	}{
		{"2403:18c0:5:627:be24:11ff:fee5:9177", true},
		{"2605:52c0:2:67b4:be24:11ff:feb6:82ae", true},
		{"104.17.19.80", false},
		{"1.1.1.1", false},
		// Hostnames are never classified as IPv6: they may resolve either way, so
		// dropping them on an IPv6 outage would discard nodes that still work.
		{"lencf.dpdns.org", false},
		{"example.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isIPv6Endpoint(c.server); got != c.want {
			t.Errorf("isIPv6Endpoint(%q) = %v, want %v", c.server, got, c.want)
		}
	}
}

func TestIsIPv4Endpoint(t *testing.T) {
	cases := []struct {
		server string
		want   bool
	}{
		{"104.17.19.80", true},
		{"172.64.159.109", true},
		{"2403:18c0:5:627:be24:11ff:fee5:9177", false},
		{"lencf.dpdns.org", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isIPv4Endpoint(c.server); got != c.want {
			t.Errorf("isIPv4Endpoint(%q) = %v, want %v", c.server, got, c.want)
		}
	}
}

func TestSkipUnreachableFamily(t *testing.T) {
	nodes := []client.Node{
		{ID: "v4a", Server: "104.17.19.80"},
		{ID: "v6a", Server: "2403:18c0:5:627:be24:11ff:fee5:9177"},
		{ID: "host", Server: "lencf.dpdns.org"},
		{ID: "v4b", Server: "172.64.159.109"},
	}

	// IPv6 down: the IPv6 literal is held back, everything dialable is kept.
	kept, skipped := skipUnreachableFamily(nodes, egressState{V4: true, V6: false})
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(kept) != 3 {
		t.Fatalf("kept = %d, want 3", len(kept))
	}
	for _, n := range kept {
		if n.ID == "v6a" {
			t.Fatalf("IPv6 node was not held back")
		}
	}

	// IPv6 healthy: nothing is touched.
	kept, skipped = skipUnreachableFamily(nodes, egressState{V4: true, V6: true})
	if skipped != 0 || len(kept) != len(nodes) {
		t.Fatalf("healthy IPv6 must keep every node: kept=%d skipped=%d", len(kept), skipped)
	}

	// IPv4 down: the two IPv4 literals are held back, the IPv6 literal and the
	// hostname are kept. A hostname may resolve to the family that still works,
	// so it must never be discarded on a single-family outage.
	kept, skipped = skipUnreachableFamily(nodes, egressState{V4: false, V6: true})
	if skipped != 2 {
		t.Fatalf("IPv4 down: skipped = %d, want 2", skipped)
	}
	if len(kept) != 2 {
		t.Fatalf("IPv4 down: kept = %d, want 2", len(kept))
	}
	for _, n := range kept {
		if n.ID == "v4a" || n.ID == "v4b" {
			t.Fatalf("IPv4 node %q was not held back", n.ID)
		}
	}

	// Both families down: nothing can be judged, so the whole round is skipped.
	// Probing the hostname anyway would fail it for an outage that is ours.
	kept, skipped = skipUnreachableFamily(nodes, egressState{V4: false, V6: false})
	if len(kept) != 0 || skipped != len(nodes) {
		t.Fatalf("both down: kept=%d skipped=%d, want 0/%d", len(kept), skipped, len(nodes))
	}
}

// The guard must fail closed only when a whole family is gone. probeEgress is
// not unit tested: it dials real networks, and a unit test that depends on the
// host's uplink reports the machine, not the code.
func TestAnyReachableFalseOnGarbageTargets(t *testing.T) {
	if anyReachable([]string{"127.0.0.1:1", "127.0.0.1:2"}) {
		t.Fatalf("expected unreachable for closed local ports")
	}
}
