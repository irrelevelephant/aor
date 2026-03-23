package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"aor/ata/config"
)

func Remote(args []string) error {
	if len(args) == 0 {
		return remoteUsage()
	}

	switch args[0] {
	case "add":
		return remoteAdd(args[1:])
	case "remove", "rm":
		return remoteRemove(args[1:])
	case "list", "ls":
		return remoteList(args[1:])
	default:
		return remoteUsage()
	}
}

func remoteAdd(args []string) error {
	fs := flag.NewFlagSet("remote add", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Remote-side workspace path (if different from local)")
	setDefault := fs.Bool("default", false, "Set as default remote")
	jsonOut := fs.Bool("json", false, "Output JSON")

	// Separate flags from positional args since flags may follow positionals.
	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 2 {
		return fmt.Errorf("usage: ata remote add <name> <url> [--workspace <path>] [--default]")
	}

	name := positional[0]
	url := positional[1]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]config.RemoteConfig)
	}

	cfg.Remotes[name] = config.RemoteConfig{
		URL:       url,
		Workspace: *workspace,
	}

	if *setDefault {
		cfg.DefaultRemote = name
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if *jsonOut {
		return outputJSON(map[string]any{
			"name":    name,
			"url":     url,
			"default": *setDefault || cfg.DefaultRemote == name,
		})
	}
	fmt.Printf("added remote %q → %s\n", name, url)
	return nil
}

func remoteRemove(args []string) error {
	fs := flag.NewFlagSet("remote remove", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: ata remote remove <name>")
	}
	name := fs.Arg(0)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if _, ok := cfg.Remotes[name]; !ok {
		return fmt.Errorf("remote %q not found", name)
	}

	delete(cfg.Remotes, name)
	if cfg.DefaultRemote == name {
		cfg.DefaultRemote = ""
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if *jsonOut {
		return outputJSON(map[string]any{"removed": name})
	}
	fmt.Printf("removed remote %q\n", name)
	return nil
}

func remoteList(args []string) error {
	fs := flag.NewFlagSet("remote list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cfg)
	}

	if len(cfg.Remotes) == 0 {
		fmt.Println("no remotes configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tURL\tWORKSPACE\tDEFAULT\n")
	for name, r := range cfg.Remotes {
		def := ""
		if name == cfg.DefaultRemote {
			def = "*"
		}
		ws := r.Workspace
		if ws == "" {
			ws = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, r.URL, ws, def)
	}
	return w.Flush()
}

func remoteUsage() error {
	return fmt.Errorf(`usage: ata remote <subcommand>

Subcommands:
  add <name> <url>   Add or update a remote
  remove <name>      Remove a remote
  list               List configured remotes

Flags for add:
  --workspace <path>  Remote-side workspace path override
  --default           Set as default remote`)
}
