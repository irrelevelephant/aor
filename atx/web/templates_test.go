package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"testing"
)

// TestTemplatesRender exercises every page template — and the
// "window-list" block shared between the unified view and the
// /atx/api/m/{name}/windows endpoint — against representative data,
// catching template syntax and missing-field errors the Go compiler
// can't see.
func TestTemplatesRender(t *testing.T) {
	funcMap := template.FuncMap{
		"json": func(v any) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
	}
	parse := func(page string) *template.Template {
		tmpl, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html", "templates/"+page)
		if err != nil {
			t.Fatalf("parse %s: %v", page, err)
		}
		return tmpl
	}

	machinesT := parse("machines.html")
	terminalT := parse("terminal.html")

	machinesData := map[string]any{
		"Title": "machines",
		"Machines": []MachineView{
			{Name: "desktop", Display: "desktop", Color: "#58a6ff", Online: true, WindowCount: 2, LastActivity: "live", Windows: []WindowView{
				{Index: 1, Name: "editor", Notified: "2m"},
				{Index: 2, Name: "shell"},
			}},
			{Name: "laptop", Display: "laptop", Color: "#3fb950", Online: true, WindowCount: 0, LastActivity: "no windows"},
			{Name: "oldserver", Display: "oldserver", Color: "#f85149", Online: false, WindowCount: 0, LastActivity: "offline"},
		},
		"OfflineStart": 2,
	}
	var buf bytes.Buffer
	if err := machinesT.ExecuteTemplate(&buf, "layout", machinesData); err != nil {
		t.Fatalf("execute machines.html: %v", err)
	}
	machinesOut := buf.String()
	for _, want := range []string{
		`id="m-desktop"`,
		`id="w-desktop"`,
		`class="offline-divider"`,
		`machine-offline`,
		`aria-expanded="false"`,
		`id="expand-all"`,
		`localStorage.getItem('atx.expanded')`,
		// Window list is now pre-rendered into every .machine-windows
		// container so restored-expanded machines have content on first
		// paint and the lazy-load fetch is unnecessary.
		`data-loaded="1"`,
		`class="window-list"`,
		`class="window-activity"`,
		`href="/atx/m/desktop/w/1"`,
		`empty-windows`,
	} {
		if !strings.Contains(machinesOut, want) {
			t.Errorf("rendered machines.html missing %q", want)
		}
	}
	// Server render still defaults to collapsed — expanded attributes are
	// applied client-side by the inline restore script.
	for _, unwant := range []string{
		`data-expanded="1"`,
		`aria-expanded="true"`,
	} {
		if strings.Contains(machinesOut, unwant) {
			t.Errorf("server render should not contain %q", unwant)
		}
	}

	// Empty-machines render should NOT include the expand-all button.
	buf.Reset()
	if err := machinesT.ExecuteTemplate(&buf, "layout", map[string]any{"Title": "machines", "Machines": []MachineView{}, "OfflineStart": 0}); err != nil {
		t.Fatalf("execute empty machines.html: %v", err)
	}
	if strings.Contains(buf.String(), `id="expand-all"`) {
		t.Errorf("empty machines view should not render the expand-all toggle")
	}

	// The "window-list" block is what the API endpoint serves, so render
	// it standalone too — any drift between full-page render and API
	// render would show up as a divergence here.
	buf.Reset()
	err := machinesT.ExecuteTemplate(&buf, "window-list", MachineView{
		Name: "desktop",
		Windows: []WindowView{
			{Index: 1, Name: "editor", Notified: "2m"},
		},
	})
	if err != nil {
		t.Fatalf("execute window-list: %v", err)
	}
	if !strings.Contains(buf.String(), `href="/atx/m/desktop/w/1"`) {
		t.Errorf("window-list block missing window link, got: %s", buf.String())
	}

	// Empty windows path.
	buf.Reset()
	if err := machinesT.ExecuteTemplate(&buf, "window-list", MachineView{Name: "desktop"}); err != nil {
		t.Fatalf("execute window-list empty: %v", err)
	}
	if !strings.Contains(buf.String(), "empty-windows") {
		t.Errorf("empty window-list missing empty-windows marker")
	}

	buf.Reset()
	terminalData := map[string]any{
		"Title":   "desktop · 1 editor",
		"Machine": MachineView{Name: "desktop", Display: "desktop", Color: "#58a6ff", Online: true},
		"Window":  WindowView{Index: 1, Name: "editor"},
		"Machines": []MachineView{
			{Name: "desktop", Display: "desktop", Color: "#58a6ff", Online: true, WindowCount: 1, Windows: []WindowView{{Index: 1, Name: "editor"}}},
		},
	}
	if err := terminalT.ExecuteTemplate(&buf, "layout", terminalData); err != nil {
		t.Fatalf("execute terminal.html: %v", err)
	}
	terminalOut := buf.String()
	for _, want := range []string{
		`id="terminal-nav-prev"`,
		`id="terminal-nav-next"`,
		`id="terminal-picker-machine"`,
		`id="terminal-picker-machine-popover"`,
		`id="terminal-picker-window"`,
		`id="terminal-picker-window-popover"`,
		`"name":"desktop"`,
		`"index":1`,
	} {
		if !strings.Contains(terminalOut, want) {
			t.Errorf("rendered terminal.html missing %q", want)
		}
	}
	if strings.Contains(terminalOut, "terminal-back") {
		t.Errorf("terminal.html should no longer render the back-link element")
	}
}
