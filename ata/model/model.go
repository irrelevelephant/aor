package model

// Status constants for tasks.
const (
	StatusBacklog    = "backlog"
	StatusQueue      = "queue"
	StatusInProgress = "in_progress"
	StatusClosed     = "closed"
)

// IsPlaceStatus reports whether s is a "place" status (queue or backlog)
// from which child tasks inherit their parent epic's status.
func IsPlaceStatus(s string) bool {
	return s == StatusQueue || s == StatusBacklog
}

// Author constants for comments.
const (
	AuthorHuman  = "human"
	AuthorAgent  = "agent"
	AuthorSystem = "system"
)

// Task represents a task or epic in the system.
type Task struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty"`
	Status      string   `json:"status"`
	SortOrder   int      `json:"sort_order"`
	EpicID      string   `json:"epic_id,omitempty"`
	Worktree    string   `json:"worktree,omitempty"`
	CreatedIn   string   `json:"created_in,omitempty"`
	IsEpic      bool     `json:"is_epic"`
	Spec        string   `json:"spec,omitempty"`
	ClaimedPID  int      `json:"claimed_pid,omitempty"`
	ClaimedHost string   `json:"claimed_host,omitempty"`
	ClaimedAt   string   `json:"claimed_at,omitempty"`
	ClosedAt    string   `json:"closed_at,omitempty"`
	CloseReason string   `json:"close_reason,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// TaskTreeNode wraps a task or epic with its children for recursive tree rendering.
type TaskTreeNode struct {
	Task
	Children []TaskTreeNode
}

// BuildTree assembles a flat list of tasks into a tree rooted at rootID.
// Tasks whose EpicID equals rootID become top-level nodes; sub-epics recurse.
// A visited set guards against cycles from data corruption.
func BuildTree(rootID string, tasks []Task) []TaskTreeNode {
	byEpic := make(map[string][]Task)
	for _, t := range tasks {
		byEpic[t.EpicID] = append(byEpic[t.EpicID], t)
	}

	visited := make(map[string]bool)
	var build func(parentID string) []TaskTreeNode
	build = func(parentID string) []TaskTreeNode {
		kids := byEpic[parentID]
		if len(kids) == 0 {
			return nil
		}
		nodes := make([]TaskTreeNode, len(kids))
		for i, t := range kids {
			nodes[i] = TaskTreeNode{Task: t}
			if t.IsEpic && !visited[t.ID] {
				visited[t.ID] = true
				nodes[i].Children = build(t.ID)
			}
		}
		return nodes
	}

	return build(rootID)
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

// SnapshotMeta holds metadata for a snapshot archive.
type SnapshotMeta struct {
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     string `json:"created_at"`
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
	Total   int `json:"total"`
	Closed  int `json:"closed"`
	Open    int `json:"open"`
	InProg  int `json:"in_progress"`
	Queue   int `json:"queue"`
	Backlog int `json:"backlog"`
}
