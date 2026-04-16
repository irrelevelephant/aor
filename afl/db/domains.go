package db

import (
	"database/sql"
	"fmt"

	"aor/afl/model"
)

const domainCols = `id, workspace, slug, name, created_at, updated_at`

// CreateDomain inserts a new domain, generating a unique ID.
func (d *DB) CreateDomain(slug, name, workspace string) (*model.Domain, error) {
	id, err := d.generateUniqueIDForTable(3, "domains")
	if err != nil {
		return nil, err
	}

	_, err = d.Exec(`INSERT INTO domains (id, workspace, slug, name) VALUES (?, ?, ?, ?)`,
		id, workspace, slug, name)
	if err != nil {
		return nil, fmt.Errorf("insert domain: %w", err)
	}

	return d.GetDomain(id)
}

// GetDomain returns a single domain by ID.
func (d *DB) GetDomain(id string) (*model.Domain, error) {
	var dom model.Domain
	err := d.QueryRow(`SELECT `+domainCols+` FROM domains WHERE id = ?`, id).
		Scan(&dom.ID, &dom.Workspace, &dom.Slug, &dom.Name, &dom.CreatedAt, &dom.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("domain not found")
		}
		return nil, fmt.Errorf("scan domain: %w", err)
	}
	return &dom, nil
}

// GetDomainBySlug returns a domain by workspace and slug.
func (d *DB) GetDomainBySlug(workspace, slug string) (*model.Domain, error) {
	var dom model.Domain
	err := d.QueryRow(`SELECT `+domainCols+` FROM domains WHERE workspace = ? AND slug = ?`, workspace, slug).
		Scan(&dom.ID, &dom.Workspace, &dom.Slug, &dom.Name, &dom.CreatedAt, &dom.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan domain: %w", err)
	}
	return &dom, nil
}

// ListDomains returns all domains for a workspace.
func (d *DB) ListDomains(workspace string) ([]model.Domain, error) {
	rows, err := d.Query(`SELECT `+domainCols+` FROM domains WHERE workspace = ? ORDER BY slug`, workspace)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	var domains []model.Domain
	for rows.Next() {
		var dom model.Domain
		if err := rows.Scan(&dom.ID, &dom.Workspace, &dom.Slug, &dom.Name, &dom.CreatedAt, &dom.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan domain row: %w", err)
		}
		domains = append(domains, dom)
	}
	return domains, rows.Err()
}

// DeleteDomain deletes a domain by ID. Cascades to flows, paths, steps, screenshots.
func (d *DB) DeleteDomain(id string) error {
	res, err := d.Exec(`DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("domain %s not found", id)
	}
	return nil
}
