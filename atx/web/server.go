// Package web mounts atx's HTTP surface at /atx/ on a shared mux.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"aor/atx/config"
	"aor/atx/db"
	"aor/atx/push"
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

func WithVAPID(v *push.VAPID) Option {
	return func(s *Server) { s.vapid = v }
}

type Server struct {
	db    *db.DB
	cfg   *config.Config
	rt    *runtime.Manager
	vapid *push.VAPID
	sse   SSEBroadcaster
	pages map[string]*template.Template
}

type MachineView struct {
	Name         string `json:"name"`
	Display      string `json:"display"`
	Color        string `json:"color"`
	Online       bool   `json:"online"`
	WindowCount  int    `json:"windowCount"`
	LastActivity string `json:"lastActivity,omitempty"`
	// Windows is populated when this view feeds the "window-list"
	// template block (handleMachineWindowsAPI) or the JSON machines
	// API consumed by the terminal-view picker. The unified-view list
	// render leaves it nil and the client lazy-fetches on expand.
	Windows []WindowView `json:"windows,omitempty"`
}

type WindowView struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Notified string `json:"notified,omitempty"`
}

func RegisterRoutes(mux *http.ServeMux, d *db.DB, cfg *config.Config, opts ...Option) *Server {
	funcMap := template.FuncMap{
		"json": func(v any) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
	}

	pageFiles := []string{"machines.html", "terminal.html"}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, p := range pageFiles {
		t, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html", "templates/"+p)
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
	mux.HandleFunc("GET /atx/m/{machine}", srv.handleMachineRedirect)
	mux.HandleFunc("GET /atx/m/{machine}/w/{window}", srv.handleTerminal)
	mux.HandleFunc("GET /atx/ws", srv.handleWS)

	mux.HandleFunc("GET /atx/api/m/{machine}/windows", srv.handleMachineWindowsAPI)
	mux.HandleFunc("GET /atx/api/machines", srv.handleMachinesAPI)

	mux.HandleFunc("GET /atx/api/push/vapid-public-key", srv.handleVAPIDPublicKey)
	mux.HandleFunc("POST /atx/api/push/subscribe", srv.handlePushSubscribe)
	mux.HandleFunc("POST /atx/api/push/unsubscribe", srv.handlePushUnsubscribe)
	mux.HandleFunc("POST /atx/api/hooks/event", srv.handleHookEvent)

	mux.HandleFunc("GET /atx/manifest.json", serveEmbedded("static/manifest.json", "application/manifest+json"))
	mux.HandleFunc("GET /atx/sw.js", serveEmbedded("static/sw.js", "application/javascript"))
	mux.Handle("GET /atx/static/", noCache(http.StripPrefix("/atx", http.FileServerFS(content))))

	return srv
}

func serveEmbedded(embedPath, contentType string) http.HandlerFunc {
	data, err := content.ReadFile(embedPath)
	if err != nil {
		log.Fatalf("atx embed missing %s: %v", embedPath, err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Write(data)
	}
}

// noCache wraps a handler so static assets always revalidate. Without
// this, browsers serve aggressively-cached CSS/JS and miss UI fixes
// until the user manually hard-refreshes.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		h.ServeHTTP(w, r)
	})
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
	machines, offlineStart := s.machineListView(false)
	s.render(w, "machines.html", map[string]any{
		"Title":        "machines",
		"Machines":     machines,
		"OfflineStart": offlineStart,
	})
}

// handleMachineRedirect keeps old /atx/m/{name} bookmarks (and the push
// notification fallback URL when no window is supplied) working by
// pointing them at the unified view's anchor for that machine.
func (s *Server) handleMachineRedirect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	http.Redirect(w, r, "/atx/#m-"+url.PathEscape(name), http.StatusFound)
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	idxStr := r.PathValue("window")
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	state, ok := s.machineState(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	var win runtime.Window
	found := false
	for _, ww := range state.Windows {
		if ww.Index == idx {
			win = ww
			found = true
			break
		}
	}
	if !found {
		win = runtime.Window{Index: idx, Name: fmt.Sprintf("w%d", idx)}
	}

	machines, _ := s.machineListView(true)

	s.render(w, "terminal.html", map[string]any{
		"Title": fmt.Sprintf("%s · %d %s", state.Display, idx, win.Name),
		"Machine": MachineView{
			Name:    state.Name,
			Display: state.Display,
			Color:   state.Color,
			Online:  state.Online,
		},
		"Window": WindowView{
			Index: win.Index,
			Name:  win.Name,
		},
		"Machines": machines,
	})
}

// handleMachineWindowsAPI renders the same window-list block the unified
// view inlines for eager-expanded machines, so the JS lazy-load on first
// expand can swap the response in via innerHTML — single source of truth.
func (s *Server) handleMachineWindowsAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("machine")
	state, ok := s.machineState(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages["machines.html"].ExecuteTemplate(w, "window-list", MachineView{
		Name:    state.Name,
		Windows: s.windowViews(state),
	}); err != nil {
		log.Printf("atx window-list render %s: %v", state.Name, err)
	}
}

func (s *Server) windowViews(state runtime.MachineState) []WindowView {
	lastNotified, err := s.db.LastNotifiedByWindow(state.Name)
	if err != nil {
		log.Printf("atx last-notified %s: %v", state.Name, err)
	}
	wins := make([]WindowView, 0, len(state.Windows))
	for _, win := range state.Windows {
		var notified string
		if ts, ok := lastNotified[strconv.Itoa(win.Index)]; ok {
			notified = relativeTime(time.Unix(ts, 0))
		}
		wins = append(wins, WindowView{
			Index:    win.Index,
			Name:     win.Name,
			Notified: notified,
		})
	}
	return wins
}

// machineListView returns machines ordered online-first, offline-after
// (config order preserved within each group). offlineStart is the index
// of the first offline machine, or len(out) if none.
//
// withWindows controls whether each machine's Windows slice is populated
// with lightweight {Index, Name} entries. Callers that need per-window
// notification timestamps must use windowViews() directly — populating
// Notified in this loop would issue an N+1 DB query per snapshot.
func (s *Server) machineListView(withWindows bool) ([]MachineView, int) {
	if s.rt == nil {
		out := make([]MachineView, 0, len(s.cfg.Machines))
		for _, m := range s.cfg.Machines {
			out = append(out, MachineView{Name: m.Name, Display: m.Display, Color: m.Color})
		}
		return out, len(out)
	}
	states := s.rt.Snapshot()
	online := make([]MachineView, 0, len(states))
	offline := make([]MachineView, 0)
	for _, st := range states {
		mv := MachineView{
			Name:         st.Name,
			Display:      st.Display,
			Color:        st.Color,
			Online:       st.Online,
			WindowCount:  len(st.Windows),
			LastActivity: machineActivity(st),
		}
		if withWindows {
			wins := make([]WindowView, 0, len(st.Windows))
			for _, w := range st.Windows {
				wins = append(wins, WindowView{Index: w.Index, Name: w.Name})
			}
			mv.Windows = wins
		}
		if st.Online {
			online = append(online, mv)
		} else {
			offline = append(offline, mv)
		}
	}
	return append(online, offline...), len(online)
}

func (s *Server) handleMachinesAPI(w http.ResponseWriter, r *http.Request) {
	machines, _ := s.machineListView(true)
	writeJSON(w, http.StatusOK, map[string]any{"machines": machines})
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
