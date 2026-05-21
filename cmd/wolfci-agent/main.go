// wolfci-agent is the wolfCI executor that runs on a node.
//
// On startup it loads its config, dials the wolfCI server via
// wolfSSL mTLS, registers, opens the Connect stream, and runs
// every JobAssignment the server pushes through a local shell
// executor. Use --dry-run to load + print the resolved config
// without dialing.
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

	client, err := agent.NewClient(cfg)
	if err != nil {
		log.Fatalf("wolfci-agent: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("wolfci-agent: starting agent_id=%q server=%q", cfg.AgentID, cfg.ServerAddress)
	if err := client.Run(ctx); err != nil {
		log.Fatalf("wolfci-agent: %v", err)
	}
	log.Printf("wolfci-agent: shutting down cleanly")
}
