package main

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/testcerts"
)

// TestServe_HelloWorld is the gating test for PLAN.md task 1.5. It
// generates a self-signed cert, runs Listen + Serve on an
// ephemeral port, and asserts that an HTTPS client receives 200
// OK with a body containing "hello, world".
func TestServe_HelloWorld(t *testing.T) {
	cert, key := testcerts.SelfSignedECDSA(t)

	ln, err := Listen(Options{
		Addr:        "127.0.0.1:0",
		Certificate: cert,
		Key:         key,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() { serverErr <- Serve(ctx, ln) }()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
				MaxVersion:         tls.VersionTLS13,
			},
		},
	}

	url := "https://" + ln.Addr().String() + "/"
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("client.Get(%s): %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "hello, world") {
		t.Fatalf("body = %q, want to contain %q", body, "hello, world")
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("Serve returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not return after context cancel")
	}
}
