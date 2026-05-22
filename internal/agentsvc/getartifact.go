package agentsvc

/* GetArtifact streams one artifact file from
 * builds/<upstream_job>/<upstream_build>/artifacts/<basename>
 * to the calling agent. Phase 15.5.
 *
 * Authorization: today we gate by "the caller identified
 * themselves via the wolfci-agent-id metadata header AND
 * that agent_id is in the registered agents map". This is a
 * minimum bar - the proper cert-CN check + the "calling
 * agent must be the one assigned the downstream build" rule
 * is tracked under the same matrix-driven gRPC authz
 * follow-up as the Phase 12.7 nodes.configure note.
 *
 * Path safety: artifact_basename must be a leaf filename.
 * "/" / "\" / ".." segments are refused BEFORE any disk
 * access so a typo cannot escape the artifacts dir.
 */

import (
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "strings"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/metadata"
    "google.golang.org/grpc/status"

    wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
)

/* artifactChunkSize is the per-message payload cap. 32 KiB
 * fits comfortably inside the default gRPC 4 MiB frame and
 * matches what the agent-side log streaming uses.
 */
const artifactChunkSize = 32 * 1024

/* GetArtifact implements wolfciv1.AgentServiceServer. */
func (s *Server) GetArtifact(req *wolfciv1.GetArtifactRequest,
    stream wolfciv1.AgentService_GetArtifactServer) error {

    if req == nil {
        return status.Error(codes.InvalidArgument,
            "GetArtifact: nil request")
    }
    if req.UpstreamJob == "" || req.UpstreamBuild < 1 {
        return status.Error(codes.InvalidArgument,
            "GetArtifact: upstream_job and upstream_build are required")
    }
    base := req.ArtifactBasename
    if base == "" {
        return status.Error(codes.InvalidArgument,
            "GetArtifact: artifact_basename is required")
    }
    if strings.ContainsAny(base, `/\`) ||
        base == ".." || base == "." ||
        strings.Contains(base, "..") {
        return status.Error(codes.InvalidArgument,
            "GetArtifact: artifact_basename must be a leaf filename")
    }

    if err := s.authorizeArtifactCaller(stream.Context()); err != nil {
        return err
    }

    if s.WorkDir == "" {
        return status.Error(codes.FailedPrecondition,
            "GetArtifact: server WorkDir is not configured")
    }
    path := filepath.Join(s.WorkDir, "builds",
        req.UpstreamJob, strconv.Itoa(int(req.UpstreamBuild)),
        "artifacts", base)
    f, err := os.Open(path)
    if err != nil {
        if os.IsNotExist(err) {
            return status.Error(codes.NotFound,
                fmt.Sprintf("artifact not found: %s/%d/%s",
                    req.UpstreamJob, req.UpstreamBuild, base))
        }
        return status.Error(codes.Internal,
            "open artifact: "+err.Error())
    }
    defer f.Close()

    buf := make([]byte, artifactChunkSize)
    for {
        n, err := f.Read(buf)
        if n > 0 {
            if sendErr := stream.Send(&wolfciv1.ArtifactChunk{
                Data: append([]byte(nil), buf[:n]...),
            }); sendErr != nil {
                return sendErr
            }
        }
        if err != nil {
            if err == io.EOF {
                return nil
            }
            return status.Error(codes.Internal,
                "read artifact: "+err.Error())
        }
    }
}

/* authorizeArtifactCaller enforces the agent-id metadata
 * gate. The header is "wolfci-agent-id"; the value must
 * match an agent currently in the registered agents map.
 * Empty header, missing metadata, or unknown agent ID all
 * return PermissionDenied.
 */
func (s *Server) authorizeArtifactCaller(ctx context.Context) error {
    md, ok := metadata.FromIncomingContext(ctx)
    if !ok || md == nil {
        return status.Error(codes.PermissionDenied,
            "GetArtifact: missing agent-id metadata")
    }
    vals := md.Get("wolfci-agent-id")
    if len(vals) == 0 || vals[0] == "" {
        return status.Error(codes.PermissionDenied,
            "GetArtifact: missing agent-id metadata")
    }
    agentID := vals[0]
    s.mu.Lock()
    _, known := s.agents[agentID]
    s.mu.Unlock()
    if !known {
        return status.Error(codes.PermissionDenied,
            "GetArtifact: unrecognized agent-id "+agentID)
    }
    return nil
}
