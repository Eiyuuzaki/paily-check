package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openpaily/paily-check/internal/vendor"
)

const apiBodyTimeout = 7 * time.Minute

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// Node is a proxy node returned by GET /api/v1/check/nodes.
type Node struct {
	ID       string                 `json:"id"`
	Hash     string                 `json:"hash"`
	Server   string                 `json:"server"`
	Protocol string                 `json:"protocol"`
	Raw      map[string]interface{} `json:"raw"`
	ProbeRaw map[string]interface{} `json:"-"` // selected dialing snapshot for this check cycle
}

// InitialResult carries the outcome of the initial latency check.
// LatencyMS == -1 means every ping attempt timed out.
type InitialResult struct {
	LatencyMS  int    `json:"latency_ms"`
	FailReason string `json:"fail_reason,omitempty"`
}

// DeepResult carries the outcome of the detailed checks.
// SpeedKbps is a pointer so it is omitted from JSON when the speed test is
// disabled (nil pointer → omitempty).
type DeepResult struct {
	AvgLatencyMS int             `json:"avg_latency_ms"`
	JitterMS     int             `json:"jitter_ms"`
	SpeedKbps    *int            `json:"speed_kbps,omitempty"`
	Region       string          `json:"region,omitempty"`
	RegionSource string          `json:"region_source,omitempty"`
	Streaming    map[string]bool `json:"streaming,omitempty"`
	Metadata     map[string]any  `json:"metadata,omitempty"`
}

// CheckResult is one entry in the POST /api/v1/check/results payload.
// Deep is omitted when the node failed the initial latency check.
type CheckResult struct {
	NodeID   string                 `json:"node_id"`
	Initial  InitialResult          `json:"initial"`
	Deep     *DeepResult            `json:"deep,omitempty"`
	ECH      *ECHAssessment         `json:"-"`
	ProbeRaw map[string]interface{} `json:"-"`
}

// ---------------------------------------------------------------------------
// API wire types
// ---------------------------------------------------------------------------

type checkNodesResponse struct {
	Nodes []Node `json:"nodes"`
}

type checkResultsRequest struct {
	Results []CheckResult `json:"results"`
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client communicates with the core API over HTTP.
// All requests carry Authorization: Bearer <Secret>.
type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

// New creates a Client and validates the core API base URL.
func New(baseURL, secret string) (*Client, error) {
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("client: invalid base URL %q", baseURL)
	}
	return &Client{
		baseURL: parsed.String(),
		secret:  secret,
		http: &http.Client{
			Timeout: apiBodyTimeout,
		},
	}, nil
}

func (c *Client) endpoint(parts ...string) (string, error) {
	endpoint, err := url.JoinPath(c.baseURL, parts...)
	if err != nil {
		return "", fmt.Errorf("client: join endpoint: %w", err)
	}
	return endpoint, nil
}

// GetNodes fetches all node metadata from paily-core.
// On HTTP or parse error the error is returned and the caller should retry
// on the next cron tick.
func (c *Client) GetNodes() ([]Node, error) {
	endpoint, err := c.endpoint("api", "v1", "check", "nodes")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	ctx, cancel := context.WithTimeout(context.Background(), apiBodyTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client: GET /api/v1/check/nodes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("client: GET /api/v1/check/nodes status %d: %s", resp.StatusCode, body)
	}

	var out checkNodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("client: decode nodes: %w", err)
	}
	// ECH injection is intentionally disabled here (2026-11-15 A/B probe):
	// same node, same minute — plaintext SNI handshake succeeded (2988ms) while
	// the ech-opts variant died with tls_handshake. Probing is about whether the
	// NODE is alive, not whether the user client's full ECH chain works; the
	// distribution generators still inject ech-opts for clients. Re-enable only
	// after ECH handshakes through these entry IPs are verified stable again.
	for i := range out.Nodes {
		_ = out.Nodes[i]
	}
	return out.Nodes, nil
}

// injectECH adds mihomo ech-opts to TLS proxies so the checker probe path
// exercises the same encrypted SNI the distributed config uses. Since GFW
// started keeping per-domain SNI blocklists (AGENTS.md 2026-11-11), a probe
// without ECH RSTs on domains the user-facing config happily connects to,
// which marks live nodes dead and keeps them out of distribution forever.
// REALITY is excluded: it needs the plaintext SNI to impersonate a real site,
// and mihomo rejects the combination ("Reality is conflict with ECH").
// Nodes may already carry ech-opts from their own raw payload - keep those.
//
// 2026-11-12: only inject when the server domain actually advertises an ech=
// value in its DNS HTTPS (type65) record. mihomo v1.19.33 treats
// ech-opts{enable:true} against a domain without one as a hard dial failure
// ("resolve ECH config error: no ECH config found in DNS records") - it does
// NOT fall back to plaintext. Blind injection marked every healthy non-CF
// node (hidenplay.net etc.) dead with a misleading "dns" fail_reason and
// kept our own self-built exits out of distribution (AGENTS.md 2026-11-12).
// The record lookup goes through the same Ali DoH resolver mihomo uses and
// is negative-cached, so the per-cycle cost is one query per distinct
// non-ECH domain.
// InjectECHForTest exposes the injector to diagnostic commands.
func InjectECHForTest(raw map[string]interface{}) { injectECH(raw) }

// Deprecated: kept for the test hook only; not called from GetNodes.
func injectECH(raw map[string]interface{}) {
	if raw == nil {
		return
	}
	if tls, ok := raw["tls"].(bool); !ok || !tls {
		return
	}
	if _, isReality := raw["reality-opts"]; isReality {
		return
	}
	if _, has := raw["ech-opts"]; has {
		return
	}
	host, _ := raw["servername"].(string)
	if host == "" {
		host, _ = raw["server"].(string)
	}
	if host == "" || net.ParseIP(host) != nil {
		// IP-literal server: no DNS record to consult; mihomo would fail the
		// same way, so keep plaintext TLS (ECH never applies to bare IPs).
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !vendor.HasECHRecord(ctx, host) {
		return
	}
	raw["ech-opts"] = map[string]interface{}{"enable": true}
}

// PostResults uploads all check results to paily-core.
// Non-2xx responses are returned to the caller for retry handling.
func (c *Client) PostResults(results []CheckResult) error {
	payload := checkResultsRequest{Results: results}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("client: marshal results: %w", err)
	}

	endpoint, err := c.endpoint("api", "v1", "check", "results")
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), apiBodyTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: POST /api/v1/check/results: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body2, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("client: POST /api/v1/check/results status %d: %s", resp.StatusCode, body2)
	}
	return nil
}

// DialRaw returns the raw proxy map to dial for probing.
//
// When a node's raw server is an IPv6 literal but the node also carries an
// IPv4 literal in its Server field (the address actually distributed to
// clients), prefer the IPv4 address. This keeps probing aligned with what
// users connect to and stops CF-anycast nodes from being marked dead just
// because their IPv6 entry is unreachable from this line (2026-11-17).
// The original raw map is never mutated.
func (n Node) DialRaw() map[string]interface{} {
	if n.ProbeRaw != nil {
		return n.ProbeRaw
	}
	raw := n.Raw
	if raw == nil {
		return raw
	}
	rs, _ := raw["server"].(string)
	if !isIPv6Literal(rs) {
		return raw
	}
	ip := net.ParseIP(strings.TrimSpace(n.Server))
	if ip == nil || ip.To4() == nil {
		return raw
	}
	cp := make(map[string]interface{}, len(raw)+1)
	for k, v := range raw {
		cp[k] = v
	}
	cp["server"] = n.Server
	return cp
}

func isIPv6Literal(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() == nil && ip.To16() != nil
}
