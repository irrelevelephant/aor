package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"aor/ata/db"
	"aor/ata/model"
)

func Snapshot(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace name or path (default: auto-detect)")
	output := fs.String("output", "", "Output file path (default: ata-snapshot-NAME-DATE.tar.gz)")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ws := resolveOrDetectWorkspace(d, *workspace)

	meta, tasks, comments, deps, tags, attachments, err := d.ExportWorkspace(ws)
	if err != nil {
		return err
	}

	// Resolve attachments directory.
	attDir, _ := db.AttachmentsDir()

	// Determine output filename.
	outPath := *output
	if outPath == "" {
		name := meta.SourceName
		if name == "" {
			name = sanitizeName(ws)
		}
		outPath = fmt.Sprintf("ata-snapshot-%s-%s.tar.gz", name, time.Now().Format("20060102"))
	}

	// Build tar.gz — write to file, remove on error.
	if err := writeSnapshotArchive(outPath, meta, tasks, comments, deps, tags, attachments, attDir); err != nil {
		os.Remove(outPath)
		return err
	}

	if *jsonOut {
		return outputJSON(struct {
			File        string `json:"file"`
			Tasks       int    `json:"tasks"`
			Comments    int    `json:"comments"`
			Deps        int    `json:"deps"`
			Tags        int    `json:"tags"`
			Attachments int    `json:"attachments"`
		}{outPath, len(tasks), len(comments), len(deps), len(tags), len(attachments)})
	}

	fmt.Printf("snapshot: %s (%d tasks, %d comments, %d deps, %d tags, %d attachments)\n",
		outPath, len(tasks), len(comments), len(deps), len(tags), len(attachments))
	return nil
}

func writeSnapshotArchive(outPath string, meta any, tasks []model.Task, comments []model.Comment, deps []model.TaskDep, tags []model.TaskTag, attachments []model.Attachment, attachmentsDir string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := writeTarEntry(tw, "metadata.json", metaBytes); err != nil {
		return err
	}
	if err := writeTarJSONL(tw, "tasks.jsonl", tasks); err != nil {
		return err
	}
	if err := writeTarJSONL(tw, "comments.jsonl", comments); err != nil {
		return err
	}
	if err := writeTarJSONL(tw, "task_deps.jsonl", deps); err != nil {
		return err
	}
	if err := writeTarJSONL(tw, "task_tags.jsonl", tags); err != nil {
		return err
	}
	if err := writeTarJSONL(tw, "attachments.jsonl", attachments); err != nil {
		return err
	}

	// Write attachment files into the archive.
	for _, a := range attachments {
		filePath := filepath.Join(attachmentsDir, a.TaskID, a.StoredName)
		data, err := os.ReadFile(filePath)
		if err != nil {
			// Skip missing files silently — they may have been deleted from disk.
			continue
		}
		if err := writeTarEntry(tw, "attachments/"+a.TaskID+"/"+a.StoredName, data); err != nil {
			return fmt.Errorf("write attachment %s: %w", a.StoredName, err)
		}
	}

	return nil
}

var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func sanitizeName(s string) string {
	s = nonAlphaNum.ReplaceAllString(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	err := tw.WriteHeader(&tar.Header{
		Name: name,
		Size: int64(len(data)),
		Mode: 0644,
	})
	if err != nil {
		return fmt.Errorf("tar header %s: %w", name, err)
	}
	_, err = tw.Write(data)
	return err
}

func writeTarJSONL[T any](tw *tar.Writer, name string, items []T) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
	}
	return writeTarEntry(tw, name, buf.Bytes())
}
