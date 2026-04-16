package db

import (
	"aor/afl/model"
)

// DomainCoverage returns coverage statistics for all domains.
// A flow is considered "covered" when every step in its happy path has screenshots
// for all 4 platforms.
func (d *DB) DomainCoverage() ([]model.DomainCoverage, error) {
	domains, err := d.ListDomains()
	if err != nil {
		return nil, err
	}

	// Single query: for each flow, check if its happy path is fully covered.
	// A flow is covered when MIN(platform_count) across all happy-path steps >= 4.
	rows, err := d.Query(`
		SELECT f.id, f.domain_id,
			CASE WHEN COUNT(s.id) > 0
			     AND MIN(COALESCE(sc.platform_count, 0)) >= ?
			THEN 1 ELSE 0 END AS covered
		FROM flows f
		JOIN paths p ON p.flow_id = f.id AND p.path_type = 'happy'
		JOIN steps s ON s.path_id = p.id
		LEFT JOIN (
			SELECT step_id, COUNT(DISTINCT platform) AS platform_count
			FROM screenshots GROUP BY step_id
		) sc ON sc.step_id = s.id
		GROUP BY f.id`, len(model.ValidPlatforms))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// flowID -> covered bool
	flowCovered := make(map[string]bool)
	flowDomain := make(map[string]string)
	for rows.Next() {
		var flowID, domainID string
		var covered int
		if err := rows.Scan(&flowID, &domainID, &covered); err != nil {
			return nil, err
		}
		flowCovered[flowID] = covered == 1
		flowDomain[flowID] = domainID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Also count flows per domain (including those with no happy path/steps).
	allFlows, err := d.Query(`SELECT id, domain_id FROM flows`)
	if err != nil {
		return nil, err
	}
	defer allFlows.Close()

	domainFlowCount := make(map[string]int)
	domainCoveredCount := make(map[string]int)
	for allFlows.Next() {
		var flowID, domainID string
		if err := allFlows.Scan(&flowID, &domainID); err != nil {
			return nil, err
		}
		domainFlowCount[domainID]++
		if flowCovered[flowID] {
			domainCoveredCount[domainID]++
		}
	}
	if err := allFlows.Err(); err != nil {
		return nil, err
	}

	var result []model.DomainCoverage
	for _, dom := range domains {
		result = append(result, model.DomainCoverage{
			Domain:       dom,
			TotalFlows:   domainFlowCount[dom.ID],
			CoveredFlows: domainCoveredCount[dom.ID],
		})
	}

	return result, nil
}

// DomainFlowsCoverage returns detailed coverage for all flows in a domain.
// Uses bulk queries instead of per-flow lookups.
func (d *DB) DomainFlowsCoverage(domainID string) ([]model.FlowCoverage, error) {
	flows, err := d.ListFlows(domainID)
	if err != nil {
		return nil, err
	}
	if len(flows) == 0 {
		return nil, nil
	}

	paths, err := d.Query(`
		SELECT `+pathCols+` FROM paths
		WHERE flow_id IN (SELECT id FROM flows WHERE domain_id = ?)
		ORDER BY flow_id, sort_order`, domainID)
	if err != nil {
		return nil, err
	}
	defer paths.Close()

	flowPaths := make(map[string][]model.Path)
	for paths.Next() {
		var p model.Path
		if err := paths.Scan(&p.ID, &p.FlowID, &p.PathType, &p.Name, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		flowPaths[p.FlowID] = append(flowPaths[p.FlowID], p)
	}
	if err := paths.Err(); err != nil {
		return nil, err
	}

	// Step counts per path.
	stepRows, err := d.Query(`
		SELECT p.id, COUNT(s.id)
		FROM paths p
		LEFT JOIN steps s ON s.path_id = p.id
		WHERE p.flow_id IN (SELECT id FROM flows WHERE domain_id = ?)
		GROUP BY p.id`, domainID)
	if err != nil {
		return nil, err
	}
	defer stepRows.Close()

	stepCounts := make(map[string]int)
	for stepRows.Next() {
		var pathID string
		var cnt int
		if err := stepRows.Scan(&pathID, &cnt); err != nil {
			return nil, err
		}
		stepCounts[pathID] = cnt
	}
	if err := stepRows.Err(); err != nil {
		return nil, err
	}

	// Screenshot coverage per path per platform.
	scRows, err := d.Query(`
		SELECT s.path_id, sc.platform, COUNT(*) AS cnt
		FROM steps s
		JOIN screenshots sc ON sc.step_id = s.id
		JOIN paths p ON p.id = s.path_id
		WHERE p.flow_id IN (SELECT id FROM flows WHERE domain_id = ?)
		GROUP BY s.path_id, sc.platform`, domainID)
	if err != nil {
		return nil, err
	}
	defer scRows.Close()

	pathPlatformCounts := make(map[string]map[string]int)
	for scRows.Next() {
		var pathID, platform string
		var cnt int
		if err := scRows.Scan(&pathID, &platform, &cnt); err != nil {
			return nil, err
		}
		if pathPlatformCounts[pathID] == nil {
			pathPlatformCounts[pathID] = make(map[string]int)
		}
		pathPlatformCounts[pathID][platform] = cnt
	}
	if err := scRows.Err(); err != nil {
		return nil, err
	}

	var result []model.FlowCoverage
	for _, f := range flows {
		fc := model.FlowCoverage{Flow: f}
		for _, p := range flowPaths[f.ID] {
			coverage := make(map[string]int)
			for _, platform := range model.ValidPlatforms {
				coverage[platform] = 0
			}
			if counts, ok := pathPlatformCounts[p.ID]; ok {
				for platform, cnt := range counts {
					coverage[platform] = cnt
				}
			}
			fc.Paths = append(fc.Paths, model.PathCoverage{
				Path:       p,
				TotalSteps: stepCounts[p.ID],
				Coverage:   coverage,
			})
		}
		result = append(result, fc)
	}
	return result, nil
}

// FlowCoverage returns detailed coverage for a flow, broken down by path and platform.
func (d *DB) FlowCoverage(flowID string) (*model.FlowCoverage, error) {
	flow, err := d.GetFlow(flowID)
	if err != nil {
		return nil, err
	}

	paths, err := d.ListPaths(flowID)
	if err != nil {
		return nil, err
	}

	// Single query: get all screenshot platform counts for all steps in this flow.
	rows, err := d.Query(`
		SELECT s.path_id, sc.platform, COUNT(*) AS cnt
		FROM steps s
		JOIN screenshots sc ON sc.step_id = s.id
		JOIN paths p ON p.id = s.path_id
		WHERE p.flow_id = ?
		GROUP BY s.path_id, sc.platform`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// pathID -> platform -> count of steps with that platform
	pathPlatformCounts := make(map[string]map[string]int)
	for rows.Next() {
		var pathID, platform string
		var cnt int
		if err := rows.Scan(&pathID, &platform, &cnt); err != nil {
			return nil, err
		}
		if pathPlatformCounts[pathID] == nil {
			pathPlatformCounts[pathID] = make(map[string]int)
		}
		pathPlatformCounts[pathID][platform] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Get step counts per path in a single query.
	stepRows, err := d.Query(`
		SELECT p.id, COUNT(s.id)
		FROM paths p
		LEFT JOIN steps s ON s.path_id = p.id
		WHERE p.flow_id = ?
		GROUP BY p.id`, flowID)
	if err != nil {
		return nil, err
	}
	defer stepRows.Close()

	stepCounts := make(map[string]int)
	for stepRows.Next() {
		var pathID string
		var cnt int
		if err := stepRows.Scan(&pathID, &cnt); err != nil {
			return nil, err
		}
		stepCounts[pathID] = cnt
	}
	if err := stepRows.Err(); err != nil {
		return nil, err
	}

	fc := &model.FlowCoverage{Flow: *flow}
	for _, p := range paths {
		coverage := make(map[string]int)
		for _, platform := range model.ValidPlatforms {
			coverage[platform] = 0
		}
		if counts, ok := pathPlatformCounts[p.ID]; ok {
			for platform, cnt := range counts {
				coverage[platform] = cnt
			}
		}

		fc.Paths = append(fc.Paths, model.PathCoverage{
			Path:       p,
			TotalSteps: stepCounts[p.ID],
			Coverage:   coverage,
		})
	}

	return fc, nil
}
