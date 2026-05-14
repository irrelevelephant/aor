// Package web mounts atx's HTTP surface at /atx/ on a shared mux.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"aor/atx/config"
	"aor/atx/db"
	"aor/atx/runtime"
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

func WithRuntime(r *runtime.Manager) Option {
	return func(s *Server) { s.rt = r }
}

type Server struct {
	db    *db.DB
	cfg   *config.Config
	rt    *runtime.Manager
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
	views := s.machineViews()
	s.render(w, "machines.html", map[string]any{
		"Title":    "machines",
		"Machines": views,
	})
}

func (s *Server) handleWindows(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	state, ok := s.machineState(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	wins := make([]WindowView, 0, len(state.Windows))
	for _, win := range state.Windows {
		wins = append(wins, WindowView{
			Index:        win.Index,
			Name:         win.Name,
			LastActivity: relativeTime(win.LastActivity),
		})
	}

	s.render(w, "windows.html", map[string]any{
		"Title": state.Display,
		"Machine": MachineView{
			Name:    state.Name,
			Display: state.Display,
			Color:   state.Color,
			Online:  state.Online,
		},
		"Windows": wins,
	})
}

func (s *Server) machineViews() []MachineView {
	if s.rt == nil {
		out := make([]MachineView, 0, len(s.cfg.Machines))
		for _, m := range s.cfg.Machines {
			out = append(out, MachineView{Name: m.Name, Display: m.Display, Color: m.Color})
		}
		return out
	}
	states := s.rt.Snapshot()
	out := make([]MachineView, 0, len(states))
	for _, st := range states {
		out = append(out, MachineView{
			Name:         st.Name,
			Display:      st.Display,
			Color:        st.Color,
			Online:       st.Online,
			WindowCount:  len(st.Windows),
			LastActivity: machineActivity(st),
		})
	}
	return out
}

func (s *Server) machineState(name string) (runtime.MachineState, bool) {
	if s.rt != nil {
		return s.rt.MachineState(name)
	}
	for _, m := range s.cfg.Machines {
		if m.Name == name {
			return runtime.MachineState{Name: m.Name, Display: m.Display, Color: m.Color}, true
		}
	}
	return runtime.MachineState{}, false
}

func machineActivity(st runtime.MachineState) string {
	if !st.Online {
		if st.LastError != "" {
			return "offline"
		}
		return "connecting…"
	}
	if len(st.Windows) == 0 {
		return "no windows"
	}
	return "live"
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < 5*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
