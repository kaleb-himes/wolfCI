package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
)

// runJobList implements `wolfci-ctl job list [--json]`.
//
// Note: we model multi-word subcommands as space-separated args
// rather than nested dispatchers. The router treats the first
// arg as the top-level subcommand ("job"), then this function
// re-routes on args[0] ("list", etc.).
func runJobList(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("job list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON instead of a text table")
	configPath := fs.String("config", "", "override ctl.yaml path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := resolveConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl job list: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dial(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl job list: dial: %v\n", err)
		return 1
	}
	defer conn.Close()

	client := cliv1.NewCLIServiceClient(conn)
	resp, err := client.ListJobs(ctx, &cliv1.Empty{})
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl job list: ListJobs: %v\n", err)
		return 1
	}

	out := formatJobs(resp.Jobs, *asJSON)
	if _, err := stdout.Write([]byte(out)); err != nil {
		return 1
	}
	return 0
}

// formatJobs is the testable output helper for job list.
func formatJobs(jobs []*cliv1.Job, asJSON bool) string {
	if asJSON {
		// Project onto a plain shape so the JSON is stable
		// regardless of proto-internal fields.
		type jobJSON struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			NodeLabel   string `json:"node_label,omitempty"`
			StepCount   int32  `json:"step_count"`
		}
		out := make([]jobJSON, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, jobJSON{
				Name:        j.Name,
				Description: j.Description,
				NodeLabel:   j.NodeLabel,
				StepCount:   j.StepCount,
			})
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		return string(data) + "\n"
	}
	var sb byteWriter
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDESCRIPTION\tNODE LABEL\tSTEPS")
	for _, j := range jobs {
		label := j.NodeLabel
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", j.Name, j.Description, label, j.StepCount)
	}
	tw.Flush()
	if len(jobs) == 0 {
		sb.WriteString("(no jobs)\n")
	}
	return sb.String()
}

// byteWriter is a tiny io.Writer over a string builder.
// tabwriter wants io.Writer; strings.Builder satisfies it but
// we want easy String() access too.
type byteWriter struct{ buf []byte }

func (b *byteWriter) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}
func (b *byteWriter) WriteString(s string) { b.buf = append(b.buf, s...) }
func (b *byteWriter) String() string       { return string(b.buf) }
