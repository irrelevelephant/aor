package db

import (
	"fmt"

	"aor/afl/model"
)

// DomainCoverage returns coverage statistics for all domains in a workspace.
// A flow is considered "covered" when every step in its happy path has screenshots
// for all 4 platforms.
func (d *DB) DomainCoverage(workspace string) ([]model.DomainCoverage, error) {
	// Get all domains.
	domains, err := d.ListDomains(workspace)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	var result []model.DomainCoverage
	for _, dom := range domains {
		flows, err := d.ListFlows(dom.ID)
		if err != nil {
			return nil, fmt.Errorf("list flows for domain %s: %w", dom.ID, err)
		}

		dc := model.DomainCoverage{
			Domain:     dom,
			TotalFlows: len(flows),
		}

		for _, flow := range flows {
			if d.isFlowCovered(flow.ID) {
				dc.CoveredFlows++
			}
		}

		result = append(result, dc)
	}

	return result, nil
}

// isFlowCovered returns true if all steps in the happy path have all 4 platform screenshots.
func (d *DB) isFlowCovered(flowID string) bool {
	// Find the happy path for this flow.
	paths, err := d.ListPaths(flowID)
	if err != nil {
		return false
	}

	var happyPathID string
	for _, p := range paths {
		if p.PathType == model.PathTypeHappy {
			happyPathID = p.ID
			break
		}
	}
	if happyPathID == "" {
		return false
	}

	// Check that every step has all 4 platforms.
	steps, err := d.ListSteps(happyPathID)
	if err != nil || len(steps) == 0 {
		return false
	}

	for _, step := range steps {
		var count int
		err := d.QueryRow(`SELECT COUNT(DISTINCT platform) FROM screenshots WHERE step_id = ?`, step.ID).Scan(&count)
		if err != nil || count < len(model.ValidPlatforms) {
			return false
		}
	}

	return true
}

// FlowCoverage returns detailed coverage for a flow, broken down by path and platform.
func (d *DB) FlowCoverage(flowID string) (*model.FlowCoverage, error) {
	flow, err := d.GetFlow(flowID)
	if err != nil {
		return nil, err
	}

	paths, err := d.ListPaths(flowID)
	if err != nil {
		return nil, fmt.Errorf("list paths: %w", err)
	}

	fc := &model.FlowCoverage{
		Flow: *flow,
	}

	for _, p := range paths {
		steps, err := d.ListSteps(p.ID)
		if err != nil {
			return nil, fmt.Errorf("list steps for path %s: %w", p.ID, err)
		}

		coverage := make(map[string]int)
		for _, platform := range model.ValidPlatforms {
			coverage[platform] = 0
		}

		for _, step := range steps {
			rows, err := d.Query(`SELECT DISTINCT platform FROM screenshots WHERE step_id = ?`, step.ID)
			if err != nil {
				return nil, fmt.Errorf("query screenshots for step %s: %w", step.ID, err)
			}

			for rows.Next() {
				var platform string
				if err := rows.Scan(&platform); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan platform: %w", err)
				}
				coverage[platform]++
			}
			rows.Close()
		}

		fc.Paths = append(fc.Paths, model.PathCoverage{
			Path:       p,
			TotalSteps: len(steps),
			Coverage:   coverage,
		})
	}

	return fc, nil
}
