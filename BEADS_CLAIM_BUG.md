# Beads `--claim` Deadlock Bug

## Affected Version

beads v0.54.0 — `internal/storage/dolt/issues.go:397`

## Symptom

`bd update <id> --claim` hangs indefinitely when the issue already has an
assignee. The process must be killed externally; on timeout/cancel the error is:

```
Error claiming <id>: failed to get current assignee: context canceled
```

## Root Cause

`ClaimIssue()` opens a transaction, runs a conditional UPDATE that only matches
rows where `assignee = '' OR assignee IS NULL`, then checks `RowsAffected`. When
the issue is already assigned, 0 rows are affected and the function falls through
to a diagnostic query to report who holds the claim:

```go
// Line 397 — BUG: uses s.db instead of tx
err := s.db.QueryRowContext(ctx,
    `SELECT assignee FROM issues WHERE id = ?`, id).Scan(&currentAssignee)
```

This query goes through `s.db` (the connection pool) rather than `tx` (the open
transaction). Embedded Dolt sets `MaxOpenConns=1`, so:

1. The transaction holds the only connection with a write lock on `issues`.
2. `s.db.QueryRowContext` requests a new connection from the pool.
3. The pool has no available connections — the one connection is held by `tx`.
4. Classic deadlock: the query waits for a connection that the transaction will
   never release (because it's waiting for the query to finish first).

## Fix

Change line 397 from `s.db` to `tx`:

```diff
 if rowsAffected == 0 {
     var currentAssignee string
-    err := s.db.QueryRowContext(ctx, `SELECT assignee FROM issues WHERE id = ?`, id).Scan(&currentAssignee)
+    err := tx.QueryRowContext(ctx, `SELECT assignee FROM issues WHERE id = ?`, id).Scan(&currentAssignee)
     if err != nil {
         return fmt.Errorf("failed to get current assignee: %w", err)
     }
     return fmt.Errorf("%w by %s", storage.ErrAlreadyClaimed, currentAssignee)
 }
```

This keeps the read within the existing transaction, avoiding the second
connection request entirely.

## aor Workaround

Until the upstream fix ships, aor avoids `--claim` and uses
`bd update <id> --status in_progress` instead. See `beads.go:claimTask()`.
