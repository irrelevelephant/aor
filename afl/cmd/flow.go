package cmd

import (
	"flag"
	"fmt"

	"aor/afl/db"
	"aor/afl/model"
)

// Flow routes flow subcommands.
func Flow(d *db.DB, args []string) error {
	if len(args) == 0 {
		return flowUsage()
	}

	switch args[0] {
	case "create":
		return flowCreate(d, args[1:])
	case "list", "ls":
		return flowList(d, args[1:])
	case "show":
		return flowShow(d, args[1:])
	case "delete", "rm":
		return flowDelete(d, args[1:])
	default:
		return flowUsage()
	}
}

func flowCreate(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("flow create", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 3 {
		return fmt.Errorf("usage: afl flow create <domain-slug> <FLOW-ID> <name> [--workspace <ws>] [--json]")
	}

	domainSlug := positional[0]
	flowID := positional[1]
	name := positional[2]
	ws := resolveOrDetectWorkspace(d, *workspace)

	dom, err := d.GetDomainBySlug(ws, domainSlug)
	if err != nil {
		return err
	}
	if dom == nil {
		return fmt.Errorf("domain %q not found in workspace %s", domainSlug, ws)
	}

	flow, err := d.CreateFlow(dom.ID, flowID, name)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(flow)
	}

	fmt.Printf("created flow: %s %q (%s)\n", flow.FlowID, flow.Name, flow.ID)
	return nil
}

func flowList(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("flow list", flag.ContinueOnError)
	domain := fs.String("domain", "", "Filter by domain slug")
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	ws := resolveOrDetectWorkspace(d, *workspace)

	var flows []model.Flow
	var err error

	if *domain != "" {
		dom, domErr := d.GetDomainBySlug(ws, *domain)
		if domErr != nil {
			return domErr
		}
		if dom == nil {
			return fmt.Errorf("domain %q not found in workspace %s", *domain, ws)
		}
		flows, err = d.ListFlows(dom.ID)
	} else {
		flows, err = d.ListFlowsByWorkspace(ws)
	}
	if err != nil {
		return err
	}

	if *jsonOut {
		if flows == nil {
			flows = []model.Flow{}
		}
		return outputJSON(flows)
	}

	if len(flows) == 0 {
		fmt.Println("no flows")
		return nil
	}

	for _, f := range flows {
		fmt.Printf("%s  %s  %s\n", f.ID, f.FlowID, f.Name)
	}
	return nil
}

func flowShow(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("flow show", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl flow show <FLOW-ID> [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return err
	}
	if flow == nil {
		return fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	if *jsonOut {
		return outputJSON(flow)
	}

	fmt.Printf("ID:        %s\n", flow.ID)
	fmt.Printf("Flow ID:   %s\n", flow.FlowID)
	fmt.Printf("Name:      %s\n", flow.Name)
	fmt.Printf("Domain:    %s\n", flow.DomainID)
	fmt.Printf("Order:     %d\n", flow.SortOrder)
	fmt.Printf("Created:   %s\n", flow.CreatedAt)
	fmt.Printf("Updated:   %s\n", flow.UpdatedAt)
	return nil
}

func flowDelete(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("flow delete", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl flow delete <FLOW-ID> [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return err
	}
	if flow == nil {
		return fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	if err := d.DeleteFlow(flow.ID); err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(map[string]any{"deleted": flow.ID, "flow_id": flowID})
	}

	fmt.Printf("deleted flow: %s (%s)\n", flowID, flow.ID)
	return nil
}

func flowUsage() error {
	return fmt.Errorf(`usage: afl flow <subcommand>

Subcommands:
  create <domain-slug> <FLOW-ID> <name>  Create a flow
  list [--domain <slug>]                  List flows
  show <FLOW-ID>                          Show flow details
  delete <FLOW-ID>                        Delete a flow

Flags:
  --domain <slug>    Filter by domain (for list)
  --workspace <ws>   Override workspace
  --json             Output JSON`)
}
