package vendor

import "sync/atomic"

// dials counts outbound dial attempts made through this package.
//
// Every probe the pipeline performs ends in ClashVendor.DialTCP or DialUDP, so
// this single counter is the cheapest honest measure of what a check actually
// costs. It exists to answer one question with numbers instead of estimates:
// a round is believed to cost ~25 requests per node, and that budget decides the
// Cloudflare quota, the round duration and how much idle window the on-demand
// check has left. Counting at the dial boundary avoids instrumenting every
// phase, and cannot drift out of sync with reality the way a per-phase tally can.
//
// The pipeline runs its phases sequentially, so a snapshot taken between phases
// gives that phase's cost without threading a context value everywhere.
var dials atomic.Int64

// Dials reports how many outbound dials have been attempted since process start.
func Dials() int64 { return dials.Load() }

// resetDials zeroes the counter. Test-only helper.
func resetDials() { dials.Store(0) }
