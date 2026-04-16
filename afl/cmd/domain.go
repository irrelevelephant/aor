package cmd

import (
	"flag"
	"fmt"

	"aor/afl/db"
	"aor/afl/model"
)

// Domain routes domain subcommands.
func Domain(d *db.DB, args []string) error {
	if len(args) == 0 {
		return domainUsage()
	}

	switch args[0] {
	case "create":
		return domainCreate(d, args[1:])
	case "list", "ls":
		return domainList(d, args[1:])
	case "show":
		return domainShow(d, args[1:])
	case "delete", "rm":
		return domainDelete(d, args[1:])
	default:
		return domainUsage()
	}
}

func domainCreate(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("domain create", flag.ContinueOnError)
	name := fs.String("name", "", "Display name (defaults to slug)")
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"name": true, "workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl domain create <slug> [--name <display-name>] [--workspace <ws>] [--json]")
	}

	slug := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	displayName := *name
	if displayName == "" {
		displayName = slug
	}

	dom, err := d.CreateDomain(slug, displayName, ws)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(dom)
	}

	fmt.Printf("created domain: %s (%s)\n", dom.Slug, dom.ID)
	return nil
}

func domainList(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("domain list", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ws := resolveOrDetectWorkspace(d, *workspace)

	domains, err := d.ListDomains(ws)
	if err != nil {
		return err
	}

	if *jsonOut {
		if domains == nil {
			domains = []model.Domain{}
		}
		return outputJSON(domains)
	}

	if len(domains) == 0 {
		fmt.Println("no domains")
		return nil
	}

	for _, dom := range domains {
		fmt.Printf("%s  %s  %s\n", dom.ID, dom.Slug, dom.Name)
	}
	return nil
}

func domainShow(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("domain show", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl domain show <slug> [--workspace <ws>] [--json]")
	}

	slug := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	dom, err := d.GetDomainBySlug(ws, slug)
	if err != nil {
		return err
	}
	if dom == nil {
		return fmt.Errorf("domain %q not found in workspace %s", slug, ws)
	}

	if *jsonOut {
		return outputJSON(dom)
	}

	fmt.Printf("ID:        %s\n", dom.ID)
	fmt.Printf("Slug:      %s\n", dom.Slug)
	fmt.Printf("Name:      %s\n", dom.Name)
	fmt.Printf("Workspace: %s\n", dom.Workspace)
	fmt.Printf("Created:   %s\n", dom.CreatedAt)
	fmt.Printf("Updated:   %s\n", dom.UpdatedAt)
	return nil
}

func domainDelete(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("domain delete", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl domain delete <slug> [--workspace <ws>] [--json]")
	}

	slug := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	dom, err := d.GetDomainBySlug(ws, slug)
	if err != nil {
		return err
	}
	if dom == nil {
		return fmt.Errorf("domain %q not found in workspace %s", slug, ws)
	}

	if err := d.DeleteDomain(dom.ID); err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(map[string]any{"deleted": dom.ID, "slug": slug})
	}

	fmt.Printf("deleted domain: %s (%s)\n", slug, dom.ID)
	return nil
}

func domainUsage() error {
	return fmt.Errorf(`usage: afl domain <subcommand>

Subcommands:
  create <slug>    Create a domain
  list             List domains
  show <slug>      Show domain details
  delete <slug>    Delete a domain

Flags:
  --name <name>      Display name (for create; defaults to slug)
  --workspace <ws>   Override workspace
  --json             Output JSON`)
}
