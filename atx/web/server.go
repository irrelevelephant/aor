// Package web mounts atx's HTTP surface at /atx/ on a shared mux.
package web

import (
	"embed"
	"html/template"
	"log"
	"net/http"

	"aor/atx/config"
	"aor/atx/db"
)

//go:embed templates/*.html static/*
var content embed.FS

type Option func(*Server)

type SSEBroadcaster interface {
	Broadcast(event, data string)
}

func WithSSE(b SSEBroadcaster) Option {
	return func(s *Server) { s.sse = b }
}

type Server struct {
	db    *db.DB
	cfg   *config.Config
	sse   SSEBroadcaster
	pages map[string]*template.Template
}

type MachineView struct {
	Name         string
	Display      string
	Color        string
	Online       bool
	WindowCount  int
	LastActivity string
}

type WindowView struct {
	Index        int
	Name         string
	Active       bool
	LastActivity string
}

// RegisterRoutes registers atx routes on the given mux under the /atx/ prefix.
func RegisterRoutes(mux *http.ServeMux, d *db.DB, cfg *config.Config, opts ...Option) *Server {
	pageFiles := []string{"machines.html", "windows.html"}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, p := range pageFiles {
		t, err := template.New("").ParseFS(content, "templates/layout.html", "templates/"+p)
		if err != nil {
			log.Fatalf("parse atx template %s: %v", p, err)
		}
		pages[p] = t
	}

	srv := &Server{db: d, cfg: cfg, pages: pages}
	for _, o := range opts {
		o(srv)
	}

	mux.HandleFunc("GET /atx/{$}", srv.handleMachines)
	mux.HandleFunc("GET /atx/m/{machine}", srv.handleWindows)

	mux.HandleFunc("GET /atx/manifest.json", serveEmbedded("static/manifest.json", "application/manifest+json"))
	mux.HandleFunc("GET /atx/sw.js", serveEmbedded("static/sw.js", "application/javascript"))
	mux.Handle("GET /atx/static/", http.StripPrefix("/atx", http.FileServerFS(content)))

	return srv
}

func serveEmbedded(embedPath, contentType string) http.HandlerFunc {
	data, err := content.ReadFile(embedPath)
	if err != nil {
		log.Fatalf("atx embed missing %s: %v", embedPath, err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, 500)
		return
	}
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("atx template %s: %v", page, err)
	}
}

func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	views := make([]MachineView, 0, len(s.cfg.Machines))
	for _, m := range s.cfg.Machines {
		views = append(views, MachineView{
			Name:         m.Name,
			Display:      m.Display,
			Color:        m.Color,
			Online:       true,
			WindowCount:  4,
			LastActivity: "just now",
		})
	}
	s.render(w, "machines.html", map[string]any{
		"Title":    "machines",
		"Machines": views,
	})
}

func (s *Server) handleWindows(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	var m *config.Machine
	for i := range s.cfg.Machines {
		if s.cfg.Machines[i].Name == name {
			m = &s.cfg.Machines[i]
			break
		}
	}
	if m == nil {
		http.NotFound(w, r)
		return
	}

	windows := []WindowView{
		{Index: 0, Name: "shell", Active: true, LastActivity: "now"},
		{Index: 1, Name: "build", Active: false, LastActivity: "2m ago"},
		{Index: 2, Name: "claude", Active: false, LastActivity: "12s ago"},
		{Index: 3, Name: "logs", Active: false, LastActivity: "1h ago"},
	}

	s.render(w, "windows.html", map[string]any{
		"Title": m.Display,
		"Machine": MachineView{
			Name:    m.Name,
			Display: m.Display,
			Color:   m.Color,
			Online:  true,
		},
		"Windows": windows,
	})
}
