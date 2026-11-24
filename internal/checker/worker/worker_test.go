package worker

import (
	"context"
	"testing"

	"github.com/openpaily/paily-check/internal/client"
)

func nodes(ids ...string) []client.Node {
	out := make([]client.Node, 0, len(ids))
	for _, id := range ids {
		out = append(out, client.Node{ID: id})
	}
	return out
}

// A panicking fn must not take the process down. Build parses an external raw
// map, so one malformed node would otherwise discard a round that had already
// spent real quota.
func TestRunNodesSurvivesPanic(t *testing.T) {
	got := RunNodes(context.Background(), nodes("a", "b", "c"), 2, func(_ context.Context, n client.Node) int {
		if n.ID == "b" {
			panic("malformed raw")
		}
		return 1
	})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (the panicking node is left out)", len(got))
	}
	if _, ok := got["b"]; ok {
		t.Fatal("the panicking node must not appear in the result map")
	}
	if got["a"] != 1 || got["c"] != 1 {
		t.Fatalf("surviving results = %v, want a=1 c=1", got)
	}
}

func TestRunNodesKeepsAllResults(t *testing.T) {
	got := RunNodes(context.Background(), nodes("a", "b"), 4, func(_ context.Context, n client.Node) string {
		return n.ID
	})
	if len(got) != 2 || got["a"] != "a" || got["b"] != "b" {
		t.Fatalf("results = %v", got)
	}
}

// concurrency <= 0 is clamped rather than deadlocking on a zero-size semaphore.
func TestRunNodesClampsConcurrency(t *testing.T) {
	got := RunNodes(context.Background(), nodes("a", "b", "c"), 0, func(_ context.Context, n client.Node) int { return 1 })
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

// A cancelled context skips nodes instead of running them; the map is simply
// smaller, and core treats a missing entry as "no result".
func TestRunNodesSkipsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := RunNodes(ctx, nodes("a", "b"), 2, func(_ context.Context, n client.Node) int {
		t.Errorf("fn must not run for %s after cancellation", n.ID)
		return 1
	})
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
