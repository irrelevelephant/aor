package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aor/ata/api"
)

// Client communicates with a remote ata server's exec API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New creates a Client for the given base URL.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Exec runs a command on the remote server and returns its output.
func (c *Client) Exec(command string, args []string) (stdout []byte, stderr string, exitCode int, err error) {
	body, err := json.Marshal(api.ExecRequest{
		Command: command,
		Args:    args,
	})
	if err != nil {
		return nil, "", 1, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/api/v1/exec"
	resp, err := c.HTTPClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, "", 1, fmt.Errorf("remote request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", 1, fmt.Errorf("read response: %w", err)
	}

	var result api.ExecResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, "", 1, fmt.Errorf("decode response: %w (body: %s)", err, string(respBody))
	}

	return []byte(result.Stdout), result.Stderr, result.ExitCode, nil
}
