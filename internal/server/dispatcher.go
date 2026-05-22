package server

/* Dispatcher is the single-port multiplexer (PLAN.md task 11.4).
 * cmd/wolfci binds ONE TLS listener via internal/tlsutil; every
 * accepted connection feeds into a net/http.Server whose handler
 * is a Dispatcher. The dispatcher inspects the request's
 * Content-Type:
 *
 *   - "application/grpc" (any subtype, e.g. +proto, +json,
 *     "; charset=utf-8") -> route to GRPC, which is typically
 *     grpc.Server.ServeHTTP for the wolfCI server's gRPC
 *     services (agentsvc + cliservice).
 *
 *   - anything else -> route to UI, which is server.New's
 *     http.Handler.
 *
 * Both handlers must coexist on the same TLS listener; ALPN
 * negotiation in wolfSSL (--enable-alpn) advertises h2 so a
 * gRPC client can dial directly and Go's net/http.Server runs
 * HTTP/2 on the back of that.
 */

import (
    "net/http"
    "strings"
)

type Dispatcher struct {
    UI   http.Handler
    GRPC http.Handler
}

func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    /* The full Content-Type may carry parameters
     * ("application/grpc; charset=utf-8") or a subtype
     * ("application/grpc+proto"). HasPrefix on "application/grpc"
     * matches all gRPC variants without matching e.g.
     * "application/grpc-web" (which is intentional - the wolfCI
     * gRPC path does not speak gRPC-Web).
     */
    if strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
        if d.GRPC != nil {
            d.GRPC.ServeHTTP(w, r)
            return
        }
        http.Error(w, "gRPC handler not configured",
            http.StatusServiceUnavailable)
        return
    }
    if d.UI != nil {
        d.UI.ServeHTTP(w, r)
        return
    }
    http.Error(w, "UI handler not configured", http.StatusServiceUnavailable)
}
