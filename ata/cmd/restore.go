package cmd

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"bytes"
	"io"
	"os"
	"strings"

	"aor/ata/db"
	"aor/ata/model"
)

func Restore(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Target workspace name or path (default: auto-detect)")
	force := fs.Bool("force", false, "Skip confirmation prompt")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, map[string]bool{
		"workspace": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata restore FILE [--workspace WS] [--force] [--json]")
	}
	archivePath := positional[0]

	// Read archive.
	entries, err := readTarGz(archivePath)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}

	// Parse metadata.
	metaBytes, ok := entries["metadata.json"]
	if !ok {
		return fmt.Errorf("archive missing metadata.json")
	}
	var meta model.SnapshotMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return fmt.Errorf("parse metadata: %w", err)
	}

	// Schema version check.
	if meta.SchemaVersion > db.SchemaVersion() {
		return fmt.Errorf("snapshot schema version %d is newer than current %d — upgrade ata first",
			meta.SchemaVersion, db.SchemaVersion())
	}

	// Resolve target workspace.
	ws := *workspace
	if ws == "" {
		ws = detectWorkspace(d)
	} else {
		if resolved, err := d.ResolveWorkspace(ws); err == nil {
			ws = resolved
		}
	}

	// Determine target name: use existing workspace name, or snapshot source name, or "".
	targetName := ""
	if existing, err := d.GetWorkspace(ws); err == nil && existing != nil {
		targetName = existing.Name
	} else if meta.SourceName != "" {
		targetName = meta.SourceName
	}

	// Parse JSONL files.
	tasks, err := parseJSONL[model.Task](entries["tasks.jsonl"])
	if err != nil {
		return fmt.Errorf("parse tasks: %w", err)
	}
	comments, err := parseJSONL[model.Comment](entries["comments.jsonl"])
	if err != nil {
		return fmt.Errorf("parse comments: %w", err)
	}
	deps, err := parseJSONL[model.TaskDep](entries["task_deps.jsonl"])
	if err != nil {
		return fmt.Errorf("parse deps: %w", err)
	}
	tags, err := parseJSONL[model.TaskTag](entries["task_tags.jsonl"])
	if err != nil {
		return fmt.Errorf("parse tags: %w", err)
	}

	// Confirm unless --force.
	if !*force {
		fmt.Printf("This will replace ALL tasks in workspace: %s\n", ws)
		fmt.Printf("  Source: %s (%d tasks, %d comments)\n", meta.SourcePath, len(tasks), len(comments))
		fmt.Print("Continue? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) != "y" {
			fmt.Println("aborted")
			return nil
		}
	}

	if err := d.ImportWorkspace(ws, targetName, meta.SourcePath, tasks, comments, deps, tags); err != nil {
		return fmt.Errorf("import: %w", err)
	}

	if *jsonOut {
		return outputJSON(struct {
			Workspace string `json:"workspace"`
			Tasks     int    `json:"tasks"`
			Comments  int    `json:"comments"`
			Deps      int    `json:"deps"`
			Tags      int    `json:"tags"`
		}{ws, len(tasks), len(comments), len(deps), len(tags)})
	}

	fmt.Printf("restored: %s (%d tasks, %d comments, %d deps, %d tags)\n",
		ws, len(tasks), len(comments), len(deps), len(tags))
	return nil
}

func readTarGz(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	entries := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		entries[hdr.Name] = data
	}
	return entries, nil
}

func parseJSONL[T any](data []byte) ([]T, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var result []T
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var item T
		if err := dec.Decode(&item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}
