package store

import (
	"context"
	"time"
)

// Space is a tree of folders and documents in a tenant.
type Space struct {
	ID          int64
	TenantID    int64
	Slug        string
	Name        string
	Description string
	// DefaultRole is the default role in the space. Nil means that the
	// space uses the default role of the tenant. An empty value means no
	// default role.
	DefaultRole *string
	CreatedAt   time.Time
	// Nodes is the number of folders and documents. LastChanged is the
	// time of the last change of a node, or nil for an empty space. Only
	// Spaces sets these fields.
	Nodes       int
	LastChanged *time.Time
}

// Spaces returns the spaces of a tenant, sorted by name.
func (s *Store) Spaces(ctx context.Context, tenantID int64) ([]Space, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sp.id, sp.tenant_id, sp.slug, sp.name, sp.description, sp.default_role, sp.created_at,
		       (SELECT count(*) FROM nodes n WHERE n.space_id = sp.id),
		       (SELECT max(n.updated_at) FROM nodes n WHERE n.space_id = sp.id)
		FROM spaces sp WHERE sp.tenant_id = $1
		ORDER BY lower(sp.name), sp.slug`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		var sp Space
		if err := rows.Scan(&sp.ID, &sp.TenantID, &sp.Slug, &sp.Name, &sp.Description,
			&sp.DefaultRole, &sp.CreatedAt, &sp.Nodes, &sp.LastChanged); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// Space returns the space with the given slug in a tenant.
func (s *Store) Space(ctx context.Context, tenantID int64, slug string) (*Space, error) {
	var sp Space
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, slug, name, description, default_role, created_at
		FROM spaces WHERE tenant_id = $1 AND slug = $2`, tenantID, slug,
	).Scan(&sp.ID, &sp.TenantID, &sp.Slug, &sp.Name, &sp.Description, &sp.DefaultRole, &sp.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &sp, nil
}

// CreateSpace makes a space. It returns ErrConflict if the tenant has a
// space with the same slug.
func (s *Store) CreateSpace(ctx context.Context, tenantID int64, slug, name, description string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO spaces (tenant_id, slug, name, description) VALUES ($1, $2, $3, $4)`,
		tenantID, slug, name, description)
	switch sqlState(err) {
	case codeUniqueViolation:
		return ErrConflict
	case codeForeignKeyViolation:
		return ErrNotFound
	}
	return err
}

// UpdateSpace changes the name and the description of a space. The slug
// is permanent because it is part of the URLs.
func (s *Store) UpdateSpace(ctx context.Context, tenantID int64, slug, name, description string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE spaces SET name = $3, description = $4 WHERE tenant_id = $1 AND slug = $2`,
		tenantID, slug, name, description)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSpaceDefaultRole sets the default role in a space. Nil means that
// the space uses the default role of the tenant. A pointer to "" means no
// default role. It returns ErrInvalid if the role does not exist.
func (s *Store) SetSpaceDefaultRole(ctx context.Context, tenantID int64, slug string, role *string) error {
	if role != nil {
		if err := checkRole(ctx, s.pool, tenantID, *role); err != nil {
			return err
		}
	}
	tag, err := s.pool.Exec(ctx, "UPDATE spaces SET default_role = $3 WHERE tenant_id = $1 AND slug = $2",
		tenantID, slug, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSpace removes a space with all its folders and documents.
func (s *Store) DeleteSpace(ctx context.Context, tenantID int64, slug string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM spaces WHERE tenant_id = $1 AND slug = $2", tenantID, slug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
