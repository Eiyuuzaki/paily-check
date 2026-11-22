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

// AppleCaptive is Apple's captive-portal probe. It is stable from mainland
// China, so it stays as a fallback, but it is NOT neutral: mainland DNS
// resolves it to an Alibaba Cloud CDN node (…w.alikunlun.com) while the rest of
// the world reaches Apple's own edge. Measuring a CF Worker against it therefore
// compares two different services — 2026-11-21 measured 81ms direct from China
// versus 896-2194ms through a Worker on the same target, which is geography of
// the resolved backend, not node quality.
const AppleCaptive = "https://captive.apple.com/hotspot-detect.html"

// NeutralProbe is the primary probe target for every node.
//
// Requirements it satisfies, in the order they were learned:
//   - Fastly-backed (mozilla.map.fastly.net), so the same anycast CDN answers
//     wherever the probe egresses from: a Worker colo and a mainland line are
//     both near a Fastly POP. That symmetry is what makes the number comparable.
//   - Not Cloudflare. A CF-hosted node probing a CF-hosted target measures the
//     CF network against itself; and connect()-based relays trip CF's loop
//     protection on CF destinations (2026-11-19: conn_reset on a healthy node).
//   - Not Google. gstatic is blocked by destination during the evening window,
//     which recorded working nodes as dead (2026-11-11).
//   - Fixed small body ("success\n"), so a captive portal or CDN error page
//     cannot pass as a clean answer.
const NeutralProbe = "https://detectportal.firefox.com/success.txt"

// Targets returns the ordered probe targets for a node; the first that answers
// wins. NeutralProbe leads for every node type, AppleCaptive is the fallback,
// and the configured fallback (gstatic) is appended last when it adds a target
// the list does not already cover.
func Targets(node client.Node, fallback string) []string {
	// Apple leads: measured from this deployment's own line (Jiangxi Mobile,
	// 2026-11-21) it answers in 60ms while the Fastly target needs 1194ms, so
	// leading with Fastly added ~1.2s of baseline to every reading and moved
	// 75% of nodes to a worse number without a single node changing.
	// NeutralProbe stays as the second target for when Apple degrades.
	// A CF target is never used: probing a CF-hosted node against a CF-hosted
	// destination measures Cloudflare against itself, and a connect()-based
	// relay trips CF loop protection there.
	out := []string{AppleCaptive, NeutralProbe}
	if fallback != "" && fallback != AppleCaptive && fallback != NeutralProbe && fallback != CFGenerate204 {
		out = append(out, fallback)
	}
	return out
}
