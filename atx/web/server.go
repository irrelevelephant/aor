// Package web mounts atx's HTTP surface at /atx/ on a shared mux.
package web

import (
	"net/http"
)

type Option func(*Server)

type SSEBroadcaster interface {
	Broadcast(event, data string)
}

func WithSSE(b SSEBroadcaster) Option {
	return func(s *Server) { s.sse = b }
}

type Server struct {
	sse SSEBroadcaster
}

// RegisterRoutes registers atx routes on the given mux under the /atx/ prefix.
func RegisterRoutes(mux *http.ServeMux, opts ...Option) *Server {
	srv := &Server{}
	for _, o := range opts {
		o(srv)
	}

	mux.HandleFunc("GET /atx/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("atx: wired up\n"))
	})

	return srv
}
