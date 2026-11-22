package client

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestECHRawSHA256Canonical(t *testing.T) {
	n := Node{Server: "192.0.2.2", ProbeRaw: map[string]any{"server": "192.0.2.3"}, Raw: map[string]any{
		"name": "display-only", "server": "2001:db8::1", "port": float64(443),
		"auth": "a&b", "ws-opts": map[string]any{"path": "/edge", "headers": map[string]any{"Host": "edge.example.com"}},
	}}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"auth":"a\u0026b","port":443,"server":"192.0.2.2","ws-opts":{"headers":{"Host":"edge.example.com"},"path":"/edge"}}`)))
	got, err := n.ECHRawSHA256()
	if err != nil || got != want {
		t.Fatalf("hash %q want %q: %v", got, want, err)
	}
	n.Raw["name"] = "renamed"
	if renamed, _ := n.ECHRawSHA256(); renamed != want {
		t.Fatal("display name changed fingerprint")
	}
	n.Raw["auth"] = "new-auth"
	if changed, _ := n.ECHRawSHA256(); changed == want {
		t.Fatal("auth excluded from fingerprint")
	}
}
