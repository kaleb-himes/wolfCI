package main

import (
	"flag"
	"fmt"
	"os"
)

// runLogin writes a ctl.yaml capturing the server endpoint and
// credential paths. mTLS is the actual authentication; "login"
// is just configuration so subsequent subcommands have something
// to read.
func runLogin(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", "", "wolfCI server address, host:port")
	cert := fs.String("cert", "", "path to client cert PEM")
	key := fs.String("key", "", "path to client key PEM")
	caCert := fs.String("ca-cert", "", "path to server CA bundle PEM")
	configPath := fs.String("config", "", "override path to ctl.yaml (default: WOLFCI_CTL_CONFIG or XDG_CONFIG_HOME/wolfci/ctl.yaml)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	missing := []string{}
	if *server == "" {
		missing = append(missing, "--server")
	}
	if *cert == "" {
		missing = append(missing, "--cert")
	}
	if *key == "" {
		missing = append(missing, "--key")
	}
	if *caCert == "" {
		missing = append(missing, "--ca-cert")
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "wolfci-ctl login: missing required flag(s): %v\n", missing)
		fs.SetOutput(stderr)
		fs.Usage()
		return 2
	}

	cfg := &Config{
		ServerAddress: *server,
		Certificate:   *cert,
		Key:           *key,
		CACertificate: *caCert,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl login: %v\n", err)
		return 2
	}

	path := *configPath
	if path == "" {
		p, err := defaultConfigPath()
		if err != nil {
			fmt.Fprintf(stderr, "wolfci-ctl login: %v\n", err)
			return 1
		}
		path = p
	}
	if err := cfg.Save(path); err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl login: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wolfci-ctl: config written to %s\n", path)
	return 0
}
