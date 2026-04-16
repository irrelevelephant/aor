package parser

import (
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Water", "water"},
		{"Check-Ins", "check-ins"},
		{"Meal Planning", "meal-planning"},
		{"Health Bloodwork", "health-bloodwork"},
		{"Diet", "diet"},
		{"Auth", "auth"},
		{"Biometrics", "biometrics"},
	}
	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExtractOrder(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"1", 1},
		{"2", 2},
		{"3a", 3},
		{"3e", 3},
		{"10", 10},
		{"2b", 2},
	}
	for _, tc := range tests {
		got := extractOrder(tc.input)
		if got != tc.want {
			t.Errorf("extractOrder(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestExtractArrowDescription(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"→ `[progress bar, preset amount carousel]`", "progress bar, preset amount carousel"},
		{"-> `[error toast displayed]`", "error toast displayed"},
		{"   → `[entry saved]`", "entry saved"},
		{"User taps Add -> `[entry saved]`", "entry saved"},
		{"no arrow here", ""},
	}
	for _, tc := range tests {
		got := extractArrowDescription(tc.input)
		if got != tc.want {
			t.Errorf("extractArrowDescription(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseSimple(t *testing.T) {
	lines := []string{
		"# Water — UX Flows",
		"",
		"> Some blockquote",
		"",
		"## WATER-LOG: Add Water",
		"",
		"**Precondition**: on diary page",
		"**Trigger**: tap +",
		"",
		"### Happy path",
		"",
		"1. Open modal -> `[modal opens]`",
		"2. Select amount -> `[amount shown]`",
		"3. Tap Add -> `[saved]`",
		"",
		"### Alternate paths",
		"",
		"- 2a. No previous -> `[defaults shown]`",
		"",
		"### Error paths",
		"",
		"- 3e. Server error -> `[error toast]`",
		"",
		"**E2E**: `e2e/tests/water.spec.ts`",
	}

	result, err := parseLines(lines)
	if err != nil {
		t.Fatalf("parseLines: %v", err)
	}

	if result.Slug != "water" {
		t.Errorf("slug = %q, want %q", result.Slug, "water")
	}
	if result.Name != "Water" {
		t.Errorf("name = %q, want %q", result.Name, "Water")
	}

	if len(result.Flows) != 1 {
		t.Fatalf("flows = %d, want 1", len(result.Flows))
	}

	flow := result.Flows[0]
	if flow.FlowID != "WATER-LOG" {
		t.Errorf("flow_id = %q, want %q", flow.FlowID, "WATER-LOG")
	}
	if flow.Name != "Add Water" {
		t.Errorf("flow name = %q, want %q", flow.Name, "Add Water")
	}

	if len(flow.Paths) != 3 {
		t.Fatalf("paths = %d, want 3", len(flow.Paths))
	}

	// Happy path
	hp := flow.Paths[0]
	if hp.PathType != "happy" {
		t.Errorf("path[0] type = %q, want %q", hp.PathType, "happy")
	}
	if len(hp.Steps) != 3 {
		t.Fatalf("happy steps = %d, want 3", len(hp.Steps))
	}
	if hp.Steps[0].Order != 1 {
		t.Errorf("step[0] order = %d, want 1", hp.Steps[0].Order)
	}
	if hp.Steps[0].Name != "Open modal" {
		t.Errorf("step[0] name = %q, want %q", hp.Steps[0].Name, "Open modal")
	}
	if hp.Steps[0].Description != "modal opens" {
		t.Errorf("step[0] desc = %q, want %q", hp.Steps[0].Description, "modal opens")
	}

	// Alternate path
	ap := flow.Paths[1]
	if ap.PathType != "alternate" {
		t.Errorf("path[1] type = %q, want %q", ap.PathType, "alternate")
	}
	if len(ap.Steps) != 1 {
		t.Fatalf("alternate steps = %d, want 1", len(ap.Steps))
	}
	if ap.Steps[0].Order != 2 {
		t.Errorf("alt step order = %d, want 2", ap.Steps[0].Order)
	}

	// Error path
	ep := flow.Paths[2]
	if ep.PathType != "error" {
		t.Errorf("path[2] type = %q, want %q", ep.PathType, "error")
	}
	if len(ep.Steps) != 1 {
		t.Fatalf("error steps = %d, want 1", len(ep.Steps))
	}
}

func TestParseImplicitHappyPath(t *testing.T) {
	lines := []string{
		"# Diet — UX Flows",
		"",
		"## DIET-ADD: Create Meal",
		"",
		"**Precondition**: on page",
		"",
		"1. User taps add -> `[meal appears]`",
		"2. Meal shows empty -> `[no items]`",
		"",
		"### Alternate paths",
		"",
		"- 1a. Default meals -> `[named meals created]`",
	}

	result, err := parseLines(lines)
	if err != nil {
		t.Fatalf("parseLines: %v", err)
	}

	if len(result.Flows) != 1 {
		t.Fatalf("flows = %d, want 1", len(result.Flows))
	}

	flow := result.Flows[0]
	if len(flow.Paths) != 2 {
		t.Fatalf("paths = %d, want 2 (implicit happy + alternate)", len(flow.Paths))
	}

	hp := flow.Paths[0]
	if hp.PathType != "happy" {
		t.Errorf("path[0] type = %q, want %q", hp.PathType, "happy")
	}
	if hp.Name != "Happy path" {
		t.Errorf("path[0] name = %q, want %q", hp.Name, "Happy path")
	}
	if len(hp.Steps) != 2 {
		t.Errorf("happy steps = %d, want 2", len(hp.Steps))
	}
}

func TestParseCrossReferenceFlowSkipped(t *testing.T) {
	lines := []string{
		"# Diary — UX Flows",
		"",
		"## DIARY-OPEN-CHECK-IN: Open Check-In from Diary",
		"",
		"See [CHECKIN-CREATE](../check-ins/flows.md#checkin-create). The diary plan box shows check-in status.",
		"",
		"**E2E**: `e2e/tests/diary/diary-check-in-modal.spec.ts`",
		"",
		"---",
		"",
		"## DIARY-DATE-NAV: Navigate Between Dates",
		"",
		"### Happy path",
		"",
		"1. Date nav shows arrows -> `[date centered]`",
		"2. User taps arrow -> `[content swaps]`",
	}

	result, err := parseLines(lines)
	if err != nil {
		t.Fatalf("parseLines: %v", err)
	}

	// The cross-reference flow should be skipped (no paths).
	if len(result.Flows) != 1 {
		t.Fatalf("flows = %d, want 1 (cross-ref skipped)", len(result.Flows))
	}
	if result.Flows[0].FlowID != "DIARY-DATE-NAV" {
		t.Errorf("flow_id = %q, want %q", result.Flows[0].FlowID, "DIARY-DATE-NAV")
	}
}

func TestParseMultilineArrow(t *testing.T) {
	lines := []string{
		"# Test — UX Flows",
		"",
		"## TEST-MULTI: Multi Line",
		"",
		"### Happy path",
		"",
		"1. Step one",
		"   → `[description on next line]`",
		"2. Step two -> `[inline desc]`",
	}

	result, err := parseLines(lines)
	if err != nil {
		t.Fatalf("parseLines: %v", err)
	}

	if len(result.Flows) != 1 {
		t.Fatalf("flows = %d, want 1", len(result.Flows))
	}

	steps := result.Flows[0].Paths[0].Steps
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}

	if steps[0].Description != "description on next line" {
		t.Errorf("step[0] desc = %q, want %q", steps[0].Description, "description on next line")
	}
	if steps[1].Description != "inline desc" {
		t.Errorf("step[1] desc = %q, want %q", steps[1].Description, "inline desc")
	}
}

func TestParseHappyPathWithParenthesized(t *testing.T) {
	// This tests the real pattern from diary/flows.md:
	// ### Happy path (data unchanged, images fresh)
	lines := []string{
		"# Test — UX Flows",
		"",
		"## TEST-WAKE: Tab Wake",
		"",
		"### Happy path (data unchanged, images fresh)",
		"",
		"1. Tab becomes visible -> `[freshness check]`",
		"2. Content matches -> `[no change]`",
	}

	result, err := parseLines(lines)
	if err != nil {
		t.Fatalf("parseLines: %v", err)
	}

	// The parenthesized happy path won't match the strict regex,
	// so it becomes an implicit happy path from numbered steps before ###.
	// Actually, the steps are AFTER the ### so they won't be implicit.
	// This is a non-standard header -- it should be handled gracefully.
	if len(result.Flows) != 1 {
		t.Fatalf("flows = %d, want 1", len(result.Flows))
	}
}

func TestParseInvalidHeader(t *testing.T) {
	lines := []string{
		"# Not A Valid Header",
		"",
		"## FLOW-1: Something",
	}

	_, err := parseLines(lines)
	if err == nil {
		t.Error("expected error for invalid header, got nil")
	}
}
