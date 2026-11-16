package client

import "testing"

func TestPathKeyV1IgnoresCredentials(t *testing.T) {
	a := Node{Protocol: "vless", Server: "1.2.3.4", Raw: map[string]interface{}{"port": float64(443), "servername": "edge.example", "uuid": "a"}}
	b := a
	b.Raw = map[string]interface{}{"port": float64(443), "servername": "edge.example", "uuid": "b"}
	if PathKeyV1(a) != PathKeyV1(b) {
		t.Fatal("credentials must not affect path identity")
	}
}

func TestPathKeyV1SeparatesTransportPath(t *testing.T) {
	a := Node{Protocol: "vless", Server: "1.2.3.4", Raw: map[string]interface{}{"port": float64(443), "servername": "edge.example", "network": "ws", "path": "/a"}}
	b := Node{Protocol: "vless", Server: "1.2.3.4", Raw: map[string]interface{}{"port": float64(443), "servername": "edge.example", "network": "ws", "path": "/b"}}
	if PathKeyV1(a) == PathKeyV1(b) {
		t.Fatal("different transport paths must not collide")
	}
}
