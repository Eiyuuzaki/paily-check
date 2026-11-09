package vendor

import (
	"testing"

	"github.com/metacubex/mihomo/adapter"
	"github.com/openpaily/paily-check/internal/interfaces"
)

func TestWireGuardTypeStringMismatch(t *testing.T) {
	raw := map[string]interface{}{
		"name": "WARP-CF", "type": "wireguard",
		"server": "engage.cloudflareclient.com", "port": 2408,
		"private-key": "nHBVqHeEZNF3fliFS2y4mq3zb6djUFxxfP2Zsd8eSzk=",
		"public-key":  "eYHK3wifnrFz0Q2JaoCHQiWAo/4wqF0IQCZZ/GqjBng=",
		"ip": "172.16.0.2", "mtu": 1280,
		"allowed-ips": []interface{}{"0.0.0.0/0", "::/0"},
	}
	p, err := adapter.ParseProxy(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ts := p.Type().String()
	t.Logf("mihomo Type().String() = %q", ts)
	parsed := interfaces.Parse(ts)
	t.Logf("interfaces.Parse(%q) = %q (Invalid=%q)", ts, parsed, interfaces.ProxyInvalid)
	if parsed == interfaces.ProxyInvalid {
		t.Fatalf("MISMATCH: mihomo reports %q but interfaces has no match -> Build returns NotReady", ts)
	}
}
