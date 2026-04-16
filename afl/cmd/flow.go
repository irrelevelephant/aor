package cmd

import (
	"flag"
	"fmt"

	"aor/afl/db"
	"aor/afl/model"
	"aor/afl/parser"
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
	case "import":
		return flowImport(d, args[1:])
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

// importResult tracks what was created/skipped during an import.
type importResult struct {
	Domain         string   `json:"domain"`
	DomainCreated  bool     `json:"domain_created"`
	FlowsCreated   []string `json:"flows_created"`
	FlowsSkipped   []string `json:"flows_skipped"`
	PathsCreated   int      `json:"paths_created"`
	PathsSkipped   int      `json:"paths_skipped"`
	StepsCreated   int      `json:"steps_created"`
	StepsSkipped   int      `json:"steps_skipped"`
}

func flowImport(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("flow import", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl flow import <flows.md-path> [--workspace <ws>] [--json]")
	}

	filePath := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	// Parse the flows.md file.
	parsed, err := parser.ParseFlowsFile(filePath)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filePath, err)
	}

	result := importResult{
		Domain:       parsed.Slug,
		FlowsCreated: []string{},
		FlowsSkipped: []string{},
	}

	// Ensure domain exists.
	dom, err := d.GetDomainBySlug(ws, parsed.Slug)
	if err != nil {
		return err
	}
	if dom == nil {
		dom, err = d.CreateDomain(parsed.Slug, parsed.Name, ws)
		if err != nil {
			return fmt.Errorf("create domain %q: %w", parsed.Slug, err)
		}
		result.DomainCreated = true
	}

	// Import each flow.
	for _, pf := range parsed.Flows {
		flow, err := d.GetFlowByFlowID(dom.ID, pf.FlowID)
		if err != nil {
			return fmt.Errorf("check flow %s: %w", pf.FlowID, err)
		}
		if flow != nil {
			result.FlowsSkipped = append(result.FlowsSkipped, pf.FlowID)
			// Still import paths/steps for existing flows.
		} else {
			flow, err = d.CreateFlow(dom.ID, pf.FlowID, pf.Name)
			if err != nil {
				return fmt.Errorf("create flow %s: %w", pf.FlowID, err)
			}
			result.FlowsCreated = append(result.FlowsCreated, pf.FlowID)
		}

		// Import paths.
		for pathIdx, pp := range pf.Paths {
			existingPath, err := d.GetPathByName(flow.ID, pp.Name)
			if err != nil {
				return fmt.Errorf("check path %q in flow %s: %w", pp.Name, pf.FlowID, err)
			}
			if existingPath != nil {
				result.PathsSkipped++
				// Still try to import steps for existing paths.
				for _, ps := range pp.Steps {
					existingStep, err := d.GetStepByOrder(existingPath.ID, ps.Order)
					if err != nil {
						return fmt.Errorf("check step %d in path %q: %w", ps.Order, pp.Name, err)
					}
					if existingStep != nil {
						result.StepsSkipped++
					} else {
						_, err = d.CreateStep(existingPath.ID, ps.Name, ps.Description, ps.Order)
						if err != nil {
							return fmt.Errorf("create step %d in path %q: %w", ps.Order, pp.Name, err)
						}
						result.StepsCreated++
					}
				}
				continue
			}

			path, err := d.CreatePath(flow.ID, pp.PathType, pp.Name, pathIdx)
			if err != nil {
				return fmt.Errorf("create path %q in flow %s: %w", pp.Name, pf.FlowID, err)
			}
			result.PathsCreated++

			// Import steps.
			for _, ps := range pp.Steps {
				existingStep, err := d.GetStepByOrder(path.ID, ps.Order)
				if err != nil {
					return fmt.Errorf("check step %d in path %q: %w", ps.Order, pp.Name, err)
				}
				if existingStep != nil {
					result.StepsSkipped++
					continue
				}

				_, err = d.CreateStep(path.ID, ps.Name, ps.Description, ps.Order)
				if err != nil {
					return fmt.Errorf("create step %d in path %q: %w", ps.Order, pp.Name, err)
				}
				result.StepsCreated++
			}
		}
	}

	if *jsonOut {
		return outputJSON(result)
	}

	// Text output.
	if result.DomainCreated {
		fmt.Printf("created domain: %s\n", parsed.Slug)
	} else {
		fmt.Printf("domain exists: %s\n", parsed.Slug)
	}
	if len(result.FlowsCreated) > 0 {
		fmt.Printf("created %d flow(s): %s\n", len(result.FlowsCreated), joinStrSlice(result.FlowsCreated, ", "))
	}
	if len(result.FlowsSkipped) > 0 {
		fmt.Printf("skipped %d existing flow(s): %s\n", len(result.FlowsSkipped), joinStrSlice(result.FlowsSkipped, ", "))
	}
	fmt.Printf("paths: %d created, %d skipped\n", result.PathsCreated, result.PathsSkipped)
	fmt.Printf("steps: %d created, %d skipped\n", result.StepsCreated, result.StepsSkipped)
	return nil
}

// joinStrSlice joins a string slice with a separator.
func joinStrSlice(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}

func flowUsage() error {
	return fmt.Errorf(`usage: afl flow <subcommand>

Subcommands:
  create <domain-slug> <FLOW-ID> <name>  Create a flow
  list [--domain <slug>]                  List flows
  show <FLOW-ID>                          Show flow details
  delete <FLOW-ID>                        Delete a flow
  import <flows.md-path>                  Import flows from a flows.md file

Flags:
  --domain <slug>    Filter by domain (for list)
  --workspace <ws>   Override workspace
  --json             Output JSON`)
}
