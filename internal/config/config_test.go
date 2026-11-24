package config

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// The rescue in initial.RunAll only helps a node whose true latency falls
// between the ping timeout and the alive threshold. When the threshold is not
// above the timeout that range is empty, and the configuration silently loses a
// safety net; say so once at load.
func TestWarnUnreachableRescue(t *testing.T) {
	cases := []struct {
		name      string
		timeout   int
		threshold int
		wantWarn  bool
	}{
		{"equal (shipped default and production)", 3000, 3000, true},
		{"threshold below timeout", 6000, 4000, true},
		{"threshold above timeout", 3000, 8000, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&buf)
			defer log.SetOutput(prev)

			cfg := defaults()
			cfg.Initial.PingTimeoutMS = c.timeout
			cfg.Initial.AliveThresholdMS = c.threshold
			warnUnreachableRescue(cfg)

			got := strings.Contains(buf.String(), "slow-node rescue cannot fire")
			if got != c.wantWarn {
				t.Fatalf("warn = %v, want %v (log: %q)", got, c.wantWarn, buf.String())
			}
		})
	}
}
