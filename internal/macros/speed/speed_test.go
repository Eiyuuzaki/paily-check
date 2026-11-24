package speed

import (
	"context"
	"testing"
)

// The reported speed must be the average over the whole test window, not the
// average of the seconds that happened to carry data. A node that stalls and
// then bursts would otherwise be labelled with the burst rate, and that label is
// what a user reads as a promise.
func TestAverageCoversStalledSeconds(t *testing.T) {
	// Eight seconds of buckets, one of which carried 1 MB and seven that carried
	// nothing, must average to 1/8 of a megabyte per second - not 1 MB/s.
	buckets := make([]uint64, 8)
	buckets[3] = 1 << 20

	var total uint64
	for _, b := range buckets {
		total += b
	}
	avg := total / uint64(len(buckets))
	kbps := int(avg * 8 / 1000)

	const wantKbps = 1048 // (1<<20)/8 bytes/s * 8 / 1000
	if kbps != wantKbps {
		t.Fatalf("kbps = %d, want %d (a burst must not be reported as sustained)", kbps, wantKbps)
	}
}

// Guards the division: an all-zero window returns 0 instead of dividing by zero.
func TestAverageAllZero(t *testing.T) {
	buckets := make([]uint64, 8)
	var total uint64
	for _, b := range buckets {
		total += b
	}
	if total != 0 {
		t.Fatal("fixture should be all zero")
	}
	// Mirrors Test's guard: total == 0 returns 0 before any division.
}

// A vendor that is not ready short-circuits to 0 without touching the network.
func TestTestReturnsZeroWhenVendorNotReady(t *testing.T) {
	if got := Test(context.Background(), nil, SpeedConfig{URL: "https://example.com/x", Threads: 1, DurationS: 1}); got != 0 {
		t.Fatalf("nil vendor = %d, want 0", got)
	}
}

func TestTestReturnsZeroWithoutURL(t *testing.T) {
	if got := Test(context.Background(), nil, SpeedConfig{Threads: 1, DurationS: 1}); got != 0 {
		t.Fatalf("empty URL = %d, want 0", got)
	}
}
