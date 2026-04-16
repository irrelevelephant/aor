package cmd

import (
	"fmt"

	"aor/afl/db"
)

// Capture routes capture subcommands.
func Capture(d *db.DB, args []string) error {
	if len(args) == 0 {
		return captureUsage()
	}

	_ = d // will be used in Phase 2

	switch args[0] {
	case "upload":
		return fmt.Errorf("not yet implemented")
	case "batch":
		return fmt.Errorf("not yet implemented")
	case "status":
		return fmt.Errorf("not yet implemented")
	case "get":
		return fmt.Errorf("not yet implemented")
	default:
		return captureUsage()
	}
}

func captureUsage() error {
	return fmt.Errorf(`usage: afl capture <subcommand>

Subcommands:
  upload    Upload a screenshot for a step+platform
  batch     Batch capture screenshots
  status    Show capture status for a flow
  get       Get a screenshot

(All capture subcommands are not yet implemented)`)
}
