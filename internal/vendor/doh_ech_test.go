package vendor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func echConfigListFixture() []byte {
	body := []byte{1, 0x00, 0x20, 0, 32} // ID, X25519, public key length
	body = append(body, bytes.Repeat([]byte{0x42}, 32)...)
	body = append(body, 0, 4, 0, 1, 0, 1, 0) // cipher suites vector, name limit
	publicName := "public.example.com"
	body = append(body, byte(len(publicName)))
	body = append(body, publicName...)
	body = append(body, 0, 0) // no extensions
	list := binary.BigEndian.AppendUint16(nil, uint16(4+len(body)))
	list = append(list, 0xfe, 0x0d)
	list = binary.BigEndian.AppendUint16(list, uint16(len(body)))
	return append(list, body...)
}

func TestResolveECHCachesDefinitiveResults(t *testing.T) {
	config := echConfigListFixture()
	value := base64.StdEncoding.EncodeToString(config)
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := requests.Add(1)
		if r.URL.Query().Get("type") != "HTTPS" {
			t.Error("wrong record type")
		}
		if i == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		var answers []map[string]any
		if r.URL.Query().Get("name") == "has.example.com" {
			time.Sleep(10 * time.Millisecond) // body may arrive after headers
			answers = append(answers, map[string]any{"type": 65, "data": "1 . ech=" + value})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Status": 0, "Answer": answers})
	}))
	defer s.Close()
	d := newDoHResolver()
	d.endpoint = s.URL
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := d.ResolveECH(ctx, "has.example.com"); err == nil || errors.Is(err, errNoECHConfig) {
		t.Fatalf("temporary HTTP error must be distinct: %v", err)
	}
	for _, host := range []string{"HAS.Example.Com.", "has.example.com"} {
		got, err := d.ResolveECH(ctx, host)
		if err != nil || !bytes.Equal(got, config) {
			t.Fatalf("config = %x, err = %v", got, err)
		}
	}
	for range 2 {
		if got, err := d.ResolveECH(ctx, "missing.example.com"); len(got) != 0 || !errors.Is(err, errNoECHConfig) {
			t.Fatalf("negative cache config = %x, err = %v", got, err)
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("unexpected request count %d", requests.Load())
	}
}

func TestResolveECHCancelLeavesDifferentHostAvailable(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "slow.example.com" {
			close(started)
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Status": 0})
	}))
	defer s.Close()
	d := newDoHResolver()
	d.endpoint = s.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := d.ResolveECH(ctx, "slow.example.com")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow request did not start")
	}
	fastCtx, fastCancel := context.WithTimeout(context.Background(), time.Second)
	defer fastCancel()
	if _, err := d.ResolveECH(fastCtx, "fast.example.com"); !errors.Is(err, errNoECHConfig) {
		t.Fatalf("unrelated host serialized behind slow request: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request err=%v", err)
	}
}
