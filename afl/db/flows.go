package db

import (
	"database/sql"
	"fmt"

	"aor/afl/model"
)

const flowCols = `id, domain_id, flow_id, name, sort_order, created_at, updated_at`

// CreateFlow inserts a new flow, generating a unique ID.
func (d *DB) CreateFlow(domainID, flowID, name string) (*model.Flow, error) {
	id, err := d.generateUniqueIDForTable(3, "flows")
	if err != nil {
		return nil, err
	}

	// Place at end of domain's flows.
	var maxOrder int
	err = d.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) FROM flows WHERE domain_id = ?`, domainID).Scan(&maxOrder)
	if err != nil {
		return nil, fmt.Errorf("get max order: %w", err)
	}

	_, err = d.Exec(`INSERT INTO flows (id, domain_id, flow_id, name, sort_order) VALUES (?, ?, ?, ?, ?)`,
		id, domainID, flowID, name, maxOrder+1)
	if err != nil {
		return nil, fmt.Errorf("insert flow: %w", err)
	}

	return d.GetFlow(id)
}

// GetFlow returns a single flow by internal ID.
func (d *DB) GetFlow(id string) (*model.Flow, error) {
	var f model.Flow
	err := d.QueryRow(`SELECT `+flowCols+` FROM flows WHERE id = ?`, id).
		Scan(&f.ID, &f.DomainID, &f.FlowID, &f.Name, &f.SortOrder, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("flow not found")
		}
		return nil, fmt.Errorf("scan flow: %w", err)
	}
	return &f, nil
}

// GetFlowByFlowID returns a flow by domain ID and spec flow ID.
func (d *DB) GetFlowByFlowID(domainID, flowID string) (*model.Flow, error) {
	var f model.Flow
	err := d.QueryRow(`SELECT `+flowCols+` FROM flows WHERE domain_id = ? AND flow_id = ?`, domainID, flowID).
		Scan(&f.ID, &f.DomainID, &f.FlowID, &f.Name, &f.SortOrder, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan flow: %w", err)
	}
	return &f, nil
}

// ResolveFlow finds a flow by its spec flow ID across all domains.
func (d *DB) ResolveFlow(flowID string) (*model.Flow, error) {
	var f model.Flow
	err := d.QueryRow(`SELECT `+flowCols+` FROM flows WHERE flow_id = ?`, flowID).
		Scan(&f.ID, &f.DomainID, &f.FlowID, &f.Name, &f.SortOrder, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve flow: %w", err)
	}
	return &f, nil
}

// ListFlows returns all flows for a domain, ordered by sort_order.
func (d *DB) ListFlows(domainID string) ([]model.Flow, error) {
	rows, err := d.Query(`SELECT `+flowCols+` FROM flows WHERE domain_id = ? ORDER BY sort_order ASC, created_at ASC`, domainID)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	defer rows.Close()

	return scanFlowRows(rows)
}

// ListAllFlows returns all flows across all domains.
func (d *DB) ListAllFlows() ([]model.Flow, error) {
	rows, err := d.Query(`SELECT ` + prefixCols("f", flowCols) + ` FROM flows f
		JOIN domains d ON d.id = f.domain_id
		ORDER BY d.slug, f.sort_order ASC, f.created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all flows: %w", err)
	}
	defer rows.Close()

	return scanFlowRows(rows)
}

// DeleteFlow deletes a flow by ID. Cascades to paths, steps, screenshots.
func (d *DB) DeleteFlow(id string) error {
	res, err := d.Exec(`DELETE FROM flows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete flow: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("flow %s not found", id)
	}
	return nil
}

// UpdateFlowOrder updates a flow's sort order.
func (d *DB) UpdateFlowOrder(id string, order int) error {
	res, err := d.Exec(`UPDATE flows SET sort_order = ? WHERE id = ?`, order, id)
	if err != nil {
		return fmt.Errorf("update flow order: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("flow %s not found", id)
	}
	return nil
}

func scanFlowRows(rows *sql.Rows) ([]model.Flow, error) {
	var flows []model.Flow
	for rows.Next() {
		var f model.Flow
		if err := rows.Scan(&f.ID, &f.DomainID, &f.FlowID, &f.Name, &f.SortOrder, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan flow row: %w", err)
		}
		flows = append(flows, f)
	}
	return flows, rows.Err()
}
