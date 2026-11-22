package ping

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSPingReusesSmallResponseConnection(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var connections atomic.Int32
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = io.WriteString(w, "success\n")
				}
			}))
			s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			s.StartTLS()
			defer s.Close()
			dialer, tr := httpsPingFixture(s)
			defer tr.CloseIdleConnections()
			for i := 0; i < 6; i++ { // one warm-up and five timed requests
				if _, err := singlePingT(context.Background(), dialer, s.URL, time.Second, tr); err != nil {
					t.Fatal(err)
				}
			}
			if got := connections.Load(); got != 1 {
				t.Fatalf("six probes opened %d TCP connections, want 1", got)
			}
		})
	}
}

func TestHTTPSPingFallsBackAfterErrorStatus(t *testing.T) {
	bad := map[string]int{
		"/bad-gateway": http.StatusBadGateway,
		"/redirect":    http.StatusFound,
		"/missing":     http.StatusNotFound,
		"/error":       http.StatusInternalServerError,
	}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code, ok := bad[r.URL.Path]; ok {
			if code == http.StatusFound {
				w.Header().Set("Location", "/ok")
			}
			w.WriteHeader(code)
			return
		}
		if r.URL.Path == "/ok" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Errorf("unexpected probe path %q", r.URL.Path)
	}))
	defer s.Close()
	dialer, tr := httpsPingFixture(s)
	defer tr.CloseIdleConnections()
	want := s.URL + "/ok"
	for path := range bad {
		_, got, err := probeAnyURL(context.Background(), dialer, []string{s.URL + path, want}, time.Second, tr)
		if err != nil || got != want {
			t.Errorf("target %s: probe target = %q, err = %v, want fallback to %q", path, got, err, want)
		}
	}
}

func httpsPingFixture(s *httptest.Server) (strictTestDialer, *http.Transport) {
	dialer := strictTestDialer{addr: s.Listener.Addr().String()}
	tr := traceTransport(dialer)
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	tr.TLSClientConfig.RootCAs = roots
	return dialer, tr
}
