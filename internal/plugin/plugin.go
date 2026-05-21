// Package plugin is the wolfCI plugin host built on
// hashicorp/go-plugin.
//
// The Host loads every binary under
// <pluginsDir>/installed/<name>/<name> as a long-running
// subprocess connected over a gRPC-on-Unix-socket transport,
// and dispatches lifecycle hooks (OnBuildComplete in Phase 7)
// to each.
//
// Plugin authors import this package to declare their
// implementation of the WolfCIPlugin interface, then hand it
// to hashicorp/go-plugin.Serve with the exported Handshake and
// PluginMap. See docs/PLUGINS.md and plugins/examples/hello
// for a reference.
package plugin

import (
	"context"

	hcplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pluginv1 "github.com/kaleb-himes/wolfCI/api/v1/plugin"
)

// PluginName is the only key in the plugin map. The wolfCI
// host dispenses this name; plugin authors register it under
// the same key. Public so external authors can reference it.
const PluginName = "wolfci"

// Handshake is the cookie wolfCI uses to confirm a plugin
// binary was launched by a wolfCI host (and not someone
// running it directly from a shell). Both ends must use the
// same values.
var Handshake = hcplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "WOLFCI_PLUGIN_COOKIE",
	MagicCookieValue: "wolfci-v1",
}

// WolfCIPlugin is what every wolfCI plugin implements.
type WolfCIPlugin interface {
	// OnBuildComplete fires once per finished build. Plugins
	// should return quickly; long-running work should happen in
	// the plugin's own goroutine.
	OnBuildComplete(ctx context.Context, event BuildCompleteEvent) error
}

// BuildCompleteEvent is the Go-side mirror of the same-named
// proto message. Plugin authors get this struct; gRPC wire is
// hidden.
type BuildCompleteEvent struct {
	JobName     string
	BuildNumber int32
	// Status is one of "success", "failure", "cancelled",
	// "error" matching scheduler.Status.
	Status   string
	ExitCode int32
	Error    string
}

// GRPCPlugin is the hashicorp/go-plugin glue. The host uses
// it via PluginMap(nil); plugins use it via PluginMap(impl).
type GRPCPlugin struct {
	hcplugin.Plugin
	Impl WolfCIPlugin
}

// GRPCServer registers the plugin's implementation on s.
func (p *GRPCPlugin) GRPCServer(broker *hcplugin.GRPCBroker, s *grpc.Server) error {
	pluginv1.RegisterWolfCIPluginServer(s, &grpcServer{Impl: p.Impl})
	return nil
}

// GRPCClient hands the host a client that satisfies WolfCIPlugin.
func (p *GRPCPlugin) GRPCClient(ctx context.Context, broker *hcplugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &grpcClient{client: pluginv1.NewWolfCIPluginClient(c)}, nil
}

// PluginMap returns the go-plugin map for ServeConfig and
// ClientConfig. Pass the implementation on the plugin side;
// pass nil on the host side.
func PluginMap(impl WolfCIPlugin) map[string]hcplugin.Plugin {
	return map[string]hcplugin.Plugin{
		PluginName: &GRPCPlugin{Impl: impl},
	}
}

// grpcServer is the proto-side adapter: it receives gRPC
// calls from the host and forwards to the Go-side
// WolfCIPlugin implementation.
type grpcServer struct {
	pluginv1.UnimplementedWolfCIPluginServer
	Impl WolfCIPlugin
}

func (s *grpcServer) OnBuildComplete(ctx context.Context, ev *pluginv1.BuildCompleteEvent) (*pluginv1.Empty, error) {
	err := s.Impl.OnBuildComplete(ctx, BuildCompleteEvent{
		JobName:     ev.JobName,
		BuildNumber: ev.BuildNumber,
		Status:      ev.Status,
		ExitCode:    ev.ExitCode,
		Error:       ev.Error,
	})
	if err != nil {
		return nil, err
	}
	return &pluginv1.Empty{}, nil
}

// grpcClient is the host-side adapter: it implements
// WolfCIPlugin by issuing gRPC calls.
type grpcClient struct {
	client pluginv1.WolfCIPluginClient
}

func (c *grpcClient) OnBuildComplete(ctx context.Context, ev BuildCompleteEvent) error {
	_, err := c.client.OnBuildComplete(ctx, &pluginv1.BuildCompleteEvent{
		JobName:     ev.JobName,
		BuildNumber: ev.BuildNumber,
		Status:      ev.Status,
		ExitCode:    ev.ExitCode,
		Error:       ev.Error,
	})
	return err
}
