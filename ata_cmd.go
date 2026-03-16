package main

import "fmt"

// ataCreateOpts holds optional flags for building an ata create command string.
type ataCreateOpts struct {
	Workspace string
	EpicID    string
	Tag       string
	Body      string
	JSON      bool
}

// buildAtaCreateCmd constructs an `ata create` command string with the given
// title placeholder and optional flags. Used across prompt builders to avoid
// duplicating the flag-appending logic.
func buildAtaCreateCmd(title string, opts ataCreateOpts) string {
	cmd := fmt.Sprintf(`ata create "%s" --status queue`, title)
	if opts.Workspace != "" {
		cmd += fmt.Sprintf(` --workspace "%s"`, opts.Workspace)
	}
	if opts.EpicID != "" {
		cmd += fmt.Sprintf(` --epic "%s"`, opts.EpicID)
	}
	if opts.Tag != "" {
		cmd += fmt.Sprintf(` --tag "%s"`, opts.Tag)
	}
	if opts.Body != "" {
		cmd += fmt.Sprintf(` --body "%s"`, opts.Body)
	}
	if opts.JSON {
		cmd += " --json"
	}
	return cmd
}
