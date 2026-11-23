package checker

import (
	"log"
	"net"
	"time"

	"github.com/openpaily/paily-check/internal/client"
)

// Egress baselines. Two per family, and a family is only declared down when
// every one of its targets fails, so a single blocked target cannot be mistaken
// for a dead uplink.
//
// Domestic resolvers on purpose. The first version used 1.1.1.1 and Google's v6
// resolver, and was wrong: measured from this line on 2026-11-23 with the uplink
// confirmed healthy, 1.1.1.1:443, 2606:4700:4700::1111, 2001:4860:4860::8888 and
// 2402:4e00:: were all unreachable, while 223.5.5.5, dns.alidns.com,
// 2400:3200::1 and 2400:3200:baba::1 answered. A liveness baseline must be a
// target that is reachable whenever the line is up; picking one that is commonly
// blocked builds a machine for declaring outages that are not happening.
var (
	egressV4Targets = []string{"223.5.5.5:443", "dns.alidns.com:443"}
	egressV6Targets = []string{"[2400:3200::1]:443", "[2400:3200:baba::1]:443"}
)

// egressState records what this host could reach when a round started.
type egressState struct {
	V4 bool
	V6 bool
}

// probeEgress answers "is the uplink up" for each address family.
//
// Nodes are only as reachable as the line under them. On 2026-11-23 a reboot of
// the optical modem left the NAS with a stale IPv6 prefix: the address and the
// default route were still present, every IPv6 dial failed, and a checker round
// ran through it marking 21 of 22 IPv6 nodes dead. None of those nodes had
// changed — the house had.
//
// A round therefore has to distinguish "this node is down" from "we are down".
// The distinction cannot be made per node, because a dead node and a dead
// uplink look identical from inside; it can only be made once per round, before
// any node is touched.
func probeEgress() egressState {
	return egressState{
		V4: anyReachable(egressV4Targets),
		V6: anyReachable(egressV6Targets),
	}
}

func anyReachable(targets []string) bool {
	const (
		dialTimeout   = 4 * time.Second
		overallBudget = 6 * time.Second
	)
	deadline := time.Now().Add(overallBudget)
	for _, t := range targets {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		timeout := dialTimeout
		if left < timeout {
			timeout = left
		}
		conn, err := net.DialTimeout("tcp", t, timeout)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

// isIPv6Endpoint reports whether a node's server is a literal IPv6 address.
// Hostnames are never treated as IPv6: they may resolve to either family and
// dropping them on an IPv6 outage would discard nodes that still work.
func isIPv6Endpoint(server string) bool {
	host := server
	if h, _, err := net.SplitHostPort(server); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil
}

// skipUnreachableFamily drops the nodes the uplink cannot reach, and reports how
// many were kept back. Dropping is the safe direction: a node absent from a round
// keeps its previous state, so an outage pauses judgement instead of failing it.
func skipUnreachableFamily(nodes []client.Node, e egressState) ([]client.Node, int) {
	if e.V6 {
		return nodes, 0
	}
	kept := make([]client.Node, 0, len(nodes))
	skipped := 0
	for _, n := range nodes {
		if isIPv6Endpoint(n.Server) {
			skipped++
			continue
		}
		kept = append(kept, n)
	}
	return kept, skipped
}

func logEgress(e egressState, total, kept, skipped int) {
	switch {
	case !e.V4 && !e.V6:
		log.Printf("[egress-guard] uplink down: IPv4 and IPv6 baselines both unreachable; skipping the whole round")
	case !e.V6:
		log.Printf("[egress-guard] IPv6 unreachable: checking %d/%d nodes, held back %d IPv6 nodes (their state is left untouched)",
			kept, total, skipped)
	case !e.V4:
		log.Printf("[egress-guard] IPv4 unreachable: IPv4 nodes will be reported as failed by the checks below; IPv6 still reachable")
	}
}
