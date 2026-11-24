// Package initial implements the fast initial latency pass over all nodes.
// Only nodes within the alive threshold continue to detailed checks.
package initial

import (
	"context"
	"log"
	"sync"

	"github.com/openpaily/paily-check/internal/checker/probeurl"
	"github.com/openpaily/paily-check/internal/checker/progress"
	"github.com/openpaily/paily-check/internal/client"
	"github.com/openpaily/paily-check/internal/config"
	"github.com/openpaily/paily-check/internal/interfaces"
	pingMacro "github.com/openpaily/paily-check/internal/macros/ping"
)

// RunAll performs an initial ping on every node concurrently (up to
// cfg.Concurrency at a time) using vendorBase.Build(node.DialRaw()) to create
// per-node proxy instances.
//
// Returns one client.CheckResult per node. Deep is always nil (filled in
// detailed checks). The caller uses InitialResult.LatencyMS to decide whether a
// node advances.
func RunAll(ctx context.Context, nodes []client.Node, vendorBase interfaces.Vendor, cfg *config.InitialConfig, tracker *progress.Tracker) []client.CheckResult {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 50
	}

	// A buffered channel, not semaphore.Weighted. Acquire reports context
	// cancellation as an error, and there is no correct result to record for a
	// node that was never probed: writing -1 reports "dead" for a node the round
	// simply did not reach, which the windowed verdict (ok_count >= bad_count)
	// then turns into a kill after two such rounds. Run is handed a context that
	// is never cancelled — cmd/checker passes context.Background() — so that path
	// could not run anyway; a channel has no such path at all.
	sem := make(chan struct{}, concurrency)
	results := make([]client.CheckResult, len(nodes))
	wg := sync.WaitGroup{}

	for i, node := range nodes {
		i, node := i, node
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer tracker.Inc()

			sem <- struct{}{}
			defer func() { <-sem }()

			// An unrecovered panic in any goroutine terminates the whole program,
			// and Build parses a raw map that came from an external source. This
			// node is recorded as dead rather than omitted: unlike a node the round
			// never reached, this one was reached and its configuration broke the
			// parser, so it cannot be dialled either. The distinct reason keeps it
			// diagnosable instead of looking like an ordinary timeout.
			defer func() {
				if p := recover(); p != nil {
					log.Printf("[initial] node %s panicked and is reported dead: %v", node.ID, p)
					results[i] = client.CheckResult{
						NodeID:  node.ID,
						Initial: client.InitialResult{LatencyMS: -1, FailReason: "panic"},
					}
				}
			}()

			v := vendorBase.Build(node.DialRaw())
			pr := pingMacro.Ping(ctx, v, pingMacro.PingConfig{
				URLs:        probeurl.Targets(node, cfg.PingURL),
				Avg:         cfg.PingAvg,
				TimeoutMS:   cfg.PingTimeoutMS,
				SampleGapMS: cfg.PingGapMS,
			})

			results[i] = client.CheckResult{
				NodeID:  node.ID,
				Initial: client.InitialResult{LatencyMS: pr.LatencyMS, FailReason: pr.FailReason},
			}
			recordOrdinarySuccess(node, &results[i], pr.ObservedURL)
		}()
	}

	wg.Wait()
	runECHComparisons(ctx, nodes, results, cfg.ECHMaxCandidates, cfg.PingTimeoutMS, productionECH(vendorBase))
	return results
}
