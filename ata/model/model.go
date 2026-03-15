package model

// Status constants for tasks.
const (
	StatusBacklog    = "backlog"
	StatusQueue      = "queue"
	StatusInProgress = "in_progress"
	StatusClosed     = "closed"
)

// Author constants for comments.
const (
	AuthorHuman  = "human"
	AuthorAgent  = "agent"
	AuthorSystem = "system"
)

// Task represents a task or epic in the system.
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Status      string `json:"status"`
	SortOrder   int    `json:"sort_order"`
	EpicID      string `json:"epic_id,omitempty"`
	Workspace   string `json:"workspace"`
	Worktree    string `json:"worktree,omitempty"`
	CreatedIn   string `json:"created_in,omitempty"`
	IsEpic      bool   `json:"is_epic"`
	Spec        string `json:"spec,omitempty"`
	ClaimedPID  int    `json:"claimed_pid,omitempty"`
	ClaimedAt   string `json:"claimed_at,omitempty"`
	ClosedAt    string `json:"closed_at,omitempty"`
	CloseReason string   `json:"close_reason,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// Comment represents a comment on a task.
type Comment struct {
	ID        int    `json:"id"`
	TaskID    string `json:"task_id"`
	Body      string `json:"body"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
}

// TaskWithComments is a task with its associated comments and attachments, used by show.
type TaskWithComments struct {
	Task
	Comments    []Comment    `json:"comments,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
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

// WorkspaceInfo is a workspace with an active task count, for the dashboard.
type WorkspaceInfo struct {
	Path  string `json:"path"`
	Name  string `json:"name,omitempty"`
	Count int    `json:"count"`
}

// DisplayName returns the name if set, otherwise the path.
func (wi WorkspaceInfo) DisplayName() string {
	if wi.Name != "" {
		return wi.Name
	}
	return wi.Path
}

// SnapshotMeta holds metadata for a workspace snapshot archive.
type SnapshotMeta struct {
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
	SourcePath    string `json:"source_path"`
	SourceName    string `json:"source_name,omitempty"`
}

// TaskDep represents a dependency edge for snapshot export/import.
type TaskDep struct {
	TaskID    string `json:"task_id"`
	DependsOn string `json:"depends_on"`
	CreatedAt string `json:"created_at"`
}

// TaskTag represents a tag association for snapshot export/import.
type TaskTag struct {
	TaskID    string `json:"task_id"`
	Tag       string `json:"tag"`
	CreatedAt string `json:"created_at"`
}

// Attachment represents a file attached to a task.
type Attachment struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Filename   string `json:"filename"`
	StoredName string `json:"stored_name"`
	MimeType   string `json:"mime_type"`
	SizeBytes  int64  `json:"size_bytes"`
	CreatedAt  string `json:"created_at"`
}

// IsImage returns true if the attachment is an image type.
func (a Attachment) IsImage() bool {
	switch a.MimeType {
	case "image/png", "image/jpeg", "image/gif", "image/svg+xml", "image/webp":
		return true
	}
	return false
}

// MarkdownRef returns a markdown reference for the attachment.
func (a Attachment) MarkdownRef(baseURL string) string {
	url := baseURL + "/attachments/" + a.TaskID + "/" + a.StoredName
	if a.IsImage() {
		return "![" + a.Filename + "](" + url + ")"
	}
	return "[" + a.Filename + "](" + url + ")"
}

// EpicProgress holds progress info for an epic.
type EpicProgress struct {
	Total    int `json:"total"`
	Closed   int `json:"closed"`
	Open     int `json:"open"`
	InProg   int `json:"in_progress"`
	Queue    int `json:"queue"`
	Backlog  int `json:"backlog"`
}
