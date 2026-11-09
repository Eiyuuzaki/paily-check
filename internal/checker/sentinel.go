package checker

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// Local connectivity sentinel: gates check cycles on real Internet access.
//
// A router/ISP outage makes every probe fail, which would wrongly kill
// every alive node in the pool (2026-11-10 incident: a router reboot
// produced 1319 bogus timeouts and flipped the whole pool dead).
// The sentinel detects such outages so cycles can be skipped or results
// discarded instead of polluting node state.

// Sentinel probe targets. One domestic (always reachable in CN) plus one
// international; success of EITHER means the local stack can reach out.
var sentinelTargets = []string{
	"http://connect.rom.miui.com/generate_204",
	"https://www.gstatic.com/generate_204",
}

const (
	sentinelTimeout = 5 * time.Second

	// ok=1 after the first passing probe; used to skip redundant probes
	// within the same cycle (probeOnce caches the verdict).
	sentinelStateUnknown int32 = 0
	sentinelStateOK      int32 = 1
	sentinelStateDown    int32 = 2
)

// sentinelVerdict caches the current cycle's verdict; reset before each cycle.
var sentinelVerdict atomic.Int32

// ResetSentinel clears the cached verdict so a new cycle probes fresh.
func ResetSentinel() {
	sentinelVerdict.Store(sentinelStateUnknown)
}

// CheckSentinel reports whether the local network can reach out.
// Results are cached per cycle: pass once = pass for the cycle,
// fail once = fail for the cycle (conservative: no mid-cycle recovery).
func CheckSentinel() error {
	switch sentinelVerdict.Load() {
	case sentinelStateOK:
		return nil
	case sentinelStateDown:
		return fmt.Errorf("local network down (cached verdict)")
	}

	domesticOK, intlOK := false, false
	var lastErr error
	for i, target := range sentinelTargets {
		client := &http.Client{Timeout: sentinelTimeout}
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if i == 0 {
			domesticOK = true
		} else {
			intlOK = true
		}
	}

	if domesticOK || intlOK {
		sentinelVerdict.Store(sentinelStateOK)
		return nil
	}
	sentinelVerdict.Store(sentinelStateDown)
	if lastErr == nil {
		lastErr = fmt.Errorf("all sentinel probes failed")
	}
	return fmt.Errorf("local network down: %w", lastErr)
}

// DiscardSentinel is checked right before posting results: if the local
// network went down mid-cycle, results would be contaminated garbage and
// must not be posted.
func DiscardSentinel() error {
	return CheckSentinel()
}