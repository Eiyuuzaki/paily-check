package request

import (
	"strings"
	"testing"
	"time"
)

// A non-positive timeout must not produce an already-expired context. Scripts
// pass this value straight from JS, so a typo would otherwise make every request
// fail instantly with a network-shaped error message.
func TestClampTimeoutMS(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want time.Duration
	}{
		{"zero falls back", 0, defaultAttemptTimeoutMS * time.Millisecond},
		{"negative falls back", -5000, defaultAttemptTimeoutMS * time.Millisecond},
		{"positive is honoured", 1500, 1500 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampTimeoutMS(tc.in); got != tc.want {
				t.Fatalf("clampTimeoutMS(%d) = %v, want %v", tc.in, got, tc.want)
			}
			if got := clampTimeoutMS(tc.in); got <= 0 {
				t.Fatalf("clampTimeoutMS(%d) returned a non-positive duration %v", tc.in, got)
			}
		})
	}
}

// readAllLimited must reject an oversized body rather than return a silently
// short one: a truncated body that looks complete is harder to notice than a
// failure.
func TestReadAllLimited(t *testing.T) {
	t.Run("under limit passes through", func(t *testing.T) {
		got, err := readAllLimited(strings.NewReader("hello"), 16)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "hello" {
			t.Fatalf("got %q, want hello", got)
		}
	})

	t.Run("exactly at limit passes", func(t *testing.T) {
		got, err := readAllLimited(strings.NewReader("1234567890"), 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 10 {
			t.Fatalf("len = %d, want 10", len(got))
		}
	})

	t.Run("over limit is an error", func(t *testing.T) {
		if _, err := readAllLimited(strings.NewReader("12345678901"), 10); err == nil {
			t.Fatal("an oversized body must be an error, not a truncated read")
		}
	})
}
