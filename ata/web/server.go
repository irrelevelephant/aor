package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"sort"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldhtml "github.com/yuin/goldmark/renderer/html"

	"aor/ata/db"
	"aor/ata/model"
)

//go:embed templates/*.html templates/partials/*.html static/*
var content embed.FS

// SSE hub for broadcasting events.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan string]string // chan -> workspace filter ("" = all)
}

func newHub() *Hub {
	return &Hub{clients: make(map[chan string]string)}
}

func (h *Hub) Subscribe(workspace string) chan string {
	ch := make(chan string, 64)
	h.mu.Lock()
	h.clients[ch] = workspace
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *Hub) Broadcast(event, workspace, data string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	for ch, filter := range h.clients {
		if filter == "" || filter == workspace {
			select {
			case ch <- msg:
			default:
				// Drop if client is slow.
			}
		}
	}
}

type Server struct {
	db       *db.DB
	hub      *Hub
	pages    map[string]*template.Template
	partials *template.Template
	md       goldmark.Markdown
}

// uniqueSortedTags extracts a deduplicated, sorted list of tags from a tag map.
func uniqueSortedTags(tagMap map[string][]string) []string {
	seen := make(map[string]bool)
	for _, tags := range tagMap {
		for _, t := range tags {
			seen[t] = true
		}
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// tagHue returns a deterministic hue (0–359) for a tag name using FNV-1a.
func tagHue(tag string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(tag)))
	return h.Sum32() % 360
}

func Serve(d *db.DB, addr string) error {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(goldhtml.WithHardWraps()),
		// Default: HTML in markdown source is escaped (safe from XSS).
	)

	funcMap := template.FuncMap{
		"renderMarkdown": func(s string) template.HTML {
			var buf bytes.Buffer
			if err := md.Convert([]byte(s), &buf); err != nil {
				return template.HTML(template.HTMLEscapeString(s))
			}
			return template.HTML(buf.String())
		},
		"statusColor": func(s string) string {
			switch s {
			case "queue":
				return "status-queue"
			case "in_progress":
				return "status-in-progress"
			case "closed":
				return "status-closed"
			default:
				return "status-backlog"
			}
		},
		"json": func(v any) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
		"urlquery": func(s string) template.URL {
			return template.URL(url.QueryEscape(s))
		},
		"workspaceURL": func(path, name string) string {
			if name != "" {
				return "/w/" + url.PathEscape(name)
			}
			return "/w?path=" + url.QueryEscape(path)
		},
		"tagColor": func(tag string) string {
			return fmt.Sprintf("hsl(%d, 55%%, 45%%)", tagHue(tag))
		},
		"tagBgColor": func(tag string) string {
			return fmt.Sprintf("hsla(%d, 60%%, 40%%, 0.15)", tagHue(tag))
		},
		"tagFilterURL": func(wsURL, tag string) string {
			u, err := url.Parse(wsURL)
			if err != nil {
				return wsURL
			}
			q := u.Query()
			q.Set("tag", tag)
			u.RawQuery = q.Encode()
			return u.String()
		},
	}

	// Parse each page template separately to avoid "content" block conflicts.
	// Each page gets: layout + partials + its own page template.
	pageFiles := []string{"index.html", "workspace.html", "task.html", "epic.html"}
	pages := make(map[string]*template.Template, len(pageFiles))
	sharedFiles := []string{"templates/layout.html", "templates/partials/task_row.html", "templates/partials/task_list.html", "templates/partials/comment.html"}
	for _, page := range pageFiles {
		t, err := template.New("").Funcs(funcMap).ParseFS(content, append(sharedFiles, "templates/"+page)...)
		if err != nil {
			return fmt.Errorf("parse template %s: %w", page, err)
		}
		pages[page] = t
	}

	// Partials template set (no layout, just fragments for htmx responses).
	partials, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/partials/*.html")
	if err != nil {
		return fmt.Errorf("parse partials: %w", err)
	}

	srv := &Server{
		db:       d,
		hub:      newHub(),
		pages:    pages,
		partials: partials,
		md:       md,
	}

	mux := http.NewServeMux()

	// Pages.
	mux.HandleFunc("GET /", srv.handleIndex)
	mux.HandleFunc("GET /w", srv.handleWorkspace)
	mux.HandleFunc("GET /w/{name}", srv.handleWorkspaceByName)
	mux.HandleFunc("GET /task/{id}", srv.handleTaskDetail)
	mux.HandleFunc("GET /epic/{id}", srv.handleEpicDetail)

	// Task mutations.
	mux.HandleFunc("POST /task", srv.handleCreateTask)
	mux.HandleFunc("POST /task/{id}", srv.handleUpdateTask)
	mux.HandleFunc("POST /task/{id}/close", srv.handleCloseTask)
	mux.HandleFunc("POST /task/{id}/reopen", srv.handleReopenTask)
	mux.HandleFunc("POST /task/{id}/promote", srv.handlePromoteTask)
	mux.HandleFunc("POST /task/{id}/comments", srv.handleAddComment)
	mux.HandleFunc("POST /epic/{id}/spec", srv.handleUpdateSpec)
	mux.HandleFunc("POST /task/{id}/deps", srv.handleAddDep)
	mux.HandleFunc("POST /task/{id}/deps/remove", srv.handleRemoveDep)
	mux.HandleFunc("POST /task/{id}/tags", srv.handleAddTag)
	mux.HandleFunc("POST /task/{id}/tags/remove", srv.handleRemoveTag)
	mux.HandleFunc("POST /reorder", srv.handleReorder)

	// SSE.
	mux.HandleFunc("GET /events", srv.handleSSE)

	// Partials.
	mux.HandleFunc("GET /partials/task-row/{id}", srv.handlePartialTaskRow)
	mux.HandleFunc("GET /partials/task-list", srv.handlePartialTaskList)

	// Static files.
	mux.Handle("GET /static/", http.FileServerFS(content))

	return http.ListenAndServe(addr, mux)
}

// render executes a page template by name.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, 500)
		return
	}
	t.ExecuteTemplate(w, "layout", data)
}

// --- Page handlers ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	workspaces, err := s.db.WorkspacesWithCounts()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Auto-redirect when exactly 1 workspace has active tasks.
	if len(workspaces) == 1 && r.URL.Query().Get("dashboard") != "1" {
		http.Redirect(w, r, workspaceURL(workspaces[0].Path, workspaces[0].Name), http.StatusTemporaryRedirect)
		return
	}

	s.render(w, "index.html", map[string]any{
		"Workspaces": workspaces,
	})
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	showClosed := r.URL.Query().Get("show_closed") == "1"
	tag := r.URL.Query().Get("tag")

	// Look up workspace name for display.
	var wsName string
	if ws, err := s.db.GetWorkspace(path); err == nil && ws != nil {
		wsName = ws.Name
	}

	queue, _ := s.db.ListTasks(path, model.StatusQueue, "", tag)
	inProgress, _ := s.db.ListTasks(path, model.StatusInProgress, "", tag)
	backlog, _ := s.db.ListTasks(path, model.StatusBacklog, "", tag)

	var closed []model.Task
	if showClosed {
		closed, _ = s.db.ListTasks(path, model.StatusClosed, "", tag)
	}

	// Collect all visible task IDs for batch queries.
	var allIDs []string
	for _, t := range queue {
		allIDs = append(allIDs, t.ID)
	}
	for _, t := range backlog {
		allIDs = append(allIDs, t.ID)
	}
	for _, t := range inProgress {
		allIDs = append(allIDs, t.ID)
	}
	for _, t := range closed {
		allIDs = append(allIDs, t.ID)
	}

	blockedIDs, _ := s.db.BlockedTaskIDs(allIDs)
	if blockedIDs == nil {
		blockedIDs = make(map[string]bool)
	}

	tagMap, _ := s.db.GetTagsForTasks(allIDs)
	if tagMap == nil {
		tagMap = make(map[string][]string)
	}

	// Build filter bar tags. When a tag filter is active, tagMap only has
	// the filtered subset, so we need a DB query for the full list.
	// When unfiltered, derive from tagMap to avoid a second query.
	var allTags []string
	if tag != "" {
		allTags, _ = s.db.ListAllTags(path)
	} else {
		allTags = uniqueSortedTags(tagMap)
	}

	wsURL := workspaceURL(path, wsName)
	showClosedURL := wsURL
	if u, err := url.Parse(wsURL); err == nil {
		q := u.Query()
		q.Set("show_closed", "1")
		u.RawQuery = q.Encode()
		showClosedURL = u.String()
	}
	s.render(w, "workspace.html", map[string]any{
		"Path":           path,
		"Name":           wsName,
		"WorkspaceURL":   wsURL,
		"ShowClosedURL":  showClosedURL,
		"Queue":        queue,
		"InProgress":   inProgress,
		"Backlog":      backlog,
		"Closed":       closed,
		"ShowClosed":   showClosed,
		"BlockedIDs":   blockedIDs,
		"TagMap":       tagMap,
		"AllTags":      allTags,
		"ActiveTag":    tag,
	})
}

func (s *Server) handleWorkspaceByName(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.db.ResolveWorkspace(name)
	if err != nil {
		http.Error(w, "workspace not found", 404)
		return
	}
	// Rewrite into the same request so handleWorkspace can serve it.
	q := r.URL.Query()
	q.Set("path", path)
	r.URL.RawQuery = q.Encode()
	s.handleWorkspace(w, r)
}

// workspaceURL returns the preferred URL for a workspace: /w/name if named, /w?path=... otherwise.
func workspaceURL(path, name string) string {
	if name != "" {
		return "/w/" + url.PathEscape(name)
	}
	return "/w?path=" + url.QueryEscape(path)
}

func (s *Server) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	twc, err := s.db.GetTaskWithComments(id)
	if err != nil {
		http.Error(w, "task not found", 404)
		return
	}

	var epicTitle string
	if twc.EpicID != "" {
		if epic, err := s.db.GetTask(twc.EpicID); err == nil {
			epicTitle = epic.Title
		}
	}

	var wsName string
	if ws, err := s.db.GetWorkspace(twc.Workspace); err == nil && ws != nil {
		wsName = ws.Name
	}

	blockers, _ := s.db.GetBlockers(id, false)
	blocking, _ := s.db.GetBlocking(id)
	isBlocked := false
	for _, b := range blockers {
		if b.Status != model.StatusClosed {
			isBlocked = true
			break
		}
	}

	tags, _ := s.db.GetTags(id)
	allTags, _ := s.db.ListAllTags(twc.Workspace)

	s.render(w, "task.html", map[string]any{
		"Task":          twc,
		"EpicTitle":     epicTitle,
		"WorkspaceName": wsName,
		"WorkspaceURL":  workspaceURL(twc.Workspace, wsName),
		"Blockers":      blockers,
		"Blocking":      blocking,
		"IsBlocked":     isBlocked,
		"Tags":          tags,
		"AllTags":       allTags,
	})
}

func (s *Server) handleEpicDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.GetTask(id)
	if err != nil || !task.IsEpic {
		http.Error(w, "epic not found", 404)
		return
	}

	children, _ := s.db.ListTasks("", "", id, "")
	progress, _ := s.db.EpicProgress(id)
	comments, _ := s.db.ListComments(id)

	// Batch-load tags for children.
	childIDs := make([]string, len(children))
	for i, c := range children {
		childIDs[i] = c.ID
	}
	tagMap, _ := s.db.GetTagsForTasks(childIDs)
	if tagMap == nil {
		tagMap = make(map[string][]string)
	}

	var wsName string
	if ws, err := s.db.GetWorkspace(task.Workspace); err == nil && ws != nil {
		wsName = ws.Name
	}

	s.render(w, "epic.html", map[string]any{
		"Epic":          task,
		"Children":      children,
		"Progress":      progress,
		"Comments":      comments,
		"WorkspaceName": wsName,
		"WorkspaceURL":  workspaceURL(task.Workspace, wsName),
		"TagMap":        tagMap,
	})
}

// --- Mutation handlers ---

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	// Support "content" field: first line = title, rest = body (Google Keep style).
	title := strings.TrimSpace(r.FormValue("title"))
	body := r.FormValue("body")
	if content := strings.TrimSpace(r.FormValue("content")); content != "" && title == "" {
		if idx := strings.Index(content, "\n"); idx >= 0 {
			title = strings.TrimSpace(content[:idx])
			body = strings.TrimSpace(content[idx+1:])
		} else {
			title = content
		}
	}

	if title == "" {
		http.Error(w, "title required", 400)
		return
	}

	task, err := s.db.CreateTask(title, body, r.FormValue("status"), r.FormValue("epic_id"), r.FormValue("workspace"), "")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Add tags if provided.
	if tagsStr := strings.TrimSpace(r.FormValue("tags")); tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				s.db.AddTag(task.ID, t)
			}
		}
	}

	s.hub.Broadcast("task_created", task.Workspace, task.ID)

	// htmx redirect.
	var wsName string
	if ws, err := s.db.GetWorkspace(task.Workspace); err == nil && ws != nil {
		wsName = ws.Name
	}
	wsURL := workspaceURL(task.Workspace, wsName)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", wsURL)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, wsURL, http.StatusSeeOther)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()

	task, err := s.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}

	// Update fields that are present.
	if title := r.FormValue("title"); title != "" {
		s.db.Exec(`UPDATE tasks SET title = ? WHERE id = ?`, title, id)
	}
	if body := r.FormValue("body"); r.Form.Has("body") {
		s.db.Exec(`UPDATE tasks SET body = ? WHERE id = ?`, body, id)
	}

	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		task, _ = s.db.GetTask(id)
		s.partials.ExecuteTemplate(w, "task_row.html", task)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleCloseTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	reason := r.FormValue("reason")

	task, err := s.db.CloseTask(id, reason)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("task_closed", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/task/"+id)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleReopenTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.ReopenTask(id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/task/"+id)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handlePromoteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.PromoteToEpic(id, "")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("epic_promoted", task.Workspace, id)
	http.Redirect(w, r, "/epic/"+id, http.StatusSeeOther)
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Error(w, "body required", 400)
		return
	}

	comment, err := s.db.AddComment(id, body, model.AuthorHuman)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("comment_added", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		s.partials.ExecuteTemplate(w, "comment.html", comment)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleUpdateSpec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	spec := r.FormValue("spec")

	_, err := s.db.UpdateSpec(id, spec)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	http.Redirect(w, r, "/epic/"+id, http.StatusSeeOther)
}

func (s *Server) handleAddDep(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	dependsOn := strings.TrimSpace(r.FormValue("depends_on"))
	if dependsOn == "" {
		http.Error(w, "depends_on required", 400)
		return
	}

	if err := s.db.AddDep(id, dependsOn); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/task/"+id)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleRemoveDep(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	dependsOn := strings.TrimSpace(r.FormValue("depends_on"))
	if dependsOn == "" {
		http.Error(w, "depends_on required", 400)
		return
	}

	if err := s.db.RemoveDep(id, dependsOn); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/task/"+id)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleAddTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag == "" {
		http.Error(w, "tag required", 400)
		return
	}

	if err := s.db.AddTag(id, tag); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/task/"+id)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleRemoveTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	tag := strings.TrimSpace(r.FormValue("tag"))
	if tag == "" {
		http.Error(w, "tag required", 400)
		return
	}

	if err := s.db.RemoveTag(id, tag); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/task/"+id)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func (s *Server) handleReorder(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.FormValue("id")
	posStr := r.FormValue("position")
	newStatus := r.FormValue("status")
	var pos int
	fmt.Sscanf(posStr, "%d", &pos)

	if err := s.db.Reorder(id, pos, newStatus); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_reordered", task.Workspace, id)
	w.WriteHeader(200)
}

// --- SSE ---

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	workspace := r.URL.Query().Get("workspace")
	ch := s.hub.Subscribe(workspace)
	defer s.hub.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Keepalive ticker.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ":keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// --- Partials ---

func (s *Server) handlePartialTaskRow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	s.partials.ExecuteTemplate(w, "task_row.html", task)
}

func (s *Server) handlePartialTaskList(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	status := r.URL.Query().Get("status")

	tasks, _ := s.db.ListTasks(workspace, status, "", "")
	s.partials.ExecuteTemplate(w, "task_list.html", tasks)
}
