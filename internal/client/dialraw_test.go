package client

import "testing"

func TestDialRawPrefersIPv4WhenRawIsIPv6(t *testing.T) {
	n := Node{Server: "172.66.137.0", Raw: map[string]interface{}{"server": "2605:52c0:2:70b1::1", "port": float64(443), "uuid": "u"}}
	got := n.DialRaw()
	if got["server"] != "172.66.137.0" {
		t.Fatalf("server=%v want ipv4", got["server"])
	}
	if n.Raw["server"] != "2605:52c0:2:70b1::1" {
		t.Fatalf("original raw mutated: %v", n.Raw["server"])
	}
	if got["port"] != float64(443) || got["uuid"] != "u" {
		t.Fatalf("other fields lost: %v", got)
	}
}

func TestDialRawLeavesIPv4Raw(t *testing.T) {
	n := Node{Server: "1.2.3.4", Raw: map[string]interface{}{"server": "5.6.7.8"}}
	if got := n.DialRaw(); got["server"] != "5.6.7.8" {
		t.Fatalf("server=%v want unchanged", got["server"])
	}
}

func TestDialRawNoIPv4Fallback(t *testing.T) {
	n := Node{Server: "2600::9", Raw: map[string]interface{}{"server": "2605:52c0::1"}}
	if got := n.DialRaw(); got["server"] != "2605:52c0::1" {
		t.Fatalf("server=%v want unchanged v6", got["server"])
	}
}

func TestDialRawDomainServer(t *testing.T) {
	n := Node{Server: "edge.example.com", Raw: map[string]interface{}{"server": "2605:52c0::1"}}
	if got := n.DialRaw(); got["server"] != "2605:52c0::1" {
		t.Fatalf("server=%v want unchanged (server not ipv4)", got["server"])
	}
}

func TestDialRawNilRaw(t *testing.T) {
	n := Node{Server: "1.2.3.4"}
	if got := n.DialRaw(); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}
