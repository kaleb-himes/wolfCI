// wolfci-ctl is the wolfCI command-line client. It speaks the
// same wolfSSL mTLS gRPC transport agents use; the server takes
// the client cert's CN as the username and gates each action
// against config-files/auth/matrix.yaml.
//
// Subcommands (Phase 8.1a ships login; the rest land in
// follow-on iterations):
//
//	wolfci-ctl login   --server ... --cert ... --key ... --ca-cert ...
//	wolfci-ctl version
//
// Flags that apply to every subcommand:
//
//	--json              emit machine-readable JSON instead of human text
//	--config <path>     override the config path (default
//	                    ~/.config/wolfci/ctl.yaml, env
//	                    WOLFCI_CTL_CONFIG, then XDG_CONFIG_HOME)
package main

import (
	"fmt"
	"os"
	"sort"
)

// version is set at build time via -ldflags. Defaults to "dev".
var version = "dev"

// subcommand is a single top-level command.
type subcommand struct {
	name     string
	synopsis string
	run      func(args []string, stdout, stderr *os.File) int
}

var subcommands = map[string]*subcommand{
	"login": {
		name:     "login",
		synopsis: "write a ctl.yaml capturing server address and credentials",
		run:      runLogin,
	},
	"version": {
		name:     "version",
		synopsis: "print the wolfci-ctl version",
		run:      runVersion,
	},
}

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

// dispatch is the testable subcommand router. Returns the exit
// code; never calls os.Exit itself.
func dispatch(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printUsage(stdout)
		return 0
	}
	sub, ok := subcommands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "wolfci-ctl: unknown subcommand %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
	return sub.run(args[1:], stdout, stderr)
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "usage: wolfci-ctl <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	names := make([]string, 0, len(subcommands))
	for n := range subcommands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := subcommands[n]
		fmt.Fprintf(w, "  %-10s %s\n", s.name, s.synopsis)
	}
}

func runVersion(args []string, stdout, stderr *os.File) int {
	_ = args
	_ = stderr
	fmt.Fprintln(stdout, "wolfci-ctl "+version)
	return 0
}
