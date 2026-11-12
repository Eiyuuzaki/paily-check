package client

import "testing"

// Regression guard for the 2026-11-12 hidencloud false-death: ech-opts must
// only land on nodes whose server domain advertises an ech= HTTPS record.
// These tests only cover the pure-logic gates (nil/tls/reality/existing);
// the DNS gate is exercised live by the checker against hidenplay.net
// (no record -> no injection) and lencf.dpdns.org (record -> injection).
func TestInjectECHGates(t *testing.T) {
	// nil raw: no panic, no injection
	injectECH(nil)

	// non-TLS node: untouched
	nonTLS := map[string]interface{}{"tls": false, "server": "example.com"}
	injectECH(nonTLS)
	if _, has := nonTLS["ech-opts"]; has {
		t.Fatal("ech-opts injected into non-TLS node")
	}

	// REALITY node: untouched
	reality := map[string]interface{}{
		"tls": true, "server": "example.com",
		"reality-opts": map[string]interface{}{"public-key": "x"},
	}
	injectECH(reality)
	if _, has := reality["ech-opts"]; has {
		t.Fatal("ech-opts injected into REALITY node")
	}

	// IP-literal server (no domain, no type65 record possible): untouched
	ipNode := map[string]interface{}{"tls": true, "server": "138.199.231.247"}
	injectECH(ipNode)
	if _, has := ipNode["ech-opts"]; has {
		t.Fatal("ech-opts injected into IP-literal node")
	}

	// node that already carries ech-opts: kept as-is
	withOpts := map[string]interface{}{
		"tls": true, "server": "lencf.dpdns.org",
		"ech-opts": map[string]interface{}{"enable": true, "config": "AA"},
	}
	injectECH(withOpts)
	o := withOpts["ech-opts"].(map[string]interface{})
	if o["config"] != "AA" {
		t.Fatal("existing ech-opts were overwritten")
	}
}
