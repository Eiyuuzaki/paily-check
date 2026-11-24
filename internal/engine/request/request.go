// Package request provides HTTP client utilities that route traffic through a
// Vendor proxy.
package request

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/openpaily/paily-check/internal/interfaces"
)

// maxResponseBody bounds how much of a proxied response is held in memory.
//
// Without a bound the only limit was the deadline, so a target that keeps
// sending could stream gigabytes into RAM within one timeout window and get the
// process OOM-killed - the checker runs on a NAS with a fixed memory budget.
const maxResponseBody = 16 << 20

// readAllLimited reads at most maxResponseBody and treats overflow as an error
// rather than returning a silently truncated body.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response body exceeds %d bytes", limit)
	}
	return b, nil
}

// defaultAttemptTimeoutMS is used when a caller supplies a non-positive
// timeout. Scripts reach these functions with a value straight from JS, so a
// typo like `timeout: 0` would otherwise create an already-expired context and
// make every request fail instantly with "context deadline exceeded" - a
// message that points at the network instead of at the option.
const defaultAttemptTimeoutMS = 3000

// clampTimeoutMS turns a caller-supplied timeout into a usable duration.
func clampTimeoutMS(ms int64) time.Duration {
	if ms <= 0 {
		ms = defaultAttemptTimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}

// clampRetry ensures 1 ≤ retry ≤ 10.
func clampRetry(n int) int {
	if n < 1 {
		return 1
	}
	if n > 10 {
		return 10
	}
	return n
}

// Unsafe performs a single HTTP request, routing through p when non-nil.
// The caller is responsible for closing resp.Body.
func Unsafe(ctx context.Context, p interfaces.Vendor, opt *interfaces.RequestOptions) (*http.Response, []string, error) {
	if p != nil && p.Status() == interfaces.VStatusNotReady {
		return nil, nil, errors.New("proxy is not ready")
	}
	if opt == nil {
		return nil, nil, errors.New("nil RequestOptions")
	}

	if opt.Method == "" {
		opt.Method = http.MethodGet
	}

	var bodyReader io.Reader
	if len(opt.Body) > 0 {
		bodyReader = bytes.NewBuffer(opt.Body)
	}

	req, err := http.NewRequestWithContext(ctx, opt.Method, opt.URL, bodyReader)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range opt.Cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       10 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	target := opt.URL
	if opt.DialURL != "" {
		target = opt.DialURL
	}
	if p != nil {
		transport.DialContext = func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			return p.DialTCP(dialCtx, target, opt.Network)
		}
	}

	redirects := []string{}
	noRedir := opt.NoRedir
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if noRedir || len(redirects) >= 64 {
				return http.ErrUseLastResponse
			}
			if r.Response != nil {
				redirects = append(redirects, r.Response.Header.Get("Location"))
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	return resp, redirects, nil
}

// WithRetry performs up to retry attempts, returning the last successful body,
// response, and redirect list. timeoutMS applies per attempt.
func WithRetry(p interfaces.Vendor, retry int, timeoutMS int64, opt *interfaces.RequestOptions) ([]byte, *http.Response, []string) {
	var (
		resp      *http.Response
		body      []byte
		redirects []string
	)
	for i := 0; i < clampRetry(retry) && resp == nil; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), clampTimeoutMS(timeoutMS))
		r, redir, err := Unsafe(ctx, p, opt)
		if err != nil {
			cancel()
			continue
		}
		// Read body BEFORE cancelling the context; cancelling early closes the
		// underlying connection and makes io.ReadAll return an empty body.
		b, rerr := readAllLimited(r.Body, maxResponseBody)
		r.Body.Close()
		cancel()
		if rerr != nil {
			continue
		}
		body = b
		resp = r
		redirects = redir
	}
	return body, resp, redirects
}

// NetCat sends data to addr over the given network through p and returns the
// raw response bytes.
//
// firstChunk returns after one Read instead of reading to EOF. It does NOT
// return a complete line: a single Read yields whatever has arrived, which may
// be half a line or several. Callers that need a full line must parse the
// result themselves.
func NetCat(ctx context.Context, p interfaces.Vendor, addr string, data []byte, firstChunk bool, network interfaces.RequestOptionsNetwork) ([]byte, error) {
	var (
		conn net.Conn
		err  error
	)
	if p != nil {
		if p.Status() != interfaces.VStatusOperational {
			return nil, errors.New("proxy is not ready")
		}
		conn, err = p.DialTCP(ctx, addr, network)
	} else {
		conn, err = net.Dial(string(network), addr)
	}
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// Context cancellation alone does not interrupt an established net.Conn.
	// Apply it to the socket so silent peers cannot pin a Checker goroutine.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if _, err := conn.Write(data); err != nil {
		return nil, err
	}

	if firstChunk {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil && err != io.EOF {
			return nil, err
		}
		return buf[:n], nil
	}

	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(conn, maxResponseBody+1))
	if err != nil {
		return nil, err
	}
	if n > maxResponseBody {
		return nil, fmt.Errorf("socket response exceeds %d bytes", maxResponseBody)
	}
	return buf.Bytes(), nil
}

// NetCatWithRetry retries NetCat up to retry times.
func NetCatWithRetry(p interfaces.Vendor, retry int, timeoutMS int64, addr string, data []byte, firstChunk bool, network interfaces.RequestOptionsNetwork) ([]byte, error) {
	var (
		out []byte
		err error
	)
	for i := 0; i < clampRetry(retry); i++ {
		ctx, cancel := context.WithTimeout(context.Background(), clampTimeoutMS(timeoutMS))
		out, err = NetCat(ctx, p, addr, data, firstChunk, network)
		cancel()
		if err == nil {
			return out, nil
		}
	}
	return out, err
}
