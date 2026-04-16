package cmd

import (
	"fmt"

	"aor/afl/db"
)

// Dispatch routes a subcommand to the appropriate handler.
func Dispatch(d *db.DB, subcmd string, args []string) error {
	switch subcmd {
	case "domain":
		return Domain(d, args)
	case "flow":
		return Flow(d, args)
	case "path":
		return PathCmd(d, args)
	case "step":
		return StepCmd(d, args)
	case "capture":
		return Capture(d, args)
	default:
		return fmt.Errorf("unknown command: %s", subcmd)
	}
}
