package router

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunWithReadyUsesBoundPortAndClosesListener(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunWithReady(ctx, []string{"--mode", "passthrough", "--listen", "127.0.0.1:0"}, io.Discard, func(url string) {
			ready <- url
		})
	}()
	var baseURL string
	select {
	case baseURL = <-ready:
	case err := <-done:
		t.Fatalf("router stopped before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("router did not become ready")
	}
	address := strings.TrimSuffix(strings.TrimPrefix(baseURL, "http://"), "/v1")
	if _, port, err := net.SplitHostPort(address); err != nil || port == "0" {
		t.Fatalf("ready URL = %q", baseURL)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(strings.TrimSuffix(baseURL, "/v1") + "/api/metrics")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d", response.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("listener survived router exit")
	}
}

func TestRunWithReadyDoesNotNotifyOnStartupFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	err = RunWithReady(t.Context(), []string{"--mode", "passthrough", "--listen", listener.Addr().String()}, io.Discard, func(string) {
		t.Error("ready called despite failed bind")
	})
	if err == nil {
		t.Fatal("expected bind failure")
	}
}
