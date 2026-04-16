package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aor/ata/db"
	"aor/ata/model"
)

func Restore(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	force := fs.Bool("force", false, "Skip confirmation prompt")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) == 0 {
		return exitUsage("usage: ata restore FILE [--force] [--json]")
	}
	archivePath := positional[0]

	entries, err := readTarGz(archivePath)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}

	metaBytes, ok := entries["metadata.json"]
	if !ok {
		return fmt.Errorf("archive missing metadata.json")
	}
	var meta model.SnapshotMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return fmt.Errorf("parse metadata: %w", err)
	}

	if meta.SchemaVersion > db.SchemaVersion() {
		return fmt.Errorf("snapshot schema version %d is newer than current %d — upgrade ata first",
			meta.SchemaVersion, db.SchemaVersion())
	}

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
	attachments, err := parseJSONL[model.Attachment](entries["attachments.jsonl"])
	if err != nil {
		return fmt.Errorf("parse attachments: %w", err)
	}

	attDir, _ := db.AttachmentsDir()

	if !*force {
		fmt.Printf("This will REPLACE all existing tasks with the snapshot contents (%d tasks, %d comments).\n", len(tasks), len(comments))
		if !promptConfirm("Continue? [y/N] ", "y") {
			fmt.Println("aborted")
			return nil
		}
	}

	// Remove any pre-existing attachment files so we don't leave orphans.
	if attDir != "" {
		existingTasks, _ := d.ListTasks("", "", "", "")
		for _, t := range existingTasks {
			os.RemoveAll(filepath.Join(attDir, t.ID))
		}
	}

	if err := d.ImportAll(tasks, comments, deps, tags, attachments); err != nil {
		return fmt.Errorf("import: %w", err)
	}

	if attDir != "" {
		for name, data := range entries {
			if !strings.HasPrefix(name, "attachments/") {
				continue
			}
			destPath := filepath.Join(attDir, strings.TrimPrefix(name, "attachments/"))
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("create attachment dir: %w", err)
			}
			if err := os.WriteFile(destPath, data, 0o644); err != nil {
				return fmt.Errorf("write attachment file: %w", err)
			}
		}
	}

	if *jsonOut {
		return outputJSON(struct {
			Tasks       int `json:"tasks"`
			Comments    int `json:"comments"`
			Deps        int `json:"deps"`
			Tags        int `json:"tags"`
			Attachments int `json:"attachments"`
		}{len(tasks), len(comments), len(deps), len(tags), len(attachments)})
	}

	fmt.Printf("restored: %d tasks, %d comments, %d deps, %d tags, %d attachments\n",
		len(tasks), len(comments), len(deps), len(tags), len(attachments))
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
