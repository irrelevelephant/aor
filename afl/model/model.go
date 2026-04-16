package model

// Platform constants.
const (
	PlatformWebDesktop = "web-desktop"
	PlatformWebMobile  = "web-mobile"
	PlatformIOS        = "ios"
	PlatformAndroid    = "android"
)

// ValidPlatforms is the list of all valid platform values.
var ValidPlatforms = []string{PlatformWebDesktop, PlatformWebMobile, PlatformIOS, PlatformAndroid}

// IsValidPlatform reports whether s is a valid platform.
func IsValidPlatform(s string) bool {
	for _, p := range ValidPlatforms {
		if s == p {
			return true
		}
	}
	return false
}

// PathType constants.
const (
	PathTypeHappy     = "happy"
	PathTypeAlternate = "alternate"
	PathTypeError     = "error"
)

// IsValidPathType reports whether s is a valid path type.
func IsValidPathType(s string) bool {
	switch s {
	case PathTypeHappy, PathTypeAlternate, PathTypeError:
		return true
	}
	return false
}

// CaptureSource constants.
const (
	SourcePlaywright    = "playwright"
	SourceXcodeBuildMCP = "xcodebuildmcp"
	SourceDroidMind     = "droidmind"
	SourceManual        = "manual"
)

// IsValidSource reports whether s is a valid capture source.
func IsValidSource(s string) bool {
	switch s {
	case SourcePlaywright, SourceXcodeBuildMCP, SourceDroidMind, SourceManual:
		return true
	}
	return false
}

// Workspace represents a registered workspace.
type Workspace struct {
	Path      string `json:"path"`
	Name      string `json:"name,omitempty"`
	CreatedAt string `json:"created_at"`
}

// DisplayName returns the name if set, otherwise the path.
func (w Workspace) DisplayName() string {
	if w.Name != "" {
		return w.Name
	}
	return w.Path
}

// Domain represents a feature domain (e.g. "hydration", "nutrition").
type Domain struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Flow represents a spec flow within a domain.
type Flow struct {
	ID        string `json:"id"`
	DomainID  string `json:"domain_id"`
	FlowID    string `json:"flow_id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Path represents a path through a flow (happy, alternate, error).
type Path struct {
	ID        string `json:"id"`
	FlowID    string `json:"flow_id"`
	PathType  string `json:"path_type"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Step represents a single step within a path.
type Step struct {
	ID          string `json:"id"`
	PathID      string `json:"path_id"`
	SortOrder   int    `json:"sort_order"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Screenshot represents a platform-specific screenshot for a step.
type Screenshot struct {
	ID            string `json:"id"`
	StepID        string `json:"step_id"`
	Platform      string `json:"platform"`
	Filename      string `json:"filename"`
	StoredName    string `json:"stored_name"`
	MimeType      string `json:"mime_type"`
	SizeBytes     int64  `json:"size_bytes"`
	CaptureSource string `json:"capture_source,omitempty"`
	CapturedAt    string `json:"captured_at"`
	CreatedAt     string `json:"created_at"`
}

// DomainCoverage holds a domain with coverage statistics.
type DomainCoverage struct {
	Domain
	TotalFlows   int `json:"total_flows"`
	CoveredFlows int `json:"covered_flows"`
}

// FlowCoverage holds a flow with per-path coverage.
type FlowCoverage struct {
	Flow
	Paths []PathCoverage `json:"paths"`
}

// PathCoverage holds a path with per-platform step coverage counts.
type PathCoverage struct {
	Path
	TotalSteps int            `json:"total_steps"`
	Coverage   map[string]int `json:"coverage"`
}
