// Package web mounts atx's HTTP surface at /atx/ on a shared mux.
package web

import (
	"fmt"
	"net/http"

	"aor/atx/config"
	"aor/atx/db"
)

type Option func(*Server)

type SSEBroadcaster interface {
	Broadcast(event, data string)
}

func WithSSE(b SSEBroadcaster) Option {
	return func(s *Server) { s.sse = b }
}

type Server struct {
	db  *db.DB
	cfg *config.Config
	sse SSEBroadcaster
}

// RegisterRoutes registers atx routes on the given mux under the /atx/ prefix.
func RegisterRoutes(mux *http.ServeMux, d *db.DB, cfg *config.Config, opts ...Option) *Server {
	srv := &Server{db: d, cfg: cfg}
	for _, o := range opts {
		o(srv)
	}

	mux.HandleFunc("GET /atx/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "atx: %d machine(s) configured\n", len(srv.cfg.Machines))
	})

	return srv
}
