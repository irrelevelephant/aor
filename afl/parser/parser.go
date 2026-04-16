package parser

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"aor/afl/model"
)

// ParsedDomain represents the domain extracted from a flows.md file.
type ParsedDomain struct {
	Slug  string       // derived from header (lowercased, hyphenated)
	Name  string       // from header text before " — UX Flows"
	Flows []ParsedFlow // flows parsed from ## sections
}

// ParsedFlow represents a single flow within a domain.
type ParsedFlow struct {
	FlowID string       // e.g., "WATER-LOG-ENTRY"
	Name   string       // e.g., "Add Water Entry"
	Paths  []ParsedPath // paths (happy, alternate, error)
}

// ParsedPath represents a path through a flow.
type ParsedPath struct {
	PathType string       // "happy", "alternate", "error"
	Name     string       // e.g., "Happy path", "Alternate: existing entry"
	Steps    []ParsedStep // ordered steps
}

// ParsedStep represents a single step within a path.
type ParsedStep struct {
	Order       int    // numeric prefix (1, 2, 3a -> 3, etc.)
	Name        string // step text after the number
	Description string // from → `[...]` line, if present
}

// header pattern: # Domain Name — UX Flows
var headerRe = regexp.MustCompile(`^#\s+(.+?)\s+—\s+UX Flows\s*$`)

// flow header pattern: ## FLOW-ID: Flow Name
var flowRe = regexp.MustCompile(`^##\s+([A-Z][A-Z0-9-]+):\s+(.+?)\s*$`)

// path header patterns: ### Happy path, ### Alternate paths, ### Alternate: ..., ### Error: ..., ### Error paths
var pathHappyRe = regexp.MustCompile(`^###\s+Happy\s+path(?:\s+\(.*\))?\s*$`)
var pathAlternateRe = regexp.MustCompile(`^###\s+Alternate(?:\s+paths?)?\s*$`)
var pathAlternateNamedRe = regexp.MustCompile(`^###\s+Alternate:\s+(.+?)\s*$`)
var pathErrorRe = regexp.MustCompile(`^###\s+Error(?:\s+paths?)?\s*$`)
var pathErrorNamedRe = regexp.MustCompile(`^###\s+Error:\s+(.+?)\s*$`)

// step line patterns:
// Numbered: 1. text, 2. text, 3a. text, 3e. text
// Bulleted alternate/error: - 2a. text, - 3e. text
var stepNumberedRe = regexp.MustCompile(`^(\d+[a-z]?)\.\s+(.+)`)
var stepBulletedRe = regexp.MustCompile(`^[-*]\s+(\d+[a-z]?)\.\s+(.+)`)

// arrow description: → `[...]` or -> `[...]`
var arrowDescRe = regexp.MustCompile(`[→]|->`)

// skip lines
var preconditionRe = regexp.MustCompile(`^\*\*Precondition\*\*`)
var triggerRe = regexp.MustCompile(`^\*\*Trigger\*\*`)
var e2eRe = regexp.MustCompile(`^\*\*E2E\*\*`)
var platformRe = regexp.MustCompile(`^\*\*Platform`)
var seeRefRe = regexp.MustCompile(`^See\s+\[`)

// extractOrder parses the numeric part from a step number like "3", "3a", "3e".
func extractOrder(s string) int {
	numStr := strings.TrimRightFunc(s, func(r rune) bool {
		return r >= 'a' && r <= 'z'
	})
	n, _ := strconv.Atoi(numStr)
	return n
}

// slugifyRe matches sequences of non-alphanumeric (except hyphen) characters.
var slugifyRe = regexp.MustCompile(`[^a-z0-9-]+`)

// slugify converts a domain name to a slug: lowercase, spaces/special chars to hyphens.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugifyRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// extractArrowDescription extracts the text within `[...]` from an arrow line.
// Handles both → `[text]` and -> `[text]` patterns, and also inline on the same line as a step.
func extractArrowDescription(line string) string {
	// Find the arrow marker position.
	idx := strings.Index(line, "→")
	if idx < 0 {
		idx = strings.Index(line, "->")
	}
	if idx < 0 {
		return ""
	}

	rest := line[idx:]
	// Find `[...]` in the rest.
	start := strings.Index(rest, "`[")
	if start < 0 {
		return ""
	}
	end := strings.Index(rest[start:], "]`")
	if end < 0 {
		return ""
	}
	// Extract content between `[ and ]`
	return rest[start+2 : start+end]
}

// isSkipLine returns true if the line should be skipped (metadata, not content).
func isSkipLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, ">") {
		return true
	}
	if trimmed == "---" {
		return true
	}
	if preconditionRe.MatchString(trimmed) {
		return true
	}
	if triggerRe.MatchString(trimmed) {
		return true
	}
	if e2eRe.MatchString(trimmed) {
		return true
	}
	if platformRe.MatchString(trimmed) {
		return true
	}
	return false
}

// ParseFlowsFile parses a flows.md file and returns the extracted domain with flows.
func ParseFlowsFile(path string) (*ParsedDomain, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	// Increase buffer for long lines (platform notes can be very long).
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return parseLines(lines)
}

func parseLines(lines []string) (*ParsedDomain, error) {
	domain, headerIdx, err := parseHeader(lines)
	if err != nil {
		return nil, err
	}

	domain.Flows = parseFlows(lines[headerIdx+1:])
	return domain, nil
}

// parseHeader finds and parses the # Domain — UX Flows header.
func parseHeader(lines []string) (*ParsedDomain, int, error) {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "# ") {
			continue
		}
		// Skip ## headers.
		if strings.HasPrefix(trimmed, "## ") {
			continue
		}
		m := headerRe.FindStringSubmatch(trimmed)
		if m == nil {
			return nil, 0, fmt.Errorf("header does not match expected format '# Domain — UX Flows': %q", trimmed)
		}
		name := strings.TrimSpace(m[1])
		return &ParsedDomain{
			Slug: slugify(name),
			Name: name,
		}, i, nil
	}
	return nil, 0, fmt.Errorf("no '# Domain — UX Flows' header found")
}

// parseFlows parses all ## flow sections from the remaining lines.
func parseFlows(lines []string) []ParsedFlow {
	var flows []ParsedFlow

	// Find all ## flow header indices.
	type flowSection struct {
		flowID string
		name   string
		start  int // line index after the ## header
	}

	var sections []flowSection
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		m := flowRe.FindStringSubmatch(trimmed)
		if m != nil {
			sections = append(sections, flowSection{
				flowID: m[1],
				name:   strings.TrimSpace(m[2]),
				start:  i + 1,
			})
		}
	}

	for i, sec := range sections {
		end := len(lines)
		if i+1 < len(sections) {
			end = sections[i+1].start - 1
		}

		sectionLines := lines[sec.start:end]
		paths := parsePaths(sectionLines)

		// Skip flows that are just cross-references with no paths (e.g., "See [FLOW-ID](...)")
		if len(paths) == 0 {
			continue
		}

		flows = append(flows, ParsedFlow{
			FlowID: sec.flowID,
			Name:   sec.name,
			Paths:  paths,
		})
	}

	return flows
}

// parsePaths extracts happy/alternate/error paths from a flow section's lines.
func parsePaths(lines []string) []ParsedPath {
	var paths []ParsedPath

	// Determine sections by ### headers.
	type pathSection struct {
		pathType string
		name     string
		start    int
	}

	var sections []pathSection

	// Track whether we've seen any ### header. If not, all numbered steps before
	// the first ### are an implicit happy path.
	firstHeaderIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if pathHappyRe.MatchString(trimmed) {
			if firstHeaderIdx < 0 {
				firstHeaderIdx = i
			}
			sections = append(sections, pathSection{
				pathType: model.PathTypeHappy,
				name:     "Happy path",
				start:    i + 1,
			})
			continue
		}

		if m := pathAlternateNamedRe.FindStringSubmatch(trimmed); m != nil {
			if firstHeaderIdx < 0 {
				firstHeaderIdx = i
			}
			sections = append(sections, pathSection{
				pathType: model.PathTypeAlternate,
				name:     strings.TrimSpace(m[1]),
				start:    i + 1,
			})
			continue
		}

		if pathAlternateRe.MatchString(trimmed) {
			if firstHeaderIdx < 0 {
				firstHeaderIdx = i
			}
			sections = append(sections, pathSection{
				pathType: model.PathTypeAlternate,
				name:     "Alternate paths",
				start:    i + 1,
			})
			continue
		}

		if m := pathErrorNamedRe.FindStringSubmatch(trimmed); m != nil {
			if firstHeaderIdx < 0 {
				firstHeaderIdx = i
			}
			sections = append(sections, pathSection{
				pathType: model.PathTypeError,
				name:     strings.TrimSpace(m[1]),
				start:    i + 1,
			})
			continue
		}

		if pathErrorRe.MatchString(trimmed) {
			if firstHeaderIdx < 0 {
				firstHeaderIdx = i
			}
			sections = append(sections, pathSection{
				pathType: model.PathTypeError,
				name:     "Error paths",
				start:    i + 1,
			})
			continue
		}

		// Also stop at a named ### section that doesn't match any of the above
		// patterns (e.g. "### Modal scroll preservation").
		if strings.HasPrefix(trimmed, "### ") {
			if firstHeaderIdx < 0 {
				firstHeaderIdx = i
			}
			// We don't add a section for it — it's non-path content we skip.
			sections = append(sections, pathSection{
				pathType: "_skip",
				name:     "",
				start:    i + 1,
			})
		}
	}

	// Check for implicit happy path: numbered steps before the first ### header.
	implicitEnd := len(lines)
	if firstHeaderIdx >= 0 {
		implicitEnd = firstHeaderIdx
	}

	// If there's no explicit happy path header but there are numbered steps at the top,
	// create an implicit happy path.
	hasExplicitHappy := false
	for _, sec := range sections {
		if sec.pathType == model.PathTypeHappy {
			hasExplicitHappy = true
			break
		}
	}

	if !hasExplicitHappy {
		implicitSteps := parseSteps(lines[:implicitEnd])
		if len(implicitSteps) > 0 {
			paths = append(paths, ParsedPath{
				PathType: model.PathTypeHappy,
				Name:     "Happy path",
				Steps:    implicitSteps,
			})
		}
	}

	// Parse each explicit section.
	for i, sec := range sections {
		if sec.pathType == "_skip" {
			continue
		}

		end := len(lines)
		if i+1 < len(sections) {
			end = sections[i+1].start - 1
		}

		sectionLines := lines[sec.start:end]
		steps := parseSteps(sectionLines)

		// Include the path even if it has zero steps (some paths just have text).
		// But for the import, only include paths with actual steps.
		if len(steps) == 0 {
			continue
		}

		paths = append(paths, ParsedPath{
			PathType: sec.pathType,
			Name:     sec.name,
			Steps:    steps,
		})
	}

	return paths
}

// parseSteps extracts steps from a section's lines.
// Steps can be numbered (1. text) or bulleted (- 2a. text).
func parseSteps(lines []string) []ParsedStep {
	var steps []ParsedStep

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if isSkipLine(trimmed) {
			continue
		}

		// Check for "See [" cross-reference lines — skip the whole flow.
		if seeRefRe.MatchString(trimmed) {
			continue
		}

		var stepNum, stepText string

		// Try numbered step: 1. text
		if m := stepNumberedRe.FindStringSubmatch(trimmed); m != nil {
			stepNum = m[1]
			stepText = m[2]
		} else if m := stepBulletedRe.FindStringSubmatch(trimmed); m != nil {
			// Try bulleted step: - 2a. text
			stepNum = m[1]
			stepText = m[2]
		} else {
			continue
		}

		order := extractOrder(stepNum)
		name := stepText
		description := ""

		// Check for inline arrow description on the same line.
		if arrowDescRe.MatchString(name) {
			desc := extractArrowDescription(name)
			if desc != "" {
				description = desc
				// Trim the arrow part from the name.
				arrowIdx := strings.Index(name, "→")
				if arrowIdx < 0 {
					arrowIdx = strings.Index(name, "->")
				}
				if arrowIdx > 0 {
					name = strings.TrimSpace(name[:arrowIdx])
				}
			}
		}

		// Check next line for continuation arrow description.
		if description == "" && i+1 < len(lines) {
			nextTrimmed := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(nextTrimmed, "→") || strings.HasPrefix(nextTrimmed, "->") {
				desc := extractArrowDescription(nextTrimmed)
				if desc != "" {
					description = desc
					i++ // consume the arrow line
				}
			}
		}

		// Handle multi-line step continuations that follow an inline arrow.
		// Some steps have sub-bullets (indented "- User taps..." lines) — skip those.

		steps = append(steps, ParsedStep{
			Order:       order,
			Name:        name,
			Description: description,
		})
	}

	return steps
}
