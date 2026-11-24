package config

import (
	"strings"
	"testing"
)

// An override that cannot be parsed must say so. Silently keeping the previous
// value leaves the operator believing the setting took effect - the same trap as
// a name template that existed in the database and quietly overrode the code.
func TestEnvIntWarnsOnBadValue(t *testing.T) {
	t.Setenv("INITIAL_PING_AVG", "three")
	cfg := defaults()

	out := captureLog(func() { envInt("INITIAL_PING_AVG", &cfg.Initial.PingAvg) })
	if !strings.Contains(out, "not an integer") {
		t.Fatalf("a non-numeric override must be reported, got %q", out)
	}
	if cfg.Initial.PingAvg != 3 {
		t.Fatalf("PingAvg = %d, want the default 3 preserved", cfg.Initial.PingAvg)
	}
}

func TestEnvIntAppliesValidValue(t *testing.T) {
	t.Setenv("INITIAL_PING_AVG", " 7 ")
	cfg := defaults()

	if out := captureLog(func() { envInt("INITIAL_PING_AVG", &cfg.Initial.PingAvg) }); out != "" {
		t.Fatalf("a valid override must not warn, got %q", out)
	}
	if cfg.Initial.PingAvg != 7 {
		t.Fatalf("PingAvg = %d, want 7", cfg.Initial.PingAvg)
	}
}

// SPEED_ENABLED=True used to be ignored outright: easy to write, impossible to
// notice.
func TestEnvBoolIsCaseInsensitive(t *testing.T) {
	for _, in := range []string{"true", "TRUE", "True", "1", "yes", "YES"} {
		t.Run(in, func(t *testing.T) {
			t.Setenv("SPEED_ENABLED", in)
			v := false
			envBool("SPEED_ENABLED", &v)
			if !v {
				t.Fatalf("%q must enable", in)
			}
		})
	}
}

func TestEnvBoolWarnsOnBadValue(t *testing.T) {
	t.Setenv("SPEED_ENABLED", "maybe")
	v := false

	out := captureLog(func() { envBool("SPEED_ENABLED", &v) })
	if !strings.Contains(out, "not a boolean") {
		t.Fatalf("an unparseable boolean must be reported, got %q", out)
	}
	if v {
		t.Fatal("an unparseable boolean must leave the previous value alone")
	}
}

// Without a secret every call to paily-core is rejected, so a round looks
// healthy while doing nothing.
func TestWarnMissingSecret(t *testing.T) {
	cfg := defaults()
	cfg.Core.Secret = "  "

	out := captureLog(func() { warnMissingSecret(cfg) })
	if !strings.Contains(out, "no service secret") {
		t.Fatalf("an empty secret must be reported, got %q", out)
	}

	cfg.Core.Secret = "abc"
	if out := captureLog(func() { warnMissingSecret(cfg) }); out != "" {
		t.Fatalf("a configured secret must not warn, got %q", out)
	}
}
