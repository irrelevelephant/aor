package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"aor/afl/config"
)

// Config handles config subcommands.
func Config(args []string) error {
	if len(args) == 0 {
		return configShow(args)
	}

	switch args[0] {
	case "default-workspace", "dw":
		return configDefaultWorkspace(args[1:])
	case "map":
		return configMap(args[1:])
	case "unmap":
		return configUnmap(args[1:])
	case "show":
		return configShow(args[1:])
	default:
		return configUsage()
	}
}

func configDefaultWorkspace(args []string) error {
	fs := flag.NewFlagSet("config default-workspace", flag.ContinueOnError)
	clear := fs.Bool("clear", false, "Clear the default workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if *clear {
		if len(positional) > 0 {
			return fmt.Errorf("--clear does not accept a workspace argument")
		}
		cfg.DefaultWorkspace = ""
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		if *jsonOut {
			return outputJSON(map[string]any{"default_workspace": nil})
		}
		fmt.Println("cleared default workspace")
		return nil
	}

	if len(positional) == 0 {
		if *jsonOut {
			return outputJSON(map[string]any{"default_workspace": cfg.DefaultWorkspace})
		}
		if cfg.DefaultWorkspace == "" {
			fmt.Println("no default workspace configured")
		} else {
			fmt.Println(cfg.DefaultWorkspace)
		}
		return nil
	}

	cfg.DefaultWorkspace = positional[0]
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if *jsonOut {
		return outputJSON(map[string]any{"default_workspace": cfg.DefaultWorkspace})
	}
	fmt.Printf("default workspace set to %q\n", cfg.DefaultWorkspace)
	return nil
}

func configMap(args []string) error {
	fs := flag.NewFlagSet("config map", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 2 {
		return fmt.Errorf("usage: afl config map <directory> <workspace>")
	}

	dir, err := filepath.Abs(positional[0])
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}

	workspace := positional[1]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Workspaces == nil {
		cfg.Workspaces = make(map[string]string)
	}
	cfg.Workspaces[dir] = workspace

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if *jsonOut {
		return outputJSON(map[string]any{"directory": dir, "workspace": workspace})
	}
	fmt.Printf("mapped %s -> %s\n", dir, workspace)
	return nil
}

func configUnmap(args []string) error {
	fs := flag.NewFlagSet("config unmap", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagArgs, positional := splitFlagsAndPositional(args, nil)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl config unmap <directory>")
	}

	dir, err := filepath.Abs(positional[0])
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if _, ok := cfg.Workspaces[dir]; !ok {
		return fmt.Errorf("no mapping for %q", dir)
	}

	delete(cfg.Workspaces, dir)
	if len(cfg.Workspaces) == 0 {
		cfg.Workspaces = nil
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if *jsonOut {
		return outputJSON(map[string]any{"unmapped": dir})
	}
	fmt.Printf("unmapped %s\n", dir)
	return nil
}

func configShow(args []string) error {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if *jsonOut {
		return outputJSON(map[string]any{
			"default_workspace": cfg.DefaultWorkspace,
			"workspaces":        cfg.Workspaces,
		})
	}

	if cfg.DefaultWorkspace == "" && len(cfg.Workspaces) == 0 {
		fmt.Println("no workspace config set")
		return nil
	}

	if cfg.DefaultWorkspace != "" {
		fmt.Printf("default workspace: %s\n", cfg.DefaultWorkspace)
	}

	if len(cfg.Workspaces) > 0 {
		fmt.Println("\ndirectory mappings:")
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for dir, ws := range cfg.Workspaces {
			fmt.Fprintf(w, "  %s\t-> %s\n", dir, ws)
		}
		w.Flush()
	}

	return nil
}

func configUsage() error {
	return fmt.Errorf(`usage: afl config <subcommand>

Subcommands:
  default-workspace [name]  Show or set the default workspace (alias: dw)
  map <dir> <workspace>     Map a directory to a workspace
  unmap <dir>               Remove a directory mapping
  show                      Show workspace config

Flags:
  --json    Output JSON
  --clear   Clear the default workspace (with default-workspace)`)
}
