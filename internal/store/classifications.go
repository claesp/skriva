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
}

// InUse reports whether documents use the level, also in the trash.
func (c *Classification) InUse() bool { return c.Documents > 0 || c.Trashed > 0 }

// Classifications returns the levels of a tenant in their order.
func (s *Store) Classifications(ctx context.Context, tenantID int64) ([]Classification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.description, c.position, c.is_default,
		       (SELECT count(*) FROM nodes n WHERE n.classification_id = c.id AND n.deleted_at IS NULL),
		       (SELECT count(*) FROM nodes n WHERE n.classification_id = c.id AND n.deleted_at IS NOT NULL)
		FROM classifications c WHERE c.tenant_id = $1
		ORDER BY c.position, lower(c.name), c.id`, tenantID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Classification, error) {
		var c Classification
		err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Position, &c.IsDefault, &c.Documents, &c.Trashed)
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
// for the default level, and ErrConflict if documents use the level.
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

// classificationFor returns the level for a document in a space: the
// level with the given ID, or the default level of the tenant if id is
// 0. It returns ErrInvalid if the level does not belong to the tenant of
// the space. Thus a document cannot have a level of a different tenant.
func classificationFor(ctx context.Context, q querier, spaceID, id int64) (int64, string, error) {
	var out int64
	var name string
	err := q.QueryRow(ctx, `
		SELECT c.id, c.name FROM classifications c JOIN spaces sp ON sp.tenant_id = c.tenant_id
		WHERE sp.id = $1 AND (c.id = $2 OR ($2 = 0 AND c.is_default))`, spaceID, id).Scan(&out, &name)
	if err != nil {
		if notFound(err) == ErrNotFound {
			return 0, "", ErrInvalid
		}
		return 0, "", err
	}
	return out, name, nil
}
