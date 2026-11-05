package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Classification is a level of information classification of a tenant.
type Classification struct {
	ID          int64
	Name        string
	Description string
	// Position gives the order of the levels, from the lowest to the
	// highest.
	Position  int
	IsDefault bool
	// Documents is the number of documents with the level, and Trashed
	// the number of documents with the level in the trash. A level with
	// documents cannot be deleted, also if the documents are in the
	// trash. Only Classifications sets them.
	Documents int
	Trashed   int
	// Settings is the number of spaces and folders that use the level as
	// their default or lowest level. Only Classifications sets it.
	Settings int
}

// InUse reports whether documents use the level, also in the trash, or
// whether a space or a folder uses it in its classification limits.
func (c *Classification) InUse() bool { return c.Documents > 0 || c.Trashed > 0 || c.Settings > 0 }

// Classifications returns the levels of a tenant in their order.
func (s *Store) Classifications(ctx context.Context, tenantID int64) ([]Classification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.description, c.position, c.is_default,
		       (SELECT count(*) FROM nodes n WHERE n.classification_id = c.id AND n.deleted_at IS NULL),
		       (SELECT count(*) FROM nodes n WHERE n.classification_id = c.id AND n.deleted_at IS NOT NULL),
		       (SELECT count(*) FROM spaces sp WHERE c.id IN (sp.default_classification_id, sp.min_classification_id)) +
		       (SELECT count(*) FROM nodes n WHERE c.id IN (n.default_classification_id, n.min_classification_id))
		FROM classifications c WHERE c.tenant_id = $1
		ORDER BY c.position, lower(c.name), c.id`, tenantID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Classification, error) {
		var c Classification
		err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Position, &c.IsDefault, &c.Documents, &c.Trashed, &c.Settings)
		return c, err
	})
}

// Classification returns one level of a tenant.
func (s *Store) Classification(ctx context.Context, tenantID, id int64) (*Classification, error) {
	var c Classification
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, position, is_default FROM classifications
		WHERE tenant_id = $1 AND id = $2`, tenantID, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.Position, &c.IsDefault)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// SaveClassification makes a level (c.ID is 0) or changes a level. If
// c.IsDefault is true, the level becomes the default and the old default
// stops being the default. The default cannot be removed: a change that
// sets IsDefault to false on the default does not change the default. It
// returns ErrConflict if the tenant has a level with the same name.
func (s *Store) SaveClassification(ctx context.Context, tenantID int64, c Classification) (int64, error) {
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if c.IsDefault {
			if _, err := tx.Exec(ctx, `
				UPDATE classifications SET is_default = false
				WHERE tenant_id = $1 AND is_default AND id <> $2`, tenantID, c.ID); err != nil {
				return err
			}
		}
		action := AuditClassificationUpdate
		if c.ID == 0 {
			action = AuditClassificationCreate
			if err := tx.QueryRow(ctx, `
				INSERT INTO classifications (tenant_id, name, description, position, is_default)
				VALUES ($1, $2, $3, $4, $5) RETURNING id`,
				tenantID, c.Name, c.Description, c.Position, c.IsDefault).Scan(&c.ID); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `
				UPDATE classifications SET name = $3, description = $4, position = $5,
				       is_default = is_default OR $6
				WHERE tenant_id = $1 AND id = $2`,
				tenantID, c.ID, c.Name, c.Description, c.Position, c.IsDefault)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		details := ""
		if c.IsDefault {
			details = "The default level."
		}
		return s.audit(ctx, tx, auditEntry{TenantID: &tenantID, Action: action, Target: c.Name, Details: details})
	})
	if sqlState(err) == codeUniqueViolation {
		return 0, ErrConflict
	}
	return c.ID, err
}

// DeleteClassification removes a level of a tenant. It returns ErrInvalid
// for the default level, and ErrConflict if documents use the level or a
// space or a folder uses it in its classification limits.
func (s *Store) DeleteClassification(ctx context.Context, tenantID, id int64) error {
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var name string
		var isDefault bool
		if err := tx.QueryRow(ctx, `
			SELECT name, is_default FROM classifications WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			tenantID, id).Scan(&name, &isDefault); err != nil {
			return notFound(err)
		}
		if isDefault {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, "DELETE FROM classifications WHERE id = $1", id); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{TenantID: &tenantID, Action: AuditClassificationDelete, Target: name})
	})
	if sqlState(err) == codeForeignKeyViolation {
		return ErrConflict
	}
	return err
}
