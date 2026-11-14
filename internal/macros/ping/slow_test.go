package ping

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The rescue path must not fire for non-timeout failures: waiting longer
// cannot fix a refused connection, so those stay classified as they were.
func TestClassifyNonTimeoutUnchanged(t *testing.T) {
	cases := map[string]string{
		"connection refused":       "conn_refused",
		"no such host":             "dns",
		"tls: handshake failure":   "tls_handshake",
		"connection reset by peer": "conn_reset",
	}
	for msg, want := range cases {
		if got := classifyPingError(errors.New(msg)); got != want {
			t.Errorf("classifyPingError(%q) = %q, want %q", msg, got, want)
		}
	}
}

// slowRetry must return immediately for a cancelled context instead of
// sleeping through the doubled budget.
func TestSlowRetryHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, ok := slowRetry(ctx, nil, "https://example.com", 5*time.Second); ok {
		t.Fatal("slowRetry reported success with a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("slowRetry blocked for %v on a cancelled context", elapsed)
	}
}
