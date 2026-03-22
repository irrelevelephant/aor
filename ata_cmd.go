package main

import "fmt"

// ataCmdOpts holds optional flags for building ata command strings.
type ataCmdOpts struct {
	Workspace string
	EpicID    string
	Tag       string
	Body      string
	JSON      bool
}

// buildAtaCreateCmd constructs an `ata create` command string with the given
// title placeholder and optional flags. Used across prompt builders to avoid
// duplicating the flag-appending logic.
func buildAtaCreateCmd(title string, opts ataCmdOpts) string {
	var cmd string
	if opts.EpicID != "" {
		cmd = fmt.Sprintf(`ata create "%s"`, title)
	} else {
		cmd = fmt.Sprintf(`ata create "%s" --status queue`, title)
	}
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

// buildAtaReadyCmd constructs an `ata ready` command string with optional
// filter flags. Uses the same opts struct as buildAtaCreateCmd.
func buildAtaReadyCmd(opts ataCmdOpts) string {
	cmd := "ata ready"
	if opts.Workspace != "" {
		cmd += fmt.Sprintf(` --workspace "%s"`, opts.Workspace)
	}
	if opts.EpicID != "" {
		cmd += fmt.Sprintf(` --epic "%s"`, opts.EpicID)
	}
	if opts.Tag != "" {
		cmd += fmt.Sprintf(` --tag "%s"`, opts.Tag)
	}
	if opts.JSON {
		cmd += " --json"
	}
	return cmd
}
