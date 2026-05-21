# wolfCI Plugins

wolfCI is extended through out-of-process plugins. Each plugin
is a separate binary that the wolfCI server spawns as a
subprocess and talks to over a Unix-domain gRPC socket. The
lifecycle (handshake, version check, graceful shutdown,
restart-on-crash) is handled by the upstream
[hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)
library so plugin authors do not have to reinvent it.

## What a plugin can do

Phase 7 ships one hook:

| Hook              | Fired when                                | Payload                              |
| ----------------- | ----------------------------------------- | ------------------------------------ |
| OnBuildComplete   | The server records a build's final result | job_name, build_number, status, exit_code, error |

Future hooks land here. `status` is one of `success`,
`failure`, `cancelled`, or `error` (mirroring
`scheduler.Status`).

## Where plugins live

```
plugins/
  installed/
    <name>/
      <name>           the plugin binary (same name as its directory)
      config.yaml      optional plugin-specific config (the wolfCI server does not read it; the plugin does)
```

The wolfCI server scans `plugins/installed/` on startup,
launches each subdirectory whose binary name matches the
directory name, and keeps the connection open for the lifetime
of the wolfCI process. Adding a plugin = drop a directory;
removing one = `rm -r`. No restart magic, no registry.

## Wire protocol

The protocol lives at [api/v1/plugin/plugin.proto](../api/v1/plugin/plugin.proto):

```proto
service WolfCIPlugin {
  rpc OnBuildComplete(BuildCompleteEvent) returns (Empty);
}
```

Generated Go bindings live in `api/v1/plugin/` and are
committed to the repo so plugin authors do not need protoc to
build.

## Handshake

The wolfCI host announces itself with this handshake config:

```go
plugin.HandshakeConfig{
    ProtocolVersion:  1,
    MagicCookieKey:   "WOLFCI_PLUGIN_COOKIE",
    MagicCookieValue: "wolfci-v1",
}
```

Plugins MUST present the same values or the host refuses to
load them. The cookie blocks "I accidentally ran my plugin
under bash" - go-plugin's parent expects this magic and a
plugin invoked standalone exits with a helpful message.

## Writing a plugin (Go)

1. Implement the `internal/plugin.WolfCIPlugin` interface
   (lands in Phase 7.3).
2. Register it via `plugin.Serve` with the wolfCI handshake.
3. `go build` to a binary named `<your-plugin>` and drop it at
   `plugins/installed/<your-plugin>/<your-plugin>`.

A complete `plugins/examples/hello` plugin will ship with
Phase 7.3 as a reference.

## Writing a plugin (other languages)

go-plugin supports any language that can run a gRPC server
once the handshake is negotiated. The plugin binary must:

1. Read `PLUGIN_PROTOCOL_VERSIONS` and `PLUGIN_MIN_PORT` /
   `MAX_PORT` from the environment.
2. Write its handshake line on stdout in the format documented
   by go-plugin (`<core version>|<plugin version>|<network>|<addr>|grpc`).
3. Serve the gRPC service defined in plugin.proto on the chosen
   network address.

In practice, every plugin language other than Go has to
re-implement go-plugin's handshake bytes. For now, write
plugins in Go.

## Threat model

- Plugins are TRUSTED. They run as the wolfCI server process
  user with full access to its file system and network.
- Do NOT install plugins from untrusted sources.
- A misbehaving plugin can crash; the host restarts it on the
  next hook fire (Phase 7.3 implementation detail).
- The host does not sandbox plugins. If isolation matters, run
  wolfCI under a dedicated, low-privilege user.

End of docs/PLUGINS.md.
