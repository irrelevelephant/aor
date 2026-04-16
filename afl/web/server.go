package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"aor/afl/api"
	"aor/afl/db"
	"aor/afl/model"
)

//go:embed templates/*.html
var content embed.FS

// SSEBroadcaster is an interface for broadcasting SSE events.
// The unified server provides the implementation.
type SSEBroadcaster interface {
	Broadcast(event, data string)
}

// noopBroadcaster is used when no SSE hub is configured.
type noopBroadcaster struct{}

func (noopBroadcaster) Broadcast(string, string) {}

// DispatchFunc runs an afl command in-process.
type DispatchFunc func(d *db.DB, subcmd string, args []string) error

// Option configures the afl web server.
type Option func(*Server)

// WithDispatch sets the command dispatch function for the exec API.
func WithDispatch(fn DispatchFunc) Option {
	return func(s *Server) { s.dispatch = fn }
}

// WithSSE sets the SSE broadcaster.
func WithSSE(b SSEBroadcaster) Option {
	return func(s *Server) { s.sse = b }
}

// Server handles afl web UI and API requests.
type Server struct {
	db             *db.DB
	dispatch       DispatchFunc
	sse            SSEBroadcaster
	pages          map[string]*template.Template
	screenshotsDir string
}

// RegisterRoutes registers all afl routes on the given mux under /afl/ prefix.
func RegisterRoutes(mux *http.ServeMux, d *db.DB, opts ...Option) *Server {
	funcMap := template.FuncMap{
		"urlquery": func(s string) template.URL {
			return template.URL(url.QueryEscape(s))
		},
		"json": func(v any) template.HTML {
			b, _ := json.Marshal(v)
			return template.HTML(b)
		},
		"toFloat": func(i int) float64 {
			return float64(i)
		},
		"div": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"mul": func(a, b float64) float64 {
			return a * b
		},
	}

	pageFiles := []string{"dashboard.html", "domain.html", "flow.html"}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, page := range pageFiles {
		t, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html", "templates/"+page)
		if err != nil {
			log.Fatalf("parse afl template %s: %v", page, err)
		}
		pages[page] = t
	}

	ssDir, _ := db.ScreenshotsDir()

	srv := &Server{
		db:             d,
		sse:            noopBroadcaster{},
		pages:          pages,
		screenshotsDir: ssDir,
	}
	for _, o := range opts {
		o(srv)
	}

	// Pages.
	mux.HandleFunc("GET /afl/", srv.handleDashboard)
	mux.HandleFunc("GET /afl/d/{id}", srv.handleDomain)
	mux.HandleFunc("GET /afl/f/{id}", srv.handleFlow)

	// Screenshots.
	mux.HandleFunc("GET /afl/screenshots/{id}", srv.handleScreenshot)

	// API.
	mux.HandleFunc("POST /api/v1/afl/exec", srv.handleAPIExec)

	return srv
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, 500)
		return
	}
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("afl template %s: %v", page, err)
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/afl/" {
		http.NotFound(w, r)
		return
	}

	domains, err := s.db.DomainCoverage()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var totalFlows, totalCovered int
	for _, dc := range domains {
		totalFlows += dc.TotalFlows
		totalCovered += dc.CoveredFlows
	}

	var coveragePercent float64
	if totalFlows > 0 {
		coveragePercent = float64(totalCovered) / float64(totalFlows) * 100
	}

	s.render(w, "dashboard.html", map[string]any{
		"Domains":         domains,
		"TotalFlows":      totalFlows,
		"TotalCovered":    totalCovered,
		"CoveragePercent": coveragePercent,
	})
}

func (s *Server) handleDomain(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")

	dom, err := s.db.GetDomain(domainID)
	if err != nil {
		http.Error(w, "domain not found", 404)
		return
	}

	flowCoverages, err := s.db.DomainFlowsCoverage(dom.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	s.render(w, "domain.html", map[string]any{
		"Domain":    dom,
		"Flows":     flowCoverages,
		"Platforms": model.ValidPlatforms,
	})
}

func (s *Server) handleFlow(w http.ResponseWriter, r *http.Request) {
	flowID := r.PathValue("id")

	flow, err := s.db.GetFlow(flowID)
	if err != nil {
		http.Error(w, "flow not found", 404)
		return
	}

	dom, err := s.db.GetDomain(flow.DomainID)
	if err != nil {
		http.Error(w, "domain not found", 500)
		return
	}

	paths, err := s.db.ListPaths(flow.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Build path coverage data.
	type pathWithSteps struct {
		Path  model.Path
		Steps []model.Step
	}
	var pathData []pathWithSteps
	for _, p := range paths {
		steps, _ := s.db.ListSteps(p.ID)
		pathData = append(pathData, pathWithSteps{Path: p, Steps: steps})
	}

	// Determine active path.
	activePathID := r.URL.Query().Get("path")
	var activePath *model.Path
	var activeSteps []model.Step
	for _, pd := range pathData {
		if activePathID == "" || pd.Path.ID == activePathID {
			activePath = &pd.Path
			activeSteps = pd.Steps
			break
		}
	}
	if activePath == nil && len(pathData) > 0 {
		activePath = &pathData[0].Path
		activeSteps = pathData[0].Steps
	}

	// Build screenshot lookup: stepID -> platform -> screenshot.
	screenshotMap := make(map[string]map[string]*model.Screenshot)
	type screenshotJS struct {
		Src      string `json:"src"`
		Platform string `json:"platform"`
		Step     int    `json:"step"`
		Source   string `json:"source"`
	}
	screenshotJSData := make(map[string][]screenshotJS)

	if activePath != nil {
		screenshots, _ := s.db.ListScreenshotsForPath(activePath.ID)
		for i := range screenshots {
			ss := &screenshots[i]
			if screenshotMap[ss.StepID] == nil {
				screenshotMap[ss.StepID] = make(map[string]*model.Screenshot)
			}
			screenshotMap[ss.StepID][ss.Platform] = ss
		}

		// Build JS data for lightbox navigation.
		for _, step := range activeSteps {
			var imgs []screenshotJS
			for _, platform := range model.ValidPlatforms {
				if ss, ok := screenshotMap[step.ID][platform]; ok {
					imgs = append(imgs, screenshotJS{
						Src:      fmt.Sprintf("/afl/screenshots/%s", ss.ID),
						Platform: platform,
						Step:     step.SortOrder,
						Source:   ss.CaptureSource,
					})
				}
			}
			if len(imgs) > 0 {
				screenshotJSData[step.ID] = imgs
			}
		}
	}

	var activePathData map[string]any
	if activePath != nil {
		activePathData = map[string]any{
			"Steps":        activeSteps,
			"ScreenshotJS": screenshotJSData,
		}
	}

	s.render(w, "flow.html", map[string]any{
		"Flow":           flow,
		"DomainID":       dom.ID,
		"DomainName":     dom.Name,
		"Platforms":      model.ValidPlatforms,
		"Paths":          pathData,
		"ActivePath":     activePath,
		"ActivePathData": activePathData,
		"ScreenshotMap":  screenshotMap,
	})
}

func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	ss, err := s.db.GetScreenshotWithPath(id)
	if err != nil {
		http.Error(w, "screenshot not found", 500)
		return
	}
	if ss == nil {
		http.Error(w, "screenshot not found", 404)
		return
	}

	filePath := filepath.Join(s.screenshotsDir, ss.FlowID, ss.StepID, ss.Platform, ss.StoredName)
	w.Header().Set("Content-Type", ss.MimeType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filePath)
}

// execMu serializes API exec calls since we redirect os.Stdout/os.Stderr.
var execMu sync.Mutex

func (s *Server) handleAPIExec(w http.ResponseWriter, r *http.Request) {
	if s.dispatch == nil {
		http.Error(w, "exec API not configured", http.StatusServiceUnavailable)
		return
	}

	var req api.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.ExecResponse{
			ExitCode: 1,
			Stderr:   "invalid request: " + err.Error(),
		})
		return
	}

	stdout, stderr, exitCode := s.execCommand(req.Command, req.Args)
	s.sse.Broadcast("afl_updated", "api")

	writeJSON(w, http.StatusOK, api.ExecResponse{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	})
}

func (s *Server) execCommand(command string, args []string) (stdout, stderr string, exitCode int) {
	execMu.Lock()
	defer execMu.Unlock()

	oldStdout := os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		return "", "pipe error: " + err.Error(), 1
	}
	os.Stdout = outW

	oldStderr := os.Stderr
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		outR.Close()
		os.Stdout = oldStdout
		return "", "pipe error: " + err.Error(), 1
	}
	os.Stderr = errW

	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); io.Copy(&errBuf, errR) }()

	cmdErr := s.dispatch(s.db, command, args)

	outW.Close()
	errW.Close()
	wg.Wait()
	outR.Close()
	errR.Close()

	code := 0
	errStr := errBuf.String()
	if cmdErr != nil {
		code = 1
		if errStr == "" {
			errStr = cmdErr.Error()
		}
	}

	return outBuf.String(), errStr, code
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
