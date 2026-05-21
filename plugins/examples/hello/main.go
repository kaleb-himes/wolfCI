// hello is the reference wolfCI plugin. It records every
// OnBuildComplete event as JSON to the path in
// WOLFCI_HELLO_OUT. Used by internal/plugin/plugin_test.go to
// exercise the full host <-> plugin round trip without
// reaching for the network.
package main

import (
	"context"
	"encoding/json"
	"os"

	hcplugin "github.com/hashicorp/go-plugin"

	plugin "github.com/kaleb-himes/wolfCI/internal/plugin"
)

type helloPlugin struct{}

func (h *helloPlugin) OnBuildComplete(ctx context.Context, ev plugin.BuildCompleteEvent) error {
	_ = ctx
	path := os.Getenv("WOLFCI_HELLO_OUT")
	if path == "" {
		// No output requested; silently succeed so production
		// installs of this plugin do not crash if the env var
		// is unset.
		return nil
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func main() {
	hcplugin.Serve(&hcplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.PluginMap(&helloPlugin{}),
		GRPCServer:      hcplugin.DefaultGRPCServer,
	})
}
