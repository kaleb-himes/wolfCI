package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
)

// runBuildLog implements `wolfci-ctl build log <job> <n>`.
// Streams the build log over CLIService.StreamBuildLog until
// the server closes (idle timeout) or the user hits Ctrl-C.
func runBuildLog(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("build log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "override ctl.yaml path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "usage: wolfci-ctl build log <job> <n>")
		return 2
	}
	jobName := rest[0]
	num, err := strconv.Atoi(rest[1])
	if err != nil || num < 1 {
		fmt.Fprintf(stderr, "wolfci-ctl build log: invalid build number %q\n", rest[1])
		return 2
	}

	cfg, err := resolveConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl build log: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := dial(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl build log: dial: %v\n", err)
		return 1
	}
	defer conn.Close()

	client := cliv1.NewCLIServiceClient(conn)
	stream, err := client.StreamBuildLog(ctx, &cliv1.BuildLogRequest{
		JobName:     jobName,
		BuildNumber: int32(num),
	})
	if err != nil {
		fmt.Fprintf(stderr, "wolfci-ctl build log: StreamBuildLog: %v\n", err)
		return 1
	}
	for {
		line, err := stream.Recv()
		if err == io.EOF {
			return 0
		}
		if err != nil {
			fmt.Fprintf(stderr, "wolfci-ctl build log: Recv: %v\n", err)
			return 1
		}
		if _, err := stdout.Write(line.Data); err != nil {
			return 1
		}
	}
}
