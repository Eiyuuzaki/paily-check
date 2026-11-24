// Package probeurl selects the HTTP probe targets for one node.
//
// A single global probe URL is wrong in two directions. The evening window
// blocks Google by destination, so a Google target records working nodes as
// dead (2026-11-11). And a Cloudflare target answers "can this node reach
// Cloudflare", not "can this node reach the internet": for a CF-hosted node it
// measures the CF network against itself, and a connect()-based relay trips
// CF's loop protection there (2026-11-19). Targets therefore leads with two
// fixed neutral targets and appends the configured fallback only when that
// adds coverage. No CF target is ever emitted.
package probeurl

import (
	"github.com/openpaily/paily-check/internal/client"
)

// CFGenerate204 is served by Cloudflare's own edge. It is never emitted as a
// probe target: for a CF-hosted node it measures the CF network against itself,
// and a connect()-based relay trips CF's loop protection on it. The constant is
// kept only so a configured fallback equal to it is recognised and skipped.
const CFGenerate204 = "https://cp.cloudflare.com/generate_204"

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
