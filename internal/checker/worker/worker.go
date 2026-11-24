// Package worker provides a generic concurrent node processor used by the
// deep-check phase.  All four deep phases (ping / geo / script / speed) share
// the same "semaphore + WaitGroup + mutex" pattern; this package encapsulates
// it so each caller only needs to supply a per-node function literal.
package worker

import (
	"context"
	"log"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/openpaily/paily-check/internal/client"
)

// RunNodes executes fn for every node in nodes, with at most concurrency
// goroutines running simultaneously.  Results are collected into a
// map[node.ID → R] and returned after all goroutines finish.
//
// Nodes whose semaphore.Acquire is interrupted by ctx cancellation are
// silently skipped (not present in the returned map).
//
// A node whose fn panics is also left out of the map rather than taking the
// process down: fn builds a mihomo proxy from a raw map that came from an
// external source, and an unrecovered panic in any goroutine terminates the
// whole program, so one malformed node would discard a round that had already
// spent real Cloudflare quota. Leaving the node out is the same outcome as a
// cancelled acquire - core sees no result and does not change its state.
//
// concurrency ≤ 0 is clamped to 1.
func RunNodes[R any](
	ctx context.Context,
	nodes []client.Node,
	concurrency int,
	fn func(ctx context.Context, node client.Node) R,
) map[string]R {
	if concurrency <= 0 {
		concurrency = 1
	}

	sem := semaphore.NewWeighted(int64(concurrency))
	results := make(map[string]R, len(nodes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, node := range nodes {
		node := node
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)

			r, ok := runNode(ctx, node, fn)
			if !ok {
				return
			}

			mu.Lock()
			results[node.ID] = r
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}

// runNode calls fn, reporting false when it panicked so the caller can leave the
// node out of the result map instead of crashing.
func runNode[R any](ctx context.Context, node client.Node, fn func(context.Context, client.Node) R) (r R, ok bool) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[worker] node %s panicked and was skipped: %v", node.ID, p)
			ok = false
		}
	}()
	return fn(ctx, node), true
}
