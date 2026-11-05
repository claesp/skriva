package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	// UploadQuotaMB is the largest total size of the uploaded files, in
	// MiB. See SpaceFileBytes for the size now.
	UploadQuotaMB int
	// SearchResults is the largest number of search results from the
	// space. The search counts only the items that the user can read.
	SearchResults int
	CreatedAt     time.Time
	// Nodes is the number of folders and documents. LastChanged is the
	// time of the last change of a node, or nil for an empty space. Only
	// Spaces sets these fields.
	Nodes       int
	LastChanged *time.Time
}

// Limits of the space settings.
const (
	MinUploadQuotaMB     = 1
	MaxUploadQuotaMB     = 1 << 20
	DefaultUploadQuotaMB = 1024
	MinSearchResults     = 10
	MaxSearchResults     = 1000
	DefaultSearchResults = 50
)

// SpaceSettings are the fields of a space that an administrator can
// change.
type SpaceSettings struct {
	// Slug is the code of the space. It is part of the URLs.
	Slug          string
	Name          string
	Description   string
	UploadQuotaMB int
	SearchResults int
}

// spaceCols are the columns for scanSpace.
const spaceCols = `sp.id, sp.tenant_id, sp.slug, sp.name, sp.description, sp.default_role,
	sp.upload_quota_mb, sp.search_results, sp.created_at`

func spaceDest(sp *Space) []any {
	return []any{&sp.ID, &sp.TenantID, &sp.Slug, &sp.Name, &sp.Description, &sp.DefaultRole,
		&sp.UploadQuotaMB, &sp.SearchResults, &sp.CreatedAt}
}

// SpaceFileBytes returns the total size of the uploaded files of a space.
// The upload quota of the space applies to this size.
func (s *Store) SpaceFileBytes(ctx context.Context, spaceID int64) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, "SELECT COALESCE(sum(size), 0) FROM files WHERE space_id = $1", spaceID).Scan(&n)
	return n, err
}

// Spaces returns the spaces of a tenant, sorted by name.
func (s *Store) Spaces(ctx context.Context, tenantID int64) ([]Space, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+spaceCols+`,
		       (SELECT count(*) FROM nodes n WHERE n.space_id = sp.id AND n.deleted_at IS NULL),
		       (SELECT max(n.updated_at) FROM nodes n WHERE n.space_id = sp.id AND n.deleted_at IS NULL)
		FROM spaces sp WHERE sp.tenant_id = $1
		ORDER BY lower(sp.name), sp.slug`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		var sp Space
		if err := rows.Scan(append(spaceDest(&sp), &sp.Nodes, &sp.LastChanged)...); err != nil {
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
		SELECT `+spaceCols+` FROM spaces sp WHERE sp.tenant_id = $1 AND sp.slug = $2`, tenantID, slug,
	).Scan(spaceDest(&sp)...)
	if err != nil {
		return nil, notFound(err)
	}
	return &sp, nil
}

// CreateSpace makes a space. It returns ErrConflict if the tenant has a
// space with the same slug.
func (s *Store) CreateSpace(ctx context.Context, tenantID int64, slug, name, description string) error {
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO spaces (tenant_id, slug, name, description) VALUES ($1, $2, $3, $4) RETURNING id`,
			tenantID, slug, name, description).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{TenantID: &tenantID, SpaceID: id, Action: AuditSpaceCreate, Target: name})
	})
	switch sqlState(err) {
	case codeUniqueViolation:
		return ErrConflict
	case codeForeignKeyViolation:
		return ErrNotFound
	}
	return err
}

// UpdateSpace changes the settings of a space. The slug is part of the
// URLs, thus a new slug changes all URLs of the space. It returns
// ErrConflict if a different space of the tenant has the new slug, and
// ErrInvalid if a limit is out of range.
func (s *Store) UpdateSpace(ctx context.Context, tenantID int64, slug string, set SpaceSettings) error {
	if set.UploadQuotaMB < MinUploadQuotaMB || set.UploadQuotaMB > MaxUploadQuotaMB ||
		set.SearchResults < MinSearchResults || set.SearchResults > MaxSearchResults {
		return ErrInvalid
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var id int64
		var oldQuota, oldResults int
		err := tx.QueryRow(ctx, `
			SELECT id, upload_quota_mb, search_results FROM spaces WHERE tenant_id = $1 AND slug = $2 FOR UPDATE`,
			tenantID, slug).Scan(&id, &oldQuota, &oldResults)
		if err != nil {
			return notFound(err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE spaces SET slug = $2, name = $3, description = $4, upload_quota_mb = $5, search_results = $6
			WHERE id = $1`, id, set.Slug, set.Name, set.Description, set.UploadQuotaMB, set.SearchResults); err != nil {
			return err
		}
		var details []string
		if set.Slug != slug {
			details = append(details, "The code changed from "+slug+" to "+set.Slug+".")
		}
		if set.UploadQuotaMB != oldQuota {
			details = append(details, fmt.Sprintf("The upload limit changed from %d MiB to %d MiB.", oldQuota, set.UploadQuotaMB))
		}
		if set.SearchResults != oldResults {
			details = append(details, fmt.Sprintf("The search results changed from %d to %d.", oldResults, set.SearchResults))
		}
		return s.audit(ctx, tx, auditEntry{TenantID: &tenantID, SpaceID: id, Action: AuditSpaceUpdate, Target: set.Name,
			Details: strings.Join(details, " ")})
	})
	if sqlState(err) == codeUniqueViolation {
		return ErrConflict
	}
	return err
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
	return s.tx(ctx, func(tx pgx.Tx) error {
		var id int64
		var name string
		err := tx.QueryRow(ctx, `
			UPDATE spaces SET default_role = $3 WHERE tenant_id = $1 AND slug = $2 RETURNING id, name`,
			tenantID, slug, role).Scan(&id, &name)
		if err != nil {
			return notFound(err)
		}
		return s.audit(ctx, tx, auditEntry{TenantID: &tenantID, SpaceID: id, Action: AuditSpaceDefaultRole,
			Target: name, Details: roleText(role, "tenant")})
	})
}

// DeleteSpace removes a space with all its folders and documents.
func (s *Store) DeleteSpace(ctx context.Context, tenantID int64, slug string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var id int64
		var name string
		err := tx.QueryRow(ctx, "SELECT id, name FROM spaces WHERE tenant_id = $1 AND slug = $2 FOR UPDATE",
			tenantID, slug).Scan(&id, &name)
		if err != nil {
			return notFound(err)
		}
		// The event is written before the deletion, because it reads the
		// code of the space.
		if err := s.audit(ctx, tx, auditEntry{TenantID: &tenantID, SpaceID: id, Action: AuditSpaceDelete, Target: name}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "DELETE FROM spaces WHERE id = $1", id)
		return err
	})
}
