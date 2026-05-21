package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
)

// runJobRun implements `wolfci-ctl job run [--json] <name>`.
// It enqueues a build via CLIService.RunJob and prints the
// assigned build number.
func runJobRun(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("job run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON instead of human text")
	configPath := fs.String("config", "", "override ctl.yaml path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "usage: wolfci-ctl job run [--json] <name>")
		return 2
	}
	name := rest[0]

	cfg, err := resolveConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl job run: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl job run: dial: %v\n", err)
		return 1
	}
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)
	resp, err := client.RunJob(ctx, &cliv1.RunJobRequest{JobName: name})
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl job run: RunJob: %v\n", err)
		return 1
	}

	if *asJSON {
		data, _ := json.Marshal(map[string]interface{}{
			"job":          name,
			"build_number": resp.BuildNumber,
		})
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprintf(stdout, "%s queued as build %d\n", name, resp.BuildNumber)
		fmt.Fprintf(stdout, "tail with: wolfci-ctl build log %s %d\n", name, resp.BuildNumber)
	}
	return 0
}
