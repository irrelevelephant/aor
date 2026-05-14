package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aor/ata/db"
)

// autoBackupRetention is the maximum number of pre-op snapshots kept in
// ~/.ata/backups. Older ones are pruned after each new write.
const autoBackupRetention = 20

// writeAutoBackup snapshots the current DB to ~/.ata/backups/pre-<op>-<ts>.tar.gz
// before a destructive operation runs. Returns the path written. Errors are
// fatal: if we can't take the safety snapshot, the caller should refuse the
// destructive op rather than proceed without a parachute.
func writeAutoBackup(d *db.DB, op string) (string, error) {
	dir, err := db.AutoBackupsDir()
	if err != nil {
		return "", fmt.Errorf("backups dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir backups: %w", err)
	}

	meta, tasks, comments, deps, tags, attachments, err := d.ExportAll()
	if err != nil {
		return "", fmt.Errorf("export: %w", err)
	}
	attDir, _ := db.AttachmentsDir()

	name := fmt.Sprintf("pre-%s-%d.tar.gz", op, time.Now().Unix())
	outPath := filepath.Join(dir, name)
	if err := writeSnapshotArchive(outPath, meta, tasks, comments, deps, tags, attachments, attDir); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("write snapshot: %w", err)
	}

	pruneAutoBackups(dir)
	return outPath, nil
}

// pruneAutoBackups deletes the oldest pre-op snapshots beyond autoBackupRetention.
// Best-effort: surface a warning but don't fail the caller.
func pruneAutoBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var snaps []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), "pre-") || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		snaps = append(snaps, e)
	}
	if len(snaps) <= autoBackupRetention {
		return
	}
	sort.Slice(snaps, func(i, j int) bool {
		ii, _ := snaps[i].Info()
		jj, _ := snaps[j].Info()
		return ii.ModTime().Before(jj.ModTime())
	})
	for _, e := range snaps[:len(snaps)-autoBackupRetention] {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			fmt.Fprintf(os.Stderr, "warning: prune auto-backup %s: %v\n", e.Name(), err)
		}
	}
}
