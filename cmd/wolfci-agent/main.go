// wolfci-agent is the wolfCI executor that runs on a node.
//
// On startup it loads its config, dials the wolfCI server via
// wolfSSL mTLS, registers, opens the Connect stream, and runs
// every JobAssignment the server pushes through a local shell
// executor. Use --dry-run to load + print the resolved config
// without dialing. Use --version to print the build stamp and
// exit.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kaleb-himes/wolfCI/internal/agent"
)

// version is overridden at build time via -ldflags -X
// main.version=<value>. scripts/build.sh defaults the value to
// "git describe --tags --always --dirty" so a development build
// still embeds the commit it was cut from (PLAN.md 12.8). The
// agent forwards this on every NodeStatus.agent_version so the
// /nodes view's "Agent version" column reflects what is
// actually deployed.
var version = "dev"

func main() {
	configPath := flag.String("config", "config-files/agent.yaml", "path to agent.yaml")
	dryRun := flag.Bool("dry-run", false, "load and print the resolved config; do not dial")
	showVersion := flag.Bool("version", false, "print the wolfci-agent version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("wolfci-agent " + version)
		return
	}

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

	client, err := agent.NewClient(cfg)
	if err != nil {
		log.Fatalf("wolfci-agent: %v", err)
	}
	client.SetVersion(version)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("wolfci-agent: starting agent_id=%q server=%q version=%q",
		cfg.AgentID, cfg.ServerAddress, version)
	if err := client.Run(ctx); err != nil {
		log.Fatalf("wolfci-agent: %v", err)
	}
	log.Printf("wolfci-agent: shutting down cleanly")
}
