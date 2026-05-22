package server_test

/* Gates PLAN.md task 11.4: a single http.Handler dispatches
 * application/grpc requests to grpc.Server.ServeHTTP and
 * everything else to the UI handler. The whole point is that
 * cmd/wolfci runs ONE TLS listener; this Dispatcher is the
 * routing fork inside that listener.
 */

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/server"
)

/* recordingHandler counts calls + remembers the last request so a
 * test can assert which side of the dispatcher fired.
 */
type recordingHandler struct {
    name  string
    calls int
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    h.calls++
    w.Header().Set("X-Routed-To", h.name)
    w.WriteHeader(http.StatusOK)
}

func newDispatcher() (*server.Dispatcher, *recordingHandler, *recordingHandler) {
    ui := &recordingHandler{name: "ui"}
    grpc := &recordingHandler{name: "grpc"}
    return &server.Dispatcher{UI: ui, GRPC: grpc}, ui, grpc
}

func TestDispatcher_RoutesGRPCContentType(t *testing.T) {
    d, ui, grpc := newDispatcher()
    req := httptest.NewRequest(http.MethodPost,
        "/wolfci.AgentService/Connect", nil)
    req.Header.Set("Content-Type", "application/grpc")
    rec := httptest.NewRecorder()
    d.ServeHTTP(rec, req)

    if rec.Header().Get("X-Routed-To") != "grpc" {
        t.Errorf("X-Routed-To = %q, want grpc",
            rec.Header().Get("X-Routed-To"))
    }
    if grpc.calls != 1 {
        t.Errorf("grpc.calls = %d, want 1", grpc.calls)
    }
    if ui.calls != 0 {
        t.Errorf("ui.calls = %d, want 0", ui.calls)
    }
}

func TestDispatcher_RoutesGRPCContentTypeSubtypes(t *testing.T) {
    /* gRPC variants: +proto, +json, etc. all start with
     * "application/grpc" - the dispatcher must accept them all.
     */
    for _, ct := range []string{
        "application/grpc",
        "application/grpc+proto",
        "application/grpc+json",
        "application/grpc; charset=utf-8",
    } {
        t.Run(ct, func(t *testing.T) {
            d, _, grpc := newDispatcher()
            req := httptest.NewRequest(http.MethodPost, "/x.Y/Z", nil)
            req.Header.Set("Content-Type", ct)
            d.ServeHTTP(httptest.NewRecorder(), req)
            if grpc.calls != 1 {
                t.Errorf("Content-Type %q: grpc.calls = %d, want 1",
                    ct, grpc.calls)
            }
        })
    }
}

func TestDispatcher_RoutesUIPath(t *testing.T) {
    d, ui, grpc := newDispatcher()
    req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
    /* Typical browser request: Content-Type set on the request
     * is empty for GET; the dispatcher must default to UI.
     */
    rec := httptest.NewRecorder()
    d.ServeHTTP(rec, req)

    if rec.Header().Get("X-Routed-To") != "ui" {
        t.Errorf("X-Routed-To = %q, want ui",
            rec.Header().Get("X-Routed-To"))
    }
    if ui.calls != 1 {
        t.Errorf("ui.calls = %d, want 1", ui.calls)
    }
    if grpc.calls != 0 {
        t.Errorf("grpc.calls = %d, want 0", grpc.calls)
    }
}

func TestDispatcher_DefaultsToUI_OnOtherContentTypes(t *testing.T) {
    for _, ct := range []string{
        "",
        "text/html",
        "application/json",
        "application/x-www-form-urlencoded",
        "multipart/form-data; boundary=----foo",
    } {
        t.Run(ct, func(t *testing.T) {
            d, ui, grpc := newDispatcher()
            req := httptest.NewRequest(http.MethodPost, "/x", nil)
            if ct != "" {
                req.Header.Set("Content-Type", ct)
            }
            d.ServeHTTP(httptest.NewRecorder(), req)
            if ui.calls != 1 {
                t.Errorf("ct=%q: ui.calls = %d, want 1", ct, ui.calls)
            }
            if grpc.calls != 0 {
                t.Errorf("ct=%q: grpc.calls = %d, want 0", ct, grpc.calls)
            }
        })
    }
}
