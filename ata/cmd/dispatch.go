package cmd

import (
	"fmt"

	"aor/ata/db"
)

// Dispatch routes a subcommand to the appropriate handler.
// Used by both the CLI main and the API exec endpoint.
func Dispatch(d *db.DB, subcmd string, args []string) error {
	switch subcmd {
	case "init":
		return Init(d, args)
	case "clean":
		return Clean(d, args)
	case "uninit":
		return Uninit(d, args)
	case "create":
		return Create(d, args)
	case "list":
		return List(d, args)
	case "show":
		return Show(d, args)
	case "edit":
		return Edit(d, args)
	case "close":
		return Close(d, args)
	case "reopen":
		return Reopen(d, args)
	case "ready":
		return Ready(d, args)
	case "claim":
		return Claim(d, args)
	case "unclaim":
		return Unclaim(d, args)
	case "promote":
		return Promote(d, args)
	case "spec":
		return Spec(d, args)
	case "comment":
		return Comment(d, args)
	case "dep":
		return Dep(d, args)
	case "tag":
		return Tag(d, args)
	case "reorder":
		return Reorder(d, args)
	case "move":
		return Move(d, args)
	case "recover":
		return Recover(d, args)
	case "epic-close-eligible":
		return EpicCloseEligible(d, args)
	case "snapshot":
		return Snapshot(d, args)
	case "restore":
		return Restore(d, args)
	case "serve":
		return Serve(d, args)
	default:
		return fmt.Errorf("unknown command: %s", subcmd)
	}
}
