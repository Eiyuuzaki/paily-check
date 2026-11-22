package ping

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/openpaily/paily-check/internal/interfaces"
)

// StrictHTTPS204 makes exactly one fresh proxied request. The bool reports
// a qualifying proxy dial failure, distinct from target TLS or HTTP failures.
func StrictHTTPS204(ctx context.Context, v interfaces.Vendor, target string, timeout time.Duration) (int, bool, error) {
	return strictHTTPS204(ctx, v, target, timeout, nil)
}

func strictHTTPS204(ctx context.Context, v interfaces.Vendor, target string, timeout time.Duration, roots *x509.CertPool) (int, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, false, err
	}
	if req.URL.Scheme != "https" || req.URL.Hostname() == "" {
		return 0, false, fmt.Errorf("probe target must be HTTPS")
	}
	req.Close = true
	start := time.Now()
	conn, err := v.DialTCP(ctx, target, interfaces.ROptionsTCP)
	if err != nil {
		reason := classifyPingError(err)
		return 0, ctx.Err() == nil && (reason == "conn_reset" || reason == "tls_handshake"), err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return 0, false, err
		}
	}
	targetTLS := tls.Client(conn, &tls.Config{ServerName: req.URL.Hostname(), MinVersion: tls.VersionTLS12, RootCAs: roots})
	if err := targetTLS.HandshakeContext(ctx); err != nil {
		return 0, false, err
	}
	if err := req.Write(targetTLS); err != nil {
		return 0, false, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(io.LimitReader(targetTLS, 32<<10)), req)
	if err != nil {
		return 0, false, err
	}
	if resp.StatusCode != http.StatusNoContent {
		return 0, false, fmt.Errorf("probe returned HTTP %d, want 204", resp.StatusCode)
	}
	return int(time.Since(start).Milliseconds()), false, ctx.Err()
}
