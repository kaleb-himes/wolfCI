#!/bin/sh
# scripts/gen-proto.sh - regenerate Go code from .proto files.
#
# Requires:
#   - protoc (libprotoc 3.x or newer) on PATH
#   - protoc-gen-go and protoc-gen-go-grpc on PATH
#     (commonly installed at $HOME/go/bin via:
#        go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.31.0
#        go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0)
#
# Outputs:
#   api/v1/*.pb.go        protobuf message types
#   api/v1/*_grpc.pb.go   gRPC service types

set -eu

cd "$(dirname "$0")/.."

# Make sure the Go plugins installed by 'go install' are visible.
export PATH="$HOME/go/bin:$PATH"

for tool in protoc protoc-gen-go protoc-gen-go-grpc; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "gen-proto.sh: required tool '$tool' is not on PATH" >&2
        echo "Install protoc via your package manager and the Go plugins via:" >&2
        echo "  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.31.0" >&2
        echo "  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0" >&2
        exit 1
    fi
done

protoc \
    --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    api/v1/agent.proto \
    api/v1/plugin/plugin.proto \
    api/v1/cli/cli.proto

echo "gen-proto.sh: regenerated api/v1/*.pb.go, api/v1/plugin/*.pb.go, api/v1/cli/*.pb.go"
