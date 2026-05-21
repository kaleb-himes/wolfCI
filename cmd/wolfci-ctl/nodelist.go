package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
)

// runNodeList implements `wolfci-ctl node list [--json]`.
func runNodeList(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("node list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON instead of a text table")
	configPath := fs.String("config", "", "override ctl.yaml path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := resolveConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl node list: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl node list: dial: %v\n", err)
		return 1
	}
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)
	resp, err := client.ListNodes(ctx, &cliv1.Empty{})
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl node list: ListNodes: %v\n", err)
		return 1
	}
	if _, err := stdout.Write([]byte(formatNodes(resp.Nodes, *asJSON))); err != nil {
		return 1
	}
	return 0
}

// formatNodes is the testable output helper for node list.
func formatNodes(nodes []*cliv1.Node, asJSON bool) string {
	if asJSON {
		type nodeJSON struct {
			AgentID   string   `json:"agent_id"`
			Labels    []string `json:"labels,omitempty"`
			Executors int32    `json:"executors"`
			Connected bool     `json:"connected"`
		}
		out := make([]nodeJSON, 0, len(nodes))
		for _, n := range nodes {
			out = append(out, nodeJSON{
				AgentID:   n.AgentId,
				Labels:    n.Labels,
				Executors: n.Executors,
				Connected: n.Connected,
			})
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		return string(data) + "\n"
	}
	var sb byteWriter
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT_ID\tSTATUS\tLABELS\tEXECUTORS")
	for _, n := range nodes {
		status := "offline"
		if n.Connected {
			status = "connected"
		}
		labels := strings.Join(n.Labels, ",")
		if labels == "" {
			labels = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", n.AgentId, status, labels, n.Executors)
	}
	tw.Flush()
	if len(nodes) == 0 {
		sb.WriteString("(no nodes)\n")
	}
	return sb.String()
}
