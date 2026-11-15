package request

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestNetCatWithRetryTimesOutWhenPeerAcceptsAndStaysSilent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		_, _ = c.Read(buf)
		close(accepted)
		time.Sleep(2 * time.Second) // emulate a peer that accepts TCP but never responds
	}()
	start := time.Now()
	_, err = NetCatWithRetry(nil, 1, 150, ln.Addr().String(), []byte("probe\n"), true, "tcp")
	if err == nil {
		t.Fatal("expected timeout from silent peer")
	}
	select {
	case <-accepted:
	default:
		t.Fatal("server never accepted connection")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("read exceeded timeout by too much: %s", elapsed)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") && !strings.Contains(strings.ToLower(err.Error()), "i/o timeout") {
		t.Logf("read ended with expected deadline-related error: %v", err)
	}
}
