package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	db             *db.DB
	hub            *Hub
	pages          map[string]*template.Template
	partials       *template.Template
	md             goldmark.Markdown
	attachmentsDir string
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

// tagsForTasks returns a deduplicated, sorted list of tags that appear on the
// given task slices, looked up from tagMap.
func tagsForTasks(tagMap map[string][]string, slices ...[]model.Task) []string {
	seen := make(map[string]bool)
	for _, tasks := range slices {
		for _, t := range tasks {
			for _, tag := range tagMap[t.ID] {
				seen[tag] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// tagPalette contains well-spaced hues that avoid 250–310 (purple, used by epics).
var tagPalette = []uint32{0, 28, 55, 85, 125, 165, 195, 220, 320, 345}

// tagHue returns a deterministic hue from the curated palette using FNV-1a.
func tagHue(tag string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(tag)))
	return tagPalette[h.Sum32()%uint32(len(tagPalette))]
}

// tagSet converts a tag slice to a map for O(1) template lookups.
func tagSet(tags []string) map[string]bool {
	m := make(map[string]bool, len(tags))
	for _, t := range tags {
		m[t] = true
	}
	return m
}

// tagFilterData builds the data map for the tag_filter_bar partial.
func tagFilterData(allTags []string, includeTags, excludeTags string) map[string]any {
	inc := db.SplitComma(includeTags)
	exc := db.SplitComma(excludeTags)
	return map[string]any{
		"AllTags":     allTags,
		"IncludeSet":  tagSet(inc),
		"ExcludeSet":  tagSet(exc),
		"HasFilters":  len(inc) > 0 || len(exc) > 0,
	}
}

// setTagQuery returns baseURL with the tag/xtag query params replaced.
// If key is empty, both tag and xtag are cleared.
func setTagQuery(baseURL, key, value string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	q := u.Query()
	q.Del("tag")
	q.Del("xtag")
	if key != "" {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func Serve(d *db.DB, addr, tlsCert, tlsKey string) error {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(goldhtml.WithHardWraps()),
		// Default: HTML in markdown source is escaped (safe from XSS).
	)

	// dict builds an ad-hoc map from key/value pairs for template calls.
	dict := func(pairs ...any) map[string]any {
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			m[pairs[i].(string)] = pairs[i+1]
		}
		return m
	}
	funcMap := template.FuncMap{
		"dict": dict,
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
		"tagColor": func(tag string) template.CSS {
			return template.CSS(fmt.Sprintf("hsl(%d, 70%%, 75%%)", tagHue(tag)))
		},
		"tagBgColor": func(tag string) template.CSS {
			return template.CSS(fmt.Sprintf("hsl(%d, 50%%, 18%%)", tagHue(tag)))
		},
		"tagFilterURL": func(baseURL, tag string) string {
			return setTagQuery(baseURL, "tag", tag)
		},
		"formatBytes": db.FormatBytes,
	}

	// Parse each page template separately to avoid "content" block conflicts.
	// Each page gets: layout + partials + its own page template.
	pageFiles := []string{"index.html", "workspace.html", "task.html", "epic.html"}
	pages := make(map[string]*template.Template, len(pageFiles))
	sharedFiles := []string{"templates/layout.html", "templates/partials/task_row.html", "templates/partials/task_list.html", "templates/partials/comment.html", "templates/partials/tag_filter_bar.html", "templates/partials/task_tags_inline.html"}
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

	// Resolve attachments directory.
	attDir, _ := db.AttachmentsDir()

	srv := &Server{
		db:             d,
		hub:            newHub(),
		pages:          pages,
		partials:       partials,
		md:             md,
		attachmentsDir: attDir,
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
	mux.HandleFunc("POST /task/{id}/demote", srv.handleDemoteEpic)
	mux.HandleFunc("POST /task/{id}/comments", srv.handleAddComment)
	mux.HandleFunc("POST /epic/{id}/spec", srv.handleUpdateSpec)
	mux.HandleFunc("POST /task/{id}/deps", srv.handleAddDep)
	mux.HandleFunc("POST /task/{id}/deps/remove", srv.handleRemoveDep)
	mux.HandleFunc("POST /task/{id}/tags", srv.handleAddTag)
	mux.HandleFunc("POST /task/{id}/tags/remove", srv.handleRemoveTag)
	mux.HandleFunc("POST /task/{id}/attachments", srv.handleUploadAttachment)
	mux.HandleFunc("POST /task/{id}/attachments/delete", srv.handleDeleteAttachment)
	mux.HandleFunc("GET /attachments/{taskID}/{filename}", srv.handleServeAttachment)
	mux.HandleFunc("POST /reorder", srv.handleReorder)

	// SSE.
	mux.HandleFunc("GET /events", srv.handleSSE)

	// Partials.
	mux.HandleFunc("GET /partials/task-row/{id}", srv.handlePartialTaskRow)
	mux.HandleFunc("GET /partials/task-list", srv.handlePartialTaskList)

	// PWA files (must be at root for browser discovery / service worker scope).
	// Read once at startup since embedded files are immutable.
	serveStatic := func(embedPath, contentType string) http.HandlerFunc {
		data, err := content.ReadFile(embedPath)
		if err != nil {
			log.Fatalf("embed missing %s: %v", embedPath, err)
		}
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Write(data)
		}
	}
	mux.HandleFunc("GET /manifest.json", serveStatic("static/manifest.json", "application/manifest+json"))
	mux.HandleFunc("GET /sw.js", serveStatic("static/sw.js", "application/javascript"))

	// Static files.
	mux.Handle("GET /static/", http.FileServerFS(content))

	// Wrap with recovery middleware so panics don't kill the server.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC %s %s: %v", r.Method, r.URL, err)
				http.Error(w, "internal server error", 500)
			}
		}()
		mux.ServeHTTP(w, r)
	})

	if tlsCert != "" && tlsKey != "" {
		return http.ListenAndServeTLS(addr, tlsCert, tlsKey, handler)
	}
	return http.ListenAndServe(addr, handler)
}

// hxRedirect sends an HX-Redirect for htmx requests, or a standard HTTP redirect otherwise.
func (s *Server) hxRedirect(w http.ResponseWriter, r *http.Request, dest string, code int) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", dest)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, dest, code)
}

// render executes a page template by name.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, 500)
		return
	}
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template %s: %v", page, err)
	}
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
	xtag := r.URL.Query().Get("xtag")

	// Look up workspace name for display.
	var wsName string
	if ws, err := s.db.GetWorkspace(path); err == nil && ws != nil {
		wsName = ws.Name
	}

	queue, err := s.db.ListTasks(path, model.StatusQueue, "", tag, xtag)
	if err != nil {
		log.Printf("ListTasks queue: %v", err)
	}
	inProgress, err := s.db.ListTasks(path, model.StatusInProgress, "", tag, xtag)
	if err != nil {
		log.Printf("ListTasks in_progress: %v", err)
	}
	backlog, err := s.db.ListTasks(path, model.StatusBacklog, "", tag, xtag)
	if err != nil {
		log.Printf("ListTasks backlog: %v", err)
	}

	var closed []model.Task
	if showClosed {
		closed, err = s.db.ListTasks(path, model.StatusClosed, "", tag, xtag)
		if err != nil {
			log.Printf("ListTasks closed: %v", err)
		}
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

	blockedIDs, err := s.db.BlockedTaskIDs(allIDs)
	if err != nil {
		log.Printf("BlockedTaskIDs: %v", err)
	}
	if blockedIDs == nil {
		blockedIDs = make(map[string]bool)
	}

	tagMap, err := s.db.GetTagsForTasks(allIDs)
	if err != nil {
		log.Printf("GetTagsForTasks: %v", err)
	}
	if tagMap == nil {
		tagMap = make(map[string][]string)
	}

	// Always fetch the full tag list — needed for the filter bar when
	// a filter is active (so filtered-out tags still appear clickable).
	allTags, err := s.db.ListAllTags(path)
	if err != nil {
		log.Printf("ListAllTags: %v", err)
	}

	// Tags from open tasks only — used for quick-add autocomplete so stale
	// tags from closed tasks don't clutter the suggestions.
	openTags := tagsForTasks(tagMap, queue, backlog, inProgress)

	// For the filter bar when unfiltered, derive from visible tasks to avoid
	// showing tags that only appear on closed/hidden tasks.
	filterBarTags := allTags
	if tag == "" && xtag == "" {
		filterBarTags = uniqueSortedTags(tagMap)
	}

	wsURL := workspaceURL(path, wsName)

	// Build URLs that preserve active tag/xtag filters.
	addTagParams := func(base string) string {
		if tag == "" && xtag == "" {
			return base
		}
		u, err := url.Parse(base)
		if err != nil {
			return base
		}
		q := u.Query()
		if tag != "" {
			q.Set("tag", tag)
		}
		if xtag != "" {
			q.Set("xtag", xtag)
		}
		u.RawQuery = q.Encode()
		return u.String()
	}

	filteredWsURL := addTagParams(wsURL)
	showClosedURL := wsURL
	if u, err := url.Parse(wsURL); err == nil {
		q := u.Query()
		q.Set("show_closed", "1")
		u.RawQuery = q.Encode()
		showClosedURL = addTagParams(u.String())
	}
	s.render(w, "workspace.html", map[string]any{
		"Path":           path,
		"Name":           wsName,
		"WorkspaceURL":   wsURL,
		"FilteredURL":    filteredWsURL,
		"ShowClosedURL":  showClosedURL,
		"Queue":        queue,
		"InProgress":   inProgress,
		"Backlog":      backlog,
		"Closed":       closed,
		"ShowClosed":   showClosed,
		"BlockedIDs":   blockedIDs,
		"TagMap":       tagMap,
		"TagFilter":    tagFilterData(filterBarTags, tag, xtag),
		"AllTags":      allTags,
		"OpenTags":     openTags,
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

	blockers, err := s.db.GetBlockers(id, false)
	if err != nil {
		log.Printf("GetBlockers %s: %v", id, err)
	}
	blocking, err := s.db.GetBlocking(id)
	if err != nil {
		log.Printf("GetBlocking %s: %v", id, err)
	}
	isBlocked := false
	for _, b := range blockers {
		if b.Status != model.StatusClosed {
			isBlocked = true
			break
		}
	}

	tags, err := s.db.GetTags(id)
	if err != nil {
		log.Printf("GetTags %s: %v", id, err)
	}
	allTags, err := s.db.ListAllTags(twc.Workspace)
	if err != nil {
		log.Printf("ListAllTags %s: %v", twc.Workspace, err)
	}
	attachments, err := s.db.ListAttachments(id)
	if err != nil {
		log.Printf("ListAttachments %s: %v", id, err)
	}

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
		"Attachments":   attachments,
	})
}

func (s *Server) handleEpicDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tag := r.URL.Query().Get("tag")
	xtag := r.URL.Query().Get("xtag")

	task, err := s.db.GetTask(id)
	if err != nil || !task.IsEpic {
		http.Error(w, "epic not found", 404)
		return
	}

	children, err := s.db.ListTasks("", "", id, tag, xtag)
	if err != nil {
		log.Printf("ListTasks epic %s: %v", id, err)
	}
	progress, err := s.db.EpicProgress(id)
	if err != nil {
		log.Printf("EpicProgress %s: %v", id, err)
	}
	comments, err := s.db.ListComments(id)
	if err != nil {
		log.Printf("ListComments %s: %v", id, err)
	}

	// Batch-load tags for children.
	childIDs := make([]string, len(children))
	for i, c := range children {
		childIDs[i] = c.ID
	}
	tagMap, err := s.db.GetTagsForTasks(childIDs)
	if err != nil {
		log.Printf("GetTagsForTasks epic %s: %v", id, err)
	}
	if tagMap == nil {
		tagMap = make(map[string][]string)
	}

	// Build filter bar tags from children (not just filtered subset).
	var childFilterTags []string
	if tag != "" || xtag != "" {
		childFilterTags, err = s.db.ListTagsForEpic(id)
		if err != nil {
			log.Printf("ListTagsForEpic %s: %v", id, err)
		}
	} else {
		childFilterTags = uniqueSortedTags(tagMap)
	}

	var wsName string
	if ws, err := s.db.GetWorkspace(task.Workspace); err == nil && ws != nil {
		wsName = ws.Name
	}

	// Load tags for the epic itself.
	epicTags, err := s.db.GetTags(id)
	if err != nil {
		log.Printf("GetTags epic %s: %v", id, err)
	}
	allTags, err := s.db.ListAllTags(task.Workspace)
	if err != nil {
		log.Printf("ListAllTags %s: %v", task.Workspace, err)
	}

	attachments, err := s.db.ListAttachments(id)
	if err != nil {
		log.Printf("ListAttachments epic %s: %v", id, err)
	}

	epicURL := "/epic/" + id
	s.render(w, "epic.html", map[string]any{
		"Epic":          task,
		"Children":      children,
		"Progress":      progress,
		"Comments":      comments,
		"WorkspaceName": wsName,
		"WorkspaceURL":  workspaceURL(task.Workspace, wsName),
		"TagMap":        tagMap,
		"TagFilter":     tagFilterData(childFilterTags, tag, xtag),
		"EpicURL":       epicURL,
		"Tags":          epicTags,
		"AllTags":       allTags,
		"Attachments":   attachments,
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
	for _, t := range db.SplitComma(r.FormValue("tags")) {
		s.db.AddTag(task.ID, t)
	}

	s.hub.Broadcast("task_created", task.Workspace, task.ID)

	// Redirect back to the referring page (preserving filters) or workspace root.
	dest := r.FormValue("redirect")
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		var wsName string
		if ws, err := s.db.GetWorkspace(task.Workspace); err == nil && ws != nil {
			wsName = ws.Name
		}
		dest = workspaceURL(task.Workspace, wsName)
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()

	// Build update params from form.
	var pTitle, pBody *string
	if title := r.FormValue("title"); title != "" {
		pTitle = &title
	}
	if r.Form.Has("body") {
		body := r.FormValue("body")
		pBody = &body
	}

	if pTitle == nil && pBody == nil {
		http.Error(w, "no fields to update", 400)
		return
	}

	// Reject body/description updates for epics — use spec instead.
	if pBody != nil {
		existing, err := s.db.GetTask(id)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		if existing.IsEpic {
			http.Error(w, "use spec for epics, not description", 400)
			return
		}
	}

	task, err := s.db.UpdateTask(id, pTitle, pBody, nil)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}

	s.hub.Broadcast("task_updated", task.Workspace, id)

	if r.Header.Get("HX-Request") == "true" {
		s.partials.ExecuteTemplate(w, "task_row.html", task)
		return
	}
	dest := "/task/" + id
	if task.IsEpic {
		dest = "/epic/" + id
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
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

	dest := "/task/" + id
	if task.IsEpic {
		dest = "/epic/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleReopenTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.ReopenTask(id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("task_updated", task.Workspace, id)

	dest := "/task/" + id
	if task.IsEpic {
		dest = "/epic/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handlePromoteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.PromoteToEpic(id, "")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("epic_promoted", task.Workspace, id)
	s.hxRedirect(w, r, "/epic/"+id, http.StatusSeeOther)
}

func (s *Server) handleDemoteEpic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.DemoteToTask(id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("task_updated", task.Workspace, id)
	s.hxRedirect(w, r, "/task/"+id, http.StatusSeeOther)
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

	task, err := s.db.GetTask(id)
	if err != nil {
		log.Printf("GetTask %s: %v", id, err)
	}
	if task != nil {
		s.hub.Broadcast("comment_added", task.Workspace, id)
	}

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

	// Validate task is an epic.
	existing, err := s.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if !existing.IsEpic {
		http.Error(w, "spec is only for epics", 400)
		return
	}

	if _, err := s.db.UpdateTask(id, nil, nil, &spec); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hxRedirect(w, r, "/epic/"+id, http.StatusSeeOther)
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

	task, err := s.db.GetTask(id)
	if err != nil {
		log.Printf("GetTask %s: %v", id, err)
	}
	if task != nil {
		s.hub.Broadcast("task_updated", task.Workspace, id)
	}

	s.hxRedirect(w, r, "/task/"+id, http.StatusSeeOther)
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

	task, err := s.db.GetTask(id)
	if err != nil {
		log.Printf("GetTask %s: %v", id, err)
	}
	if task != nil {
		s.hub.Broadcast("task_updated", task.Workspace, id)
	}

	s.hxRedirect(w, r, "/task/"+id, http.StatusSeeOther)
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

	task, err := s.db.GetTask(id)
	if err != nil {
		log.Printf("GetTask %s: %v", id, err)
	}
	if task != nil {
		s.hub.Broadcast("task_updated", task.Workspace, id)
	}

	s.hxRedirect(w, r, s.tagRedirect(id, task, r), http.StatusSeeOther)
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

	task, err := s.db.GetTask(id)
	if err != nil {
		log.Printf("GetTask %s: %v", id, err)
	}
	if task != nil {
		s.hub.Broadcast("task_updated", task.Workspace, id)
	}

	s.hxRedirect(w, r, s.tagRedirect(id, task, r), http.StatusSeeOther)
}

// tagRedirect returns the appropriate redirect URL after a tag mutation.
// Uses the "redirect" form parameter if set, otherwise falls back to task/epic based on type.
func (s *Server) tagRedirect(id string, task *model.Task, r *http.Request) string {
	if dest := r.FormValue("redirect"); dest != "" && strings.HasPrefix(dest, "/") && !strings.HasPrefix(dest, "//") {
		return dest
	}
	if task != nil && task.IsEpic {
		return "/epic/" + id
	}
	return "/task/" + id
}

// allowedMIME is the set of allowed upload MIME types.
var allowedMIME = map[string]bool{
	"image/png":     true,
	"image/jpeg":    true,
	"image/gif":     true,
	"image/svg+xml": true,
	"image/webp":    true,
	"application/pdf": true,
	"text/plain":      true,
	"text/markdown":   true,
}

// sanitizeFilename returns a safe filename, stripping path traversal and null bytes.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\x00", "")
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	return name
}

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Enforce 10 MB limit.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20+1024) // extra 1KB for multipart headers
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "file too large (max 10 MB)", 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required", 400)
		return
	}
	defer file.Close()

	// Read first 512 bytes for content detection.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	detected := http.DetectContentType(buf[:n])
	// Reset reader.
	file.Seek(0, io.SeekStart)

	// For SVG, DetectContentType returns text/xml or text/plain. Check extension too.
	if strings.HasSuffix(strings.ToLower(header.Filename), ".svg") {
		detected = "image/svg+xml"
	}
	// For markdown, DetectContentType returns text/plain. Check extension.
	if strings.HasSuffix(strings.ToLower(header.Filename), ".md") {
		detected = "text/markdown"
	}

	if !allowedMIME[detected] {
		http.Error(w, fmt.Sprintf("file type %s not allowed", detected), 400)
		return
	}

	filename := sanitizeFilename(header.Filename)

	// Create DB record (storedName = id + "_" + filename, computed internally).
	att, err := s.db.CreateAttachment(id, filename, detected, header.Size)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Write file to disk.
	taskDir := filepath.Join(s.attachmentsDir, id)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		s.db.DeleteAttachment(att.ID)
		http.Error(w, "failed to create attachment directory", 500)
		return
	}

	filePath := filepath.Join(taskDir, att.StoredName)
	dst, err := os.Create(filePath)
	if err != nil {
		s.db.DeleteAttachment(att.ID)
		http.Error(w, "failed to write file", 500)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(filePath)
		s.db.DeleteAttachment(att.ID)
		http.Error(w, "failed to write file", 500)
		return
	}

	// Update size_bytes with actual bytes written.
	if written != header.Size {
		s.db.Exec(`UPDATE attachments SET size_bytes = ? WHERE id = ?`, written, att.ID)
	}

	task, _ := s.db.GetTask(id)
	if task != nil {
		s.hub.Broadcast("task_updated", task.Workspace, id)
	}

	dest := "/task/" + id
	if task != nil && task.IsEpic {
		dest = "/epic/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	attID := r.FormValue("attachment_id")
	if attID == "" {
		http.Error(w, "attachment_id required", 400)
		return
	}

	att, err := s.db.GetAttachment(attID)
	if err != nil {
		http.Error(w, "attachment not found", 404)
		return
	}

	// Verify attachment belongs to this task.
	if att.TaskID != id {
		http.Error(w, "attachment does not belong to this task", 400)
		return
	}

	// Remove from disk.
	os.Remove(filepath.Join(s.attachmentsDir, att.TaskID, att.StoredName))

	// Remove from DB.
	if err := s.db.DeleteAttachment(attID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	task, _ := s.db.GetTask(id)
	if task != nil {
		s.hub.Broadcast("task_updated", task.Workspace, id)
	}

	dest := "/task/" + id
	if task != nil && task.IsEpic {
		dest = "/epic/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleServeAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	filename := r.PathValue("filename")

	// Sanitize to prevent path traversal.
	filename = filepath.Base(filename)
	filePath := filepath.Join(s.attachmentsDir, taskID, filename)

	// Detect MIME from extension for Content-Type header.
	ext := filepath.Ext(filename)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// SVG: force download to prevent script execution.
	if strings.HasSuffix(strings.ToLower(filename), ".svg") {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	} else if !strings.HasPrefix(mimeType, "image/") {
		// Non-images: suggest download.
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	}

	http.ServeFile(w, r, filePath)
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

	task, err := s.db.GetTask(id)
	if err != nil {
		log.Printf("GetTask %s: %v", id, err)
	}
	if task != nil {
		s.hub.Broadcast("task_reordered", task.Workspace, id)
	}
	if dest := r.FormValue("redirect"); dest != "" {
		s.hxRedirect(w, r, dest, http.StatusSeeOther)
		return
	}
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

	tasks, err := s.db.ListTasks(workspace, status, "", "", "")
	if err != nil {
		log.Printf("ListTasks partial: %v", err)
	}
	s.partials.ExecuteTemplate(w, "task_list.html", tasks)
}
