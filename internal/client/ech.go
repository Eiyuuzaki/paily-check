package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"time"
)

const (
	ECHPlain    = "plain"
	ECHRequired = "required"
	ECHUnknown  = "unknown"
)

// ECHAssessment is a path-specific observation from one check cycle.
// Its Config is the exact list used for both successful ECH samples.
type ECHAssessment struct {
	Version       int       `json:"version"`
	Decision      string    `json:"decision"`
	RawSHA256     string    `json:"raw_sha256"`
	ProbeURL      string    `json:"probe_url"`
	PlainAttempts int       `json:"plain_attempts"`
	PlainSuccess  int       `json:"plain_success"`
	ECHAttempts   int       `json:"ech_attempts"`
	ECHSuccess    int       `json:"ech_success"`
	Config        string    `json:"config,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

// ECHRawSHA256 fingerprints the unmodified dialing input (the effective
// server after DialRaw's IPv4 preference). Names are display-only.
func (n Node) ECHRawSHA256() (string, error) {
	n.ProbeRaw = nil
	raw := maps.Clone(n.DialRaw())
	delete(raw, "name")
	data, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
