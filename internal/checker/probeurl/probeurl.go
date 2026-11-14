// Package probeurl selects the HTTP probe target for one node.
//
// A single global probe URL makes Cloudflare-hosted nodes look dead at night:
// the evening window blocks Google by destination, while the same node still
// reaches Cloudflare fine. Probing a CF node against a CF target keeps
// "blocked probe target" from being recorded as "dead node".
package probeurl

import (
	"net/netip"
	"strings"
	"sync"

	"github.com/openpaily/paily-check/internal/client"
)

// CFGenerate204 is served by Cloudflare's own edge, so a CF-Workers node only
// has to reach the Cloudflare network to answer it, not the open internet.
const CFGenerate204 = "https://cp.cloudflare.com/generate_204"

// cfPrefixes are Cloudflare's published anycast ranges. 104.16.0.0/12 covers
// the 104.16-104.31 block our entry pool rotates through; 172.64.0.0/13 covers
// 172.66/172.67.
var cfPrefixes = []string{
	"103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"104.16.0.0/12", "108.162.192.0/18", "131.0.72.0/22",
	"141.101.64.0/18", "162.158.0.0/15", "172.64.0.0/13",
	"173.245.48.0/20", "188.114.96.0/20", "190.93.240.0/20",
	"197.234.240.0/22", "198.41.128.0/17",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32",
	"2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// cfHostSuffixes identify CF Workers entry domains seen in node raw configs.
var cfHostSuffixes = []string{"workers.dev", "dpdns.org"}

var (
	prefixOnce sync.Once
	prefixes   []netip.Prefix
)

func loadPrefixes() {
	prefixOnce.Do(func() {
		for _, raw := range cfPrefixes {
			if p, err := netip.ParsePrefix(raw); err == nil {
				prefixes = append(prefixes, p)
			}
		}
	})
}

// IsCloudflare reports whether the node lives on Cloudflare's edge, either
// because its server is a CF anycast address or because its raw config points
// at a CF Workers hostname.
func IsCloudflare(node client.Node) bool {
	loadPrefixes()
	if addr, err := netip.ParseAddr(strings.TrimSpace(node.Server)); err == nil {
		addr = addr.Unmap()
		for _, p := range prefixes {
			if p.Contains(addr) {
				return true
			}
		}
	}
	for _, key := range []string{"servername", "sni", "host", "server"} {
		s, ok := node.Raw[key].(string)
		if !ok {
			continue
		}
		low := strings.ToLower(s)
		for _, suffix := range cfHostSuffixes {
			if strings.HasSuffix(low, suffix) || strings.Contains(low, "."+suffix) {
				return true
			}
		}
	}
	return false
}

// For returns the probe URL to use for this node.
func For(node client.Node, fallback string) string {
	if IsCloudflare(node) {
		return CFGenerate204
	}
	return fallback
}
