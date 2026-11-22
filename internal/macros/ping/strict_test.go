package ping

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openpaily/paily-check/internal/interfaces"
)

type strictTestDialer struct {
	interfaces.Vendor
	addr string
	err  error
}

func (d strictTestDialer) DialTCP(ctx context.Context, _ string, _ interfaces.RequestOptionsNetwork) (net.Conn, error) {
	if d.err != nil {
		return nil, d.err
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.addr)
}

func TestStrictHTTPS204(t *testing.T) {
	for _, code := range []int{204, 200, 302, 502} {
		var requestCount atomic.Int32
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			if r.URL.Path != "/original" || r.Method != http.MethodGet {
				t.Errorf("unexpected request %s %s", r.Method, r.URL)
			}
			w.Header().Set("Location", "/redirect")
			w.WriteHeader(code)
		}))
		roots := x509.NewCertPool()
		roots.AddCert(s.Certificate())
		dialer := strictTestDialer{addr: s.Listener.Addr().String()}
		_, qualifying, err := strictHTTPS204(context.Background(), dialer, s.URL+"/original", time.Second, roots)
		s.Close()
		if qualifying || (err == nil) != (code == 204) || requestCount.Load() != 1 {
			t.Fatalf("status %d: qualifying=%v, err=%v, requests=%d", code, qualifying, err, requestCount.Load())
		}
	}
}

func TestStrictHTTPS204SeparatesDialAndTargetTLS(t *testing.T) {
	dialer := strictTestDialer{err: errors.New("fixture: connection reset")}
	_, qualifying, err := StrictHTTPS204(context.Background(), dialer, "https://probe.example.com", time.Second)
	if !qualifying || err == nil {
		t.Fatalf("dial result: qualifying=%v err=%v", qualifying, err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	dialer = strictTestDialer{addr: s.Listener.Addr().String()}
	_, qualifying, err = StrictHTTPS204(context.Background(), dialer, "https"+strings.TrimPrefix(s.URL, "http"), time.Second)
	if qualifying || err == nil {
		t.Fatalf("target TLS result: qualifying=%v err=%v", qualifying, err)
	}
}
