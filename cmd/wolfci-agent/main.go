// wolfci-agent is the wolfCI executor that runs on a node.
//
// On startup it loads its config from a YAML file and validates
// it. The full agent protocol (gRPC over wolfSSL mTLS) lands
// with PLAN.md task 5.3; this binary today loads the config
// and prints what it would do. Run with --dry-run to inspect
// the resolved config without attempting any network operation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/kaleb-himes/wolfCI/internal/agent"
)

func main() {
	configPath := flag.String("config", "config-files/agent.yaml", "path to agent.yaml")
	dryRun := flag.Bool("dry-run", false, "load and print the resolved config; do not dial")
	flag.Parse()

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("wolfci-agent: %v", err)
	}

	if *dryRun {
		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			log.Fatalf("wolfci-agent: marshal config: %v", err)
		}
		fmt.Println(string(out))
		return
	}

	fmt.Fprintln(os.Stderr, "wolfci-agent: mTLS dial and agent protocol not yet implemented (PLAN.md task 5.3)")
	fmt.Fprintln(os.Stderr, "wolfci-agent: re-run with --dry-run to inspect the loaded config")
	os.Exit(2)
}
