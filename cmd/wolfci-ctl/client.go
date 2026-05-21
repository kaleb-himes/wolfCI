package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// dial returns a gRPC client connection to cfg.ServerAddress
// using wolfSSL mTLS exactly the way agents do. The cert
// chain comes from cfg.Certificate / Key; the server is
// verified against cfg.CACertificate. Insecure gRPC
// credentials are correct here because the underlying
// transport (returned by tlsutil.Dial) is already
// wolfSSL-encrypted.
func dial(ctx context.Context, cfg *Config) (*grpc.ClientConn, error) {
	cert, err := os.ReadFile(cfg.Certificate)
	if err != nil {
		return nil, fmt.Errorf("read certificate %s: %w", cfg.Certificate, err)
	}
	key, err := os.ReadFile(cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", cfg.Key, err)
	}
	ca, err := os.ReadFile(cfg.CACertificate)
	if err != nil {
		return nil, fmt.Errorf("read ca_certificate %s: %w", cfg.CACertificate, err)
	}
	tlsCfg := &tlsutil.Config{
		Certificate: cert,
		Key:         key,
		RootCAs:     ca,
		MinVersion:  tlsutil.VersionTLS13,
	}
	return grpc.DialContext(ctx,
		cfg.ServerAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return tlsutil.Dial("tcp", addr, tlsCfg)
		}),
	)
}

// resolveConfig finds the ctl.yaml the caller wants to use,
// from explicit --config flag or defaultConfigPath().
func resolveConfig(explicit string) (*Config, error) {
	path := explicit
	if path == "" {
		p, err := defaultConfigPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return LoadConfig(path)
}
