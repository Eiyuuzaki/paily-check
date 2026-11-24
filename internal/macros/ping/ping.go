// Package ping implements a single-node HTTP ping via a Vendor proxy.
package ping

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	urllib "net/url"
	"strings"
	"time"

	"github.com/openpaily/paily-check/internal/interfaces"
)

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// PingConfig carries parameters for a single ping run.
type PingConfig struct {
	URL string
	// URLs is an ordered list of probe targets; the first that answers wins.
	// When empty, URL is used. See probeurl.Targets.
	URLs      []string
	Avg       int
	TimeoutMS int
	// SampleGapMS is the pause between consecutive samples. 0 disables it.
	//
	// The gap exists to avoid a thundering herd of probes hitting the line
	// at once, but it is pure dead time: with the concurrency semaphore
	// already capping in-flight probes, sleeping between samples only
	// stretches each node's wall time and (because the sleep happens while
	// the semaphore slot is held in initial.RunAll) starves other nodes.
	// Set 0 on degraded links where probe latency dominates.
	SampleGapMS int
}

// PingResult is the outcome of Ping().
type PingResult struct {
	LatencyMS int // -1 = all attempts timed out
	JitterMS  int // population stddev over N samples (0 for initial pass if not needed)
	// Success/Total expose per-node ping reliability so downstream can
	// separate "avg includes timeout penalties" from "avg of live samples".
	Success int
	Total   int
	// AvgSuccessMS is the mean over successful attempts only (0 when none).
	AvgSuccessMS int
	// FailReason classifies why the node failed (empty when alive). Values:
	// timeout / conn_refused / dns / tls_handshake / dial_failed / conn_reset / other.
	FailReason  string
	ObservedURL string // first successful request, including warm-up and slow retry
}

// Ping performs up to cfg.Avg attempts against cfg.URL through vendor v.
// It calculates latency and jitter.
//
// Average rule (N = cfg.Avg, T = cfg.TimeoutMS):
//
//	if all N attempts fail → LatencyMS = -1
//	otherwise             → sum = Σ(success_rtts) + failed * T
//	                        LatencyMS = sum / N
//
// JitterMS = population stddev across all N sample values (failures → T).
func Ping(ctx context.Context, v interfaces.Vendor, cfg PingConfig) PingResult {
	n := cfg.Avg
	if n <= 0 {
		n = 3
	}
	t := cfg.TimeoutMS
	if t <= 0 {
		t = 3000
	}
	candidates := cfg.URLs
	if len(candidates) == 0 {
		candidates = []string{cfg.URL}
	}

	samples := make([]int, n) // filled with T or actual rtt
	// ok mirrors samples: a failure is recorded as the timeout value, so the
	// sample alone cannot say whether the attempt succeeded. Inferring it from
	// "latency < timeout" breaks on an attempt that answers exactly at the
	// budget, which would be counted as a success in Success and dropped from
	// AvgSuccessMS.
	ok := make([]bool, n)
	okCount := 0
	lastFailReason := ""
	observedURL := ""

	// One keep-alive transport for the whole run. Building a fresh transport per
	// sample made every sample pay a full cold start (proxy dial + TLS-in-TLS),
	// so a CF Worker node measured ~1.4-2.1s while the same node over a reused
	// connection answers in ~340-490ms (2026-11-17, measured on the NAS).
	tr := traceTransport(v)
	defer tr.CloseIdleConnections()

	// One uncounted warm-up request. The first request through a tunnel pays
	// worker spin-up and a full TLS-in-TLS handshake (~1.4s for a CF node)
	// while every later request reuses that connection (~340ms). Counting the
	// cold start both inflates the average and injects ~1s of jitter that has
	// nothing to do with node quality, so pay it once and discard it.
	for _, u := range candidates {
		if _, err := singlePingT(ctx, v, u, time.Duration(t)*time.Millisecond, tr); err == nil {
			observedURL = u
			break
		}
	}

	for i := 0; i < n; i++ {
		httpDelay, target, err := probeAnyURL(ctx, v, candidates, time.Duration(t)*time.Millisecond, tr)
		if err != nil {
			samples[i] = t
			lastFailReason = classifyPingError(err)
		} else {
			samples[i] = int(httpDelay)
			ok[i] = true
			okCount++
			if observedURL == "" {
				observedURL = target
			}
		}
		// Pause between samples (not after the last one). Configurable:
		// 0 disables the gap entirely — see PingConfig.SampleGapMS.
		if cfg.SampleGapMS > 0 && i < n-1 {
			select {
			case <-ctx.Done():
				return PingResult{LatencyMS: -1, JitterMS: -1, FailReason: "aborted", Success: okCount, Total: n}
			case <-time.After(time.Duration(cfg.SampleGapMS) * time.Millisecond):
			}
		}
	}

	if okCount == 0 {
		if lastFailReason == "" {
			lastFailReason = "timeout"
		}
		// Slow-node rescue: a host that is merely slow must not be classified
		// dead. Free hosts like HidenCloud sit at 3-4s on the live path, so a
		// single network hiccup pushes every sample past the normal budget and
		// the node flips alive<->dead forever (2026-11-13: app2301.hidenplay.net
		// answered 200 in 2.5s by hand while the checker recorded timeout).
		// Only timeouts qualify — a refused/reset/dns failure will not become a
		// success just because we wait longer, so skipping the retry keeps dead
		// nodes cheap.
		if lastFailReason == "timeout" {
			if r, target, ok := slowRetryURL(ctx, v, candidates, time.Duration(t*2)*time.Millisecond); ok {
				return PingResult{
					LatencyMS:    int(r),
					JitterMS:     0,
					Success:      1,
					Total:        n,
					AvgSuccessMS: int(r),
					ObservedURL:  target,
				}
			}
		}
		return PingResult{LatencyMS: -1, JitterMS: -1, FailReason: lastFailReason, Success: 0, Total: n, ObservedURL: observedURL}
	}

	// average = (Σ samples) / N
	sum := 0
	for _, s := range samples {
		sum += s
	}
	avg := sum / n

	// jitter = population stddev over all N samples
	variance := 0.0
	favg := float64(sum) / float64(n)
	for _, s := range samples {
		d := float64(s) - favg
		variance += d * d
	}
	variance /= float64(n)
	jitter := int(math.Round(math.Sqrt(variance)))

	// Mean over successful samples only (raw live-path latency, no timeout penalty).
	sumOK := 0
	for i, s := range samples {
		if ok[i] {
			sumOK += s
		}
	}
	avgOK := 0
	if okCount > 0 {
		avgOK = sumOK / okCount
	}
	return PingResult{LatencyMS: avg, JitterMS: jitter, Success: okCount, Total: n, AvgSuccessMS: avgOK, ObservedURL: observedURL}
}

// ---------------------------------------------------------------------------
// Low-level: single attempt
// ---------------------------------------------------------------------------

// classifyPingError maps a ping failure to a coarse reason code so operators
// can tell "died of TLS block" from "died of timeout" from "connection refused".
func classifyPingError(err error) string {
	if err == nil {
		return ""
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "refused"):
		return "conn_refused"
	case strings.Contains(s, "no such host") || strings.Contains(s, "server misbehaving") || strings.Contains(s, "dns"):
		return "dns"
	case strings.Contains(s, "tls") || strings.Contains(s, "certificate") || strings.Contains(s, "handshake"):
		return "tls_handshake"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return "timeout"
	case strings.Contains(s, "reset") || strings.Contains(s, "eof") || strings.Contains(s, "closed"):
		return "conn_reset"
	case strings.Contains(s, "dial"):
		return "dial_failed"
	default:
		return "other"
	}
}

// slowRetry gives an already-failing measurement one more chance with a
// doubled budget. Returns the observed delay when the node answers, and
// ok=false when it still does not (a genuine dead node costs one extra
// attempt, not the whole retry matrix).
// probeAny tries each target in order and returns the first successful RTT.
// A target that fails (timeout / TLS / reset) falls through to the next one, so
// a single blocked target does not mark the node dead.
func probeAny(ctx context.Context, v interfaces.Vendor, urls []string, timeout time.Duration) (int64, error) {
	return probeAnyT(ctx, v, urls, timeout, nil)
}

// probeAnyT is probeAny with a shared keep-alive transport.
func probeAnyT(ctx context.Context, v interfaces.Vendor, urls []string, timeout time.Duration, tr *http.Transport) (int64, error) {
	delay, _, err := probeAnyURL(ctx, v, urls, timeout, tr)
	return delay, err
}

func probeAnyURL(ctx context.Context, v interfaces.Vendor, urls []string, timeout time.Duration, tr *http.Transport) (int64, string, error) {
	var lastErr error
	for _, u := range urls {
		d, err := singlePingT(ctx, v, u, timeout, tr)
		if err == nil {
			return d, u, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("ping: no targets")
	}
	return 0, "", lastErr
}

func slowRetry(ctx context.Context, v interfaces.Vendor, urls []string, timeout time.Duration) (int64, bool) {
	delay, _, ok := slowRetryURL(ctx, v, urls, timeout)
	return delay, ok
}

func slowRetryURL(ctx context.Context, v interfaces.Vendor, urls []string, timeout time.Duration) (int64, string, bool) {
	if ctx.Err() != nil {
		return 0, "", false
	}
	delay, target, err := probeAnyURL(ctx, v, urls, timeout, nil)
	return delay, target, err == nil
}

func singlePing(ctx context.Context, v interfaces.Vendor, rawURL string, timeout time.Duration) (httpDelay int64, err error) {
	return singlePingT(ctx, v, rawURL, timeout, nil)
}

func singlePingT(ctx context.Context, v interfaces.Vendor, rawURL string, timeout time.Duration, tr *http.Transport) (httpDelay int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if strings.HasPrefix(rawURL, "https:") {
		_, http, _, e := pingViaTraceT(ctx, v, rawURL, tr)
		return int64(http), e
	}
	_, http, _, e := pingViaNetCat(ctx, v, rawURL)
	return int64(http), e
}

// traceTransport dials every request through v and keeps connections alive so
// subsequent samples measure steady state rather than a fresh handshake.
func traceTransport(v interfaces.Vendor) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return v.DialTCP(ctx, "https://"+addr, interfaces.ROptionsTCP)
		},
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// ---------------------------------------------------------------------------
// Ping via HTTPS trace (TLS round-trip measurement)
// ---------------------------------------------------------------------------

func pingViaTrace(ctx context.Context, p interfaces.Vendor, url string) (rtt, req uint16, statusCode int, err error) {
	return pingViaTraceT(ctx, p, url, nil)
}

func pingViaTraceT(ctx context.Context, p interfaces.Vendor, url string, tr *http.Transport) (rtt, req uint16, statusCode int, err error) {
	transport := tr
	if transport == nil {
		transport = traceTransport(p)
	}

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, 0, 0, err
	}

	var tlsEnd, writeStart, writeEnd int64
	trace := &httptrace.ClientTrace{
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { tlsEnd = time.Now().UnixMilli() },
		GotFirstResponseByte: func() { writeEnd = time.Now().UnixMilli() },
		WroteHeaders:         func() { writeStart = time.Now().UnixMilli() },
	}
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(ctx, trace))
	_ = writeStart // kept for reference; unused in return calculation

	start := time.Now().UnixMilli()
	resp, err := transport.RoundTrip(httpReq)
	if err != nil {
		return 0, 0, 0, err
	}
	defer resp.Body.Close()
	connEnd := time.Now().UnixMilli()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, 0, resp.StatusCode, fmt.Errorf("unexpected probe HTTP status %d", resp.StatusCode)
	}
	// Small probe bodies must reach EOF for HTTP/1.1 connection reuse.
	// Bound the read for custom targets and keep it outside the RTT measurement.
	const maxDrainBytes = 16 << 10
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes)); err != nil {
		return 0, 0, resp.StatusCode, fmt.Errorf("read probe response: %w", err)
	}

	if !strings.HasPrefix(url, "https:") {
		return uint16(connEnd - start), uint16(connEnd - start), resp.StatusCode, nil
	}
	if resp.TLS != nil && resp.TLS.HandshakeComplete {
		return uint16(writeEnd - tlsEnd), uint16(writeEnd - start), resp.StatusCode, nil
	}
	return 0, 0, 0, fmt.Errorf("TLS handshake did not complete")
}

// ---------------------------------------------------------------------------
// Ping via plain HTTP netcat (TCP round-trip measurement)
// ---------------------------------------------------------------------------

const netcatPayload = "GET %s HTTP/1.1\r\nAccept: */*\r\nAccept-Encoding: gzip, deflate\r\nHost: %s\r\nUser-Agent: HTTPie/3.0.2 PailyCheck/1.0\r\n\r\n"

type timeoutReader struct {
	r       *bufio.Reader
	timeout time.Time
}

func (tr *timeoutReader) Read(p []byte) (n int, err error) {
	if time.Now().After(tr.timeout) {
		return 0, errors.New("read timeout")
	}
	return tr.r.Read(p)
}

func saferParseHTTPStatus(reader *bufio.Reader) (int, error) {
	tr := &timeoutReader{reader, time.Now().Add(5 * time.Second)}
	lr := io.LimitReader(tr, 1024*1024)
	resp, err := http.ReadResponse(bufio.NewReader(lr), nil)
	if err != nil {
		return 0, err
	}
	return resp.StatusCode, nil
}

func pingViaNetCat(ctx context.Context, p interfaces.Vendor, rawURL string) (rtt, req uint16, statusCode int, err error) {
	purl, parseErr := urllib.Parse(rawURL)
	if parseErr != nil {
		return 0, 0, 0, parseErr
	}
	path := purl.EscapedPath()
	if purl.RawQuery != "" {
		path += "?" + purl.Query().Encode()
	}
	if path == "" {
		path = "/"
	}
	payload := fmt.Sprintf(netcatPayload, path, purl.Hostname())

	connStart := time.Now()
	conn, err := p.DialTCP(ctx, rawURL, interfaces.ROptionsTCP)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
	reader := bufio.NewReader(conn)

	tcpStart := time.Now()
	if _, err := conn.Write([]byte(payload)); err != nil {
		return 0, 0, 0, fmt.Errorf("write 1: %w", err)
	}
	if _, err := reader.Peek(1); err != nil {
		return 0, 0, 0, fmt.Errorf("peek 1: %w", err)
	}

	tcpRTT := time.Since(tcpStart).Milliseconds()
	connRTT := time.Since(connStart).Milliseconds()
	statusCode, _ = saferParseHTTPStatus(reader)
	for reader.Buffered() > 0 {
		_, _, _ = reader.ReadLine()
	}

	// second request for a cleaner RTT sample
	tcpStart = time.Now()
	if _, err := conn.Write([]byte(payload)); err != nil {
		return uint16(tcpRTT), uint16(connRTT), statusCode, nil
	}
	if _, err := reader.Peek(1); err != nil {
		if err == io.EOF {
			return uint16(tcpRTT), uint16(connRTT), statusCode, nil
		}
		return uint16(tcpRTT), uint16(connRTT), statusCode, nil
	}
	tcpRTT = time.Since(tcpStart).Milliseconds()
	statusCode2, err2 := saferParseHTTPStatus(reader)
	if err2 == nil {
		statusCode = statusCode2
	}

	return uint16(tcpRTT), uint16(connRTT), statusCode, nil
}
