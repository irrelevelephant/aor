package api

// ExecRequest is the request body for POST /api/v1/exec.
type ExecRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// ExecResponse is the response body for POST /api/v1/exec.
type ExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}
