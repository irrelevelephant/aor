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
	"strconv"
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

// SSE hub for broadcasting events to all subscribed clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

func newHub() *Hub {
	return &Hub{clients: make(map[chan string]struct{})}
}

func (h *Hub) Subscribe() chan string {
	ch := make(chan string, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *Hub) Broadcast(event, data string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// Drop if client is slow.
		}
	}
}

// SSEBroadcaster is an interface for broadcasting SSE events.
// When running standalone (ata serve), the built-in Hub is used.
// When running unified (aor serve), the shared hub is injected.
type SSEBroadcaster interface {
	Broadcast(event, data string)
}

// DispatchFunc runs an ata command in-process. Matches cmd.Dispatch signature.
type DispatchFunc func(d *db.DB, subcmd string, args []string) error

// Option configures the web server.
type Option func(*Server)

// WithDispatch sets the command dispatch function for the exec API.
func WithDispatch(fn DispatchFunc) Option {
	return func(s *Server) { s.dispatch = fn }
}

// WithSSE sets the SSE broadcaster (for unified server mode).
func WithSSE(b SSEBroadcaster) Option {
	return func(s *Server) { s.hub = b }
}

type Server struct {
	db             *db.DB
	hub            SSEBroadcaster
	localHub       *Hub // only set for standalone (ata serve) mode
	dispatch       DispatchFunc
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

// treeCount returns the total number of tasks in a tree (all levels).
func treeCount(nodes []model.TaskTreeNode) int {
	n := len(nodes)
	for _, node := range nodes {
		n += treeCount(node.Children)
	}
	return n
}

func hasEpicGroup(nodes []model.TaskTreeNode) bool {
	for _, n := range nodes {
		if n.IsEpicGroup() {
			return true
		}
	}
	return false
}

// flattenTree extracts all tasks from tree nodes into a flat slice (all levels).
func flattenTree(nodes []model.TaskTreeNode) []model.Task {
	var tasks []model.Task
	for _, n := range nodes {
		tasks = append(tasks, n.Task)
		tasks = append(tasks, flattenTree(n.Children)...)
	}
	return tasks
}

// tagPalette contains well-spaced hues that avoid 250-310 (purple, used by epics).
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
		"AllTags":    allTags,
		"IncludeSet": tagSet(inc),
		"ExcludeSet": tagSet(exc),
		"HasFilters": len(inc) > 0 || len(exc) > 0,
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

// initServer creates and configures a Server with templates and options.
func initServer(d *db.DB, opts ...Option) (*Server, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(goldhtml.WithHardWraps()),
	)

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
		"tagColor": func(tag string) template.CSS {
			return template.CSS(fmt.Sprintf("hsl(%d, 70%%, 75%%)", tagHue(tag)))
		},
		"tagBgColor": func(tag string) template.CSS {
			return template.CSS(fmt.Sprintf("hsl(%d, 50%%, 18%%)", tagHue(tag)))
		},
		"tagFilterURL": func(baseURL, tag string) string {
			return setTagQuery(baseURL, "tag", tag)
		},
		"formatBytes":  db.FormatBytes,
		"taskURL":      taskURL,
		"hasEpicGroup": hasEpicGroup,
	}

	pageFiles := []string{"tasks.html", "task.html", "epic.html"}
	pages := make(map[string]*template.Template, len(pageFiles))
	sharedFiles := []string{"templates/layout.html", "templates/partials/task_row.html", "templates/partials/task_list.html", "templates/partials/comment.html", "templates/partials/tag_filter_bar.html", "templates/partials/task_tags_inline.html", "templates/partials/epic_group.html", "templates/partials/deps_section.html"}
	for _, page := range pageFiles {
		t, err := template.New("").Funcs(funcMap).ParseFS(content, append(sharedFiles, "templates/"+page)...)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		pages[page] = t
	}

	partials, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}

	attDir, _ := db.AttachmentsDir()

	localHub := newHub()
	srv := &Server{
		db:             d,
		hub:            localHub,
		localHub:       localHub,
		pages:          pages,
		partials:       partials,
		md:             md,
		attachmentsDir: attDir,
	}
	for _, o := range opts {
		o(srv)
	}

	return srv, nil
}

// registerAtaRoutes registers all ata HTTP routes on the given mux.
func (srv *Server) registerAtaRoutes(mux *http.ServeMux) {
	// Pages.
	mux.HandleFunc("GET /", srv.handleTasks)
	mux.HandleFunc("GET /task/{id}", srv.handleTaskDetail)
	mux.HandleFunc("GET /epic/{id}", srv.handleEpicDetail)

	// Task mutations.
	mux.HandleFunc("POST /task", srv.handleCreateTask)
	mux.HandleFunc("POST /task/{id}", srv.handleUpdateTask)
	mux.HandleFunc("POST /task/{id}/move", srv.handleMoveTask)
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
	mux.HandleFunc("POST /epic/{id}/children", srv.handleAddChild)
	mux.HandleFunc("GET /attachments/{taskID}/{filename}", srv.handleServeAttachment)
	mux.HandleFunc("POST /reorder", srv.handleReorder)

	// API.
	mux.HandleFunc("POST /api/v1/exec", srv.handleAPIExec)

	// Partials.
	mux.HandleFunc("GET /partials/task-row/{id}", srv.handlePartialTaskRow)
	mux.HandleFunc("GET /partials/task-list", srv.handlePartialTaskList)

	// PWA files (must be at root for browser discovery / service worker scope).
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
}

// RegisterRoutes creates a Server and registers all ata routes on the given mux.
// Used by the unified aor serve command.
func RegisterRoutes(mux *http.ServeMux, d *db.DB, opts ...Option) *Server {
	srv, err := initServer(d, opts...)
	if err != nil {
		log.Fatalf("init ata server: %v", err)
	}
	srv.registerAtaRoutes(mux)
	return srv
}

// Serve starts the ata web server standalone (used by ata serve).
func Serve(d *db.DB, addr, tlsCert, tlsKey string, opts ...Option) error {
	srv, err := initServer(d, opts...)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	srv.registerAtaRoutes(mux)

	// SSE (standalone only -- unified server provides its own).
	mux.HandleFunc("GET /events", srv.handleSSE)

	// Wrap with recovery middleware.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC %s %s: %v", r.Method, r.URL, err)
				http.Error(w, "internal server error", 500)
			}
		}()
		if r.Method == "POST" && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
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

func (s *Server) parentEpicTitle(epicID string) string {
	if epicID == "" {
		return ""
	}
	parent, err := s.db.GetTask(epicID)
	if err != nil {
		log.Printf("GetTask parent epic %s: %v", epicID, err)
		return ""
	}
	return parent.Title
}

// --- Page handlers ---

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	showClosed := r.URL.Query().Get("show_closed") == "1"
	tag := r.URL.Query().Get("tag")
	xtag := r.URL.Query().Get("xtag")

	queue, err := s.db.ListTaskTree(model.StatusQueue, tag, xtag)
	if err != nil {
		log.Printf("ListTaskTree queue: %v", err)
	}
	inProgress, err := s.db.ListTasks(model.StatusInProgress, "", tag, xtag)
	if err != nil {
		log.Printf("ListTasks in_progress: %v", err)
	}
	backlog, err := s.db.ListTaskTree(model.StatusBacklog, tag, xtag)
	if err != nil {
		log.Printf("ListTaskTree backlog: %v", err)
	}

	var closed []model.Task
	if showClosed {
		closed, err = s.db.ListTasks(model.StatusClosed, "", tag, xtag)
		if err != nil {
			log.Printf("ListTasks closed: %v", err)
		}
	}

	allOpen := append(flattenTree(queue), flattenTree(backlog)...)
	allOpen = append(allOpen, inProgress...)
	allVisible := append(allOpen, closed...)
	allIDs := make([]string, len(allVisible))
	tagMap := make(map[string][]string, len(allVisible))
	for i, t := range allVisible {
		allIDs[i] = t.ID
		if len(t.Tags) > 0 {
			tagMap[t.ID] = t.Tags
		}
	}

	blockedIDs, err := s.db.BlockedTaskIDs(allIDs)
	if err != nil {
		log.Printf("BlockedTaskIDs: %v", err)
	}
	if blockedIDs == nil {
		blockedIDs = make(map[string]bool)
	}

	allTags, err := s.db.ListAllTags()
	if err != nil {
		log.Printf("ListAllTags: %v", err)
	}

	openTags := tagsForTasks(tagMap, allOpen)

	filterBarTags := openTags
	if showClosed {
		filterBarTags = allTags
	}

	baseURL := "/"

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

	filteredURL := addTagParams(baseURL)
	showClosedURL := baseURL
	if u, err := url.Parse(baseURL); err == nil {
		q := u.Query()
		q.Set("show_closed", "1")
		u.RawQuery = q.Encode()
		showClosedURL = addTagParams(u.String())
	}
	s.render(w, "tasks.html", map[string]any{
		"BaseURL":       baseURL,
		"FilteredURL":   filteredURL,
		"ShowClosedURL": showClosedURL,
		"Queue":         queue,
		"QueueCount":    treeCount(queue),
		"InProgress":    inProgress,
		"Backlog":       backlog,
		"BacklogCount":  treeCount(backlog),
		"Closed":        closed,
		"ShowClosed":    showClosed,
		"BlockedIDs":    blockedIDs,
		"TagMap":        tagMap,
		"TagFilter":     tagFilterData(filterBarTags, tag, xtag),
		"AllTags":       allTags,
		"OpenTags":      openTags,
	})
}

func taskURL(isEpic bool, id string) string {
	if isEpic {
		return "/epic/" + id
	}
	return "/task/" + id
}

func taskDetailURL(task *model.Task, id string) string {
	return taskURL(task != nil && task.IsEpic, id)
}

func (s *Server) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := s.db.GetTask(id)
	if err != nil {
		http.Error(w, "task not found", 404)
		return
	}

	if task.IsEpic {
		http.Redirect(w, r, "/epic/"+id, http.StatusSeeOther)
		return
	}

	comments, err := s.db.ListComments(id)
	if err != nil {
		log.Printf("ListComments %s: %v", id, err)
	}
	twc := &model.TaskWithComments{Task: *task, Comments: comments}

	epicTitle := s.parentEpicTitle(twc.EpicID)

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
	allTags, err := s.db.ListAllTags()
	if err != nil {
		log.Printf("ListAllTags: %v", err)
	}
	attachments, err := s.db.ListAttachments(id)
	if err != nil {
		log.Printf("ListAttachments %s: %v", id, err)
	}

	s.render(w, "task.html", map[string]any{
		"Task":        twc,
		"EpicTitle":   epicTitle,
		"IsBlocked":   isBlocked,
		"Tags":        tags,
		"AllTags":     allTags,
		"Attachments": attachments,
		"DepsData": map[string]any{
			"TaskID":   id,
			"Status":   twc.Status,
			"Blockers": blockers,
			"Blocking": blocking,
		},
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

	childTasks, err := s.db.ListTasks("", id, tag, xtag)
	if err != nil {
		log.Printf("ListTasks epic %s: %v", id, err)
	}

	childrenTree := model.BuildTree(id, childTasks)

	progress, err := s.db.EpicProgress(id)
	if err != nil {
		log.Printf("EpicProgress %s: %v", id, err)
	}
	comments, err := s.db.ListComments(id)
	if err != nil {
		log.Printf("ListComments %s: %v", id, err)
	}

	childIDs := make([]string, len(childTasks))
	tagMap := make(map[string][]string, len(childTasks))
	for i, c := range childTasks {
		childIDs[i] = c.ID
		if len(c.Tags) > 0 {
			tagMap[c.ID] = c.Tags
		}
	}

	blockedIDs, err := s.db.BlockedTaskIDs(childIDs)
	if err != nil {
		log.Printf("BlockedTaskIDs epic %s: %v", id, err)
	}
	if blockedIDs == nil {
		blockedIDs = make(map[string]bool)
	}

	var childFilterTags []string
	if tag != "" || xtag != "" {
		childFilterTags, err = s.db.ListTagsForEpic(id)
		if err != nil {
			log.Printf("ListTagsForEpic %s: %v", id, err)
		}
	} else {
		childFilterTags = uniqueSortedTags(tagMap)
	}

	epicTags, err := s.db.GetTags(id)
	if err != nil {
		log.Printf("GetTags epic %s: %v", id, err)
	}
	allTags, err := s.db.ListAllTags()
	if err != nil {
		log.Printf("ListAllTags: %v", err)
	}

	attachments, err := s.db.ListAttachments(id)
	if err != nil {
		log.Printf("ListAttachments epic %s: %v", id, err)
	}

	blockers, err := s.db.GetBlockers(id, false)
	if err != nil {
		log.Printf("GetBlockers epic %s: %v", id, err)
	}
	blocking, err := s.db.GetBlocking(id)
	if err != nil {
		log.Printf("GetBlocking epic %s: %v", id, err)
	}
	isBlocked := false
	for _, b := range blockers {
		if b.Status != model.StatusClosed {
			isBlocked = true
			break
		}
	}

	epicTitle := s.parentEpicTitle(task.EpicID)

	epicURL := "/epic/" + id
	s.render(w, "epic.html", map[string]any{
		"Epic":        task,
		"Children":    childrenTree,
		"BlockedIDs":  blockedIDs,
		"Progress":    progress,
		"Comments":    comments,
		"TagMap":      tagMap,
		"TagFilter":   tagFilterData(childFilterTags, tag, xtag),
		"EpicURL":     epicURL,
		"Tags":        epicTags,
		"AllTags":     allTags,
		"Attachments": attachments,
		"EpicTitle":   epicTitle,
		"IsBlocked":   isBlocked,
		"DepsData": map[string]any{
			"TaskID":   id,
			"Status":   task.Status,
			"Blockers": blockers,
			"Blocking": blocking,
		},
	})
}

// --- Mutation handlers ---

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

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

	task, err := s.db.CreateTask(title, body, r.FormValue("status"), r.FormValue("epic_id"), "")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	for _, t := range db.SplitComma(r.FormValue("tags")) {
		s.db.AddTag(task.ID, t)
	}

	s.hub.Broadcast("task_created", task.ID)

	dest := r.FormValue("redirect")
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") {
		dest = "/"
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()

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

	s.hub.Broadcast("task_updated", id)

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

func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	to := r.FormValue("to")
	if to != model.StatusQueue && to != model.StatusBacklog {
		http.Error(w, "invalid target status", 400)
		return
	}
	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		http.Error(w, "task not found", 404)
		return
	}
	if task.IsEpic {
		err = s.db.MoveEpicTree(id, to)
	} else {
		_, err = s.db.MoveTasks([]string{id}, "", to)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.hub.Broadcast("task_updated", id)
	dest := r.FormValue("redirect")
	if dest == "" {
		dest = "/task/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
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

	s.hub.Broadcast("task_closed", id)

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

	s.hub.Broadcast("task_updated", id)

	dest := "/task/" + id
	if task.IsEpic {
		dest = "/epic/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handlePromoteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.db.PromoteToEpic(id, "")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("epic_promoted", id)
	s.hxRedirect(w, r, "/epic/"+id, http.StatusSeeOther)
}

func (s *Server) handleDemoteEpic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.db.DemoteToTask(id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.hub.Broadcast("task_updated", id)
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

	s.hub.Broadcast("comment_added", id)

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
	s.hub.Broadcast("task_updated", id)

	if r.Header.Get("HX-Request") == "true" {
		s.renderDepsSection(w, id, task)
		return
	}
	http.Redirect(w, r, taskDetailURL(task, id), http.StatusSeeOther)
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
	s.hub.Broadcast("task_updated", id)

	if r.Header.Get("HX-Request") == "true" {
		s.renderDepsSection(w, id, task)
		return
	}
	http.Redirect(w, r, taskDetailURL(task, id), http.StatusSeeOther)
}

func (s *Server) renderDepsSection(w http.ResponseWriter, id string, task *model.Task) {
	blockers, err := s.db.GetBlockers(id, false)
	if err != nil {
		log.Printf("GetBlockers %s: %v", id, err)
	}
	blocking, err := s.db.GetBlocking(id)
	if err != nil {
		log.Printf("GetBlocking %s: %v", id, err)
	}
	status := ""
	if task != nil {
		status = task.Status
	}
	s.partials.ExecuteTemplate(w, "deps_section.html", map[string]any{
		"TaskID":   id,
		"Status":   status,
		"Blockers": blockers,
		"Blocking": blocking,
	})
}

func (s *Server) handleAddChild(w http.ResponseWriter, r *http.Request) {
	epicID := r.PathValue("id")
	r.ParseForm()
	childID := strings.TrimSpace(r.FormValue("child_id"))
	if childID == "" {
		http.Error(w, "child_id required", 400)
		return
	}
	if err := s.db.SetEpicID(childID, epicID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.hub.Broadcast("task_updated", epicID)
	s.hxRedirect(w, r, "/epic/"+epicID, http.StatusSeeOther)
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
	s.hub.Broadcast("task_updated", id)

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
	s.hub.Broadcast("task_updated", id)

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
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/svg+xml":   true,
	"image/webp":      true,
	"application/pdf": true,
	"text/plain":      true,
	"text/markdown":   true,
}

// headerUnsafe strips characters that could break HTTP headers or enable path traversal.
var headerUnsafe = strings.NewReplacer("\x00", "", "\"", "", "\r", "", "\n", "")

// sanitizeFilename returns a safe filename, stripping path traversal, null bytes,
// and characters that could break HTTP headers (quotes, CR, LF).
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = headerUnsafe.Replace(name)
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	return name
}

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20+1024)
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

	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	detected := http.DetectContentType(buf[:n])
	file.Seek(0, io.SeekStart)

	if strings.HasSuffix(strings.ToLower(header.Filename), ".svg") {
		detected = "image/svg+xml"
	}
	if strings.HasSuffix(strings.ToLower(header.Filename), ".md") {
		detected = "text/markdown"
	}

	if !allowedMIME[detected] {
		http.Error(w, fmt.Sprintf("file type %s not allowed", detected), 400)
		return
	}

	filename := sanitizeFilename(header.Filename)

	att, err := s.db.CreateAttachment(id, filename, detected, header.Size)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

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

	if written != header.Size {
		if _, err := s.db.Exec(`UPDATE attachments SET size_bytes = ? WHERE id = ?`, written, att.ID); err != nil {
			log.Printf("update attachment size %s: %v", att.ID, err)
		}
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_updated", id)

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

	if att.TaskID != id {
		http.Error(w, "attachment does not belong to this task", 400)
		return
	}

	os.Remove(filepath.Join(s.attachmentsDir, att.TaskID, att.StoredName))

	if err := s.db.DeleteAttachment(attID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	task, _ := s.db.GetTask(id)
	s.hub.Broadcast("task_updated", id)

	dest := "/task/" + id
	if task != nil && task.IsEpic {
		dest = "/epic/" + id
	}
	s.hxRedirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) handleServeAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	filename := r.PathValue("filename")

	filename = filepath.Base(filename)
	filePath := filepath.Join(s.attachmentsDir, taskID, filename)

	ext := filepath.Ext(filename)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	if strings.HasSuffix(strings.ToLower(filename), ".svg") {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Security-Policy", "sandbox")
	} else if !strings.HasPrefix(mimeType, "image/") {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	}

	http.ServeFile(w, r, filePath)
}

func (s *Server) handleReorder(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.FormValue("id")
	posStr := r.FormValue("position")
	newStatus := r.FormValue("status")
	parentID := r.FormValue("parent")
	oldParentID := r.FormValue("oldParent")
	pos, err := strconv.Atoi(posStr)
	if err != nil {
		http.Error(w, "invalid position", 400)
		return
	}

	epicChanged := oldParentID != parentID

	if epicChanged {
		if err := s.db.SetEpicID(id, parentID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if newStatus != "" {
			if _, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE id = ? AND status != ?`, newStatus, id, newStatus); err != nil {
				log.Printf("update status for reparent: %v", err)
			}
		}
		if parentID != "" {
			err = s.db.ReorderInEpic(id, pos, parentID)
		} else {
			err = s.db.Reorder(id, pos, newStatus)
		}
	} else if parentID != "" {
		err = s.db.ReorderInEpic(id, pos, parentID)
	} else {
		if newStatus != "" {
			task, tErr := s.db.GetTask(id)
			if tErr != nil {
				http.Error(w, tErr.Error(), 500)
				return
			}
			if task != nil && task.IsEpic {
				if mErr := s.db.MoveEpicTree(id, newStatus); mErr != nil {
					http.Error(w, mErr.Error(), 500)
					return
				}
			}
		}
		err = s.db.Reorder(id, pos, newStatus)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	s.hub.Broadcast("task_reordered", id)
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

	if s.localHub == nil {
		http.Error(w, "SSE not available (use unified server)", 500)
		return
	}

	ch := s.localHub.Subscribe()
	defer s.localHub.Unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

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
	status := r.URL.Query().Get("status")

	tasks, err := s.db.ListTasks(status, "", "", "")
	if err != nil {
		log.Printf("ListTasks partial: %v", err)
	}
	s.partials.ExecuteTemplate(w, "task_list.html", tasks)
}
