package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aor/afl/api"
)

const (
	execTimeout   = 30 * time.Second
	uploadTimeout = 10 * time.Minute
)

// Client communicates with a remote afl server's exec API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New creates a Client for the given base URL. Per-call timeouts are applied
// via context; the HTTP client itself has no deadline so uploads aren't
// capped by a fast-fail ceiling inherited from plain exec calls.
func New(baseURL string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{},
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

	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/afl/exec", bytes.NewReader(body))
	if err != nil {
		return nil, "", 1, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, "", 1, fmt.Errorf("remote request failed: %w", err)
	}
	defer resp.Body.Close()

	return decodeExecResponse(resp.Body)
}

// Upload identifies the single arg slot in a command that references a local
// file or directory to stream to the remote.
type Upload struct {
	ArgIdx int
	IsDir  bool
}

// ExecWithUpload runs a command whose arg at up.ArgIdx is a local file (or
// directory, if up.IsDir). The path is read from args[up.ArgIdx] and streamed
// to the server via multipart; args is sent with a placeholder at that index
// so the server can materialize the upload to a temp path before dispatch.
func (c *Client) ExecWithUpload(command string, args []string, up Upload) (stdout []byte, stderr string, exitCode int, err error) {
	if up.ArgIdx < 0 || up.ArgIdx >= len(args) {
		return nil, "", 1, fmt.Errorf("upload arg index %d out of range", up.ArgIdx)
	}
	localPath := args[up.ArgIdx]
	key := strconv.Itoa(up.ArgIdx)

	attach, err := uploadAttacher(up, localPath, key)
	if err != nil {
		return nil, "", 1, err
	}

	rewritten := make([]string, len(args))
	copy(rewritten, args)
	if up.IsDir {
		rewritten[up.ArgIdx] = api.UploadDirPrefix + key
	} else {
		rewritten[up.ArgIdx] = api.UploadFilePrefix + key
	}

	return c.postMultipart(command, rewritten, attach)
}

// uploadAttacher builds the per-upload function that writes file parts into
// a multipart.Writer. It also runs any eager validation (dir-exists,
// dir-nonempty) before the streaming goroutine starts, so callers get a
// proper error instead of a half-written request.
func uploadAttacher(up Upload, localPath, key string) (func(*multipart.Writer) error, error) {
	if !up.IsDir {
		return func(mw *multipart.Writer) error {
			return attachFile(mw, api.UploadFilePart(key), localPath)
		}, nil
	}

	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil, fmt.Errorf("read dir %q: %w", localPath, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(localPath, e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("dir %q is empty", localPath)
	}
	sort.Strings(files)

	return func(mw *multipart.Writer) error {
		for i, path := range files {
			if err := attachFile(mw, api.UploadDirPart(key, i), path); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func (c *Client) postMultipart(command string, args []string, attach func(*multipart.Writer) error) ([]byte, string, int, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, "", 1, fmt.Errorf("marshal args: %w", err)
	}

	// Stream the multipart body through a pipe so large files never
	// buffer fully in memory. The writer goroutine's error is captured
	// and surfaced after the HTTP roundtrip.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	var writeErr error
	go func() {
		defer func() {
			_ = mw.Close()
			_ = pw.CloseWithError(writeErr)
		}()
		if err := mw.WriteField(api.CommandField, command); err != nil {
			writeErr = fmt.Errorf("write command field: %w", err)
			return
		}
		if err := mw.WriteField(api.ArgsField, string(argsJSON)); err != nil {
			writeErr = fmt.Errorf("write args field: %w", err)
			return
		}
		if err := attach(mw); err != nil {
			writeErr = err
			return
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+api.ExecUploadPath, pr)
	if err != nil {
		return nil, "", 1, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, "", 1, fmt.Errorf("remote upload failed: %w", err)
	}
	defer resp.Body.Close()

	return decodeExecResponse(resp.Body)
}

func decodeExecResponse(body io.Reader) ([]byte, string, int, error) {
	respBody, err := io.ReadAll(body)
	if err != nil {
		return nil, "", 1, fmt.Errorf("read response: %w", err)
	}
	var result api.ExecResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, "", 1, fmt.Errorf("decode response: %w (body: %s)", err, string(respBody))
	}
	return []byte(result.Stdout), result.Stderr, result.ExitCode, nil
}

func attachFile(mw *multipart.Writer, partName, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	part, err := mw.CreateFormFile(partName, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy %s: %w", path, err)
	}
	return nil
}
