// wolfci is the wolfCI server binary. This iteration provides a
// hello-world HTTPS endpoint backed by the internal/tlsutil
// wolfSSL listener. Real handlers (jobs, nodes, UI) land in later
// phases.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// Options groups the runtime parameters for the wolfci hello-world
// server.
type Options struct {
	Addr        string
	Certificate []byte
	Key         []byte
}

// Listen binds an HTTPS-capable net.Listener using opts. Returns
// the wrapped listener; the caller passes it to Serve.
func Listen(opts Options) (net.Listener, error) {
	if opts.Addr == "" {
		return nil, errors.New("wolfci.Listen: Options.Addr is required")
	}
	inner, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("wolfci.Listen: %w", err)
	}
	ln, err := tlsutil.NewListener(inner, &tlsutil.Config{
		Certificate: opts.Certificate,
		Key:         opts.Key,
	})
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("wolfci.Listen: %w", err)
	}
	return ln, nil
}

// Serve runs the hello-world HTTP server on ln until ctx is
// cancelled or ln is closed. Returns nil on graceful shutdown.
func Serve(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "wolfci: hello, world")
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: wolfci <addr> <cert.pem> <key.pem>")
		os.Exit(2)
	}
	addr := os.Args[1]
	cert, err := os.ReadFile(os.Args[2])
	if err != nil {
		log.Fatalf("wolfci: read cert: %v", err)
	}
	key, err := os.ReadFile(os.Args[3])
	if err != nil {
		log.Fatalf("wolfci: read key: %v", err)
	}

	ln, err := Listen(Options{Addr: addr, Certificate: cert, Key: key})
	if err != nil {
		log.Fatalf("wolfci: listen: %v", err)
	}
	defer ln.Close()

	if err := Serve(context.Background(), ln); err != nil {
		log.Fatalf("wolfci: serve: %v", err)
	}
}
