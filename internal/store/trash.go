package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TrashDays is the number of days that an item stays in the trash. Then
// DeleteExpired removes it permanently.
const TrashDays = 30

// TrashItem is a folder or a document that a user deleted. The subtree of
// the item is in the trash with it.
type TrashItem struct {
	ID           int64
	PublicID     string
	Kind         string
	Title        string
	AccessPathID int64
	DeletedAt    time.Time
	// DeletedBy is the name of the user that deleted the item. It is
	// empty if the user does not exist now.
	DeletedBy string
	// Descendants is the number of items in the subtree that went into
	// the trash with the item.
	Descendants int
	// ParentTitle is the title of the old parent, or "" for the top level.
	// The caller must remove it if the user cannot read the parent.
	ParentTitle string
	// ParentAccessPathID is the access path of the old parent, or 0.
	ParentAccessPathID int64
	// ParentInTrash is true if the old parent is also in the trash.
	ParentInTrash bool
}

// IsFolder reports whether the item is a folder.
func (t *TrashItem) IsFolder() bool { return t.Kind == KindFolder }

// TrashNode moves a node and its subtree into the trash. The items of the
// subtree that are in the trash already stay separate items. It returns
// ErrNotFound if the node does not exist or is in the trash.
func (s *Store) TrashNode(ctx context.Context, spaceID int64, publicID string, userID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		var sealedTitle []byte
		if err := tx.QueryRow(ctx, `
			SELECT id, title FROM nodes WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL`,
			spaceID, publicID).Scan(&id, &sealedTitle); err != nil {
			return notFound(err)
		}
		title, err := s.openText(ctx, spaceID, titlePlace(spaceID, publicID), sealedTitle)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			WITH RECURSIVE sub(id) AS (
				SELECT $1::bigint
				UNION
				SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted_at IS NULL
			)
			UPDATE nodes n SET deleted_at = $2, deleted_by = $3, trash_id = $1
			FROM sub WHERE n.id = sub.id`, id, s.now(), nullID(userID))
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodeTrash, Target: title,
			TargetID: publicID, Details: itemCount(tag.RowsAffected() - 1)})
	})
}

// trashSelect reads TrashItem values with scanTrashItem. The query must
// continue with a WHERE clause.
const trashSelect = `
	SELECT n.id, n.public_id, n.space_id, n.kind, n.title, n.access_path_id, n.deleted_at,
	       COALESCE(NULLIF(u.display_name, ''), u.username, ''),
	       (SELECT count(*) FROM nodes d WHERE d.trash_id = n.id AND d.id <> n.id),
	       p.public_id, p.title, COALESCE(p.access_path_id, 0), COALESCE(p.deleted_at IS NOT NULL, false)
	FROM nodes n
	LEFT JOIN users u ON u.id = n.deleted_by
	LEFT JOIN nodes p ON p.id = n.parent_id
`

// trashRow is a TrashItem with the encrypted titles.
type trashRow struct {
	TrashItem
	spaceID       int64
	title, parent []byte
	parentID      *string
}

func scanTrashItem(row pgx.CollectableRow) (trashRow, error) {
	var t trashRow
	err := row.Scan(&t.ID, &t.PublicID, &t.spaceID, &t.Kind, &t.title, &t.AccessPathID, &t.DeletedAt,
		&t.DeletedBy, &t.Descendants, &t.parentID, &t.parent, &t.ParentAccessPathID, &t.ParentInTrash)
	return t, err
}

// openTrashItem decrypts the titles of an item in the trash.
func (s *Store) openTrashItem(ctx context.Context, t *trashRow) error {
	var err error
	if t.Title, err = s.openText(ctx, t.spaceID, titlePlace(t.spaceID, t.PublicID), t.title); err != nil {
		return err
	}
	if t.parentID != nil {
		t.ParentTitle, err = s.openText(ctx, t.spaceID, titlePlace(t.spaceID, *t.parentID), t.parent)
	}
	return err
}

// MaxTrashItems is the maximum number of items that Trash returns.
const MaxTrashItems = 1000

// Trash returns the items in the trash of a space, newest first. The
// caller must remove the items that the user cannot see.
func (s *Store) Trash(ctx context.Context, spaceID int64) ([]TrashItem, error) {
	rows, err := s.pool.Query(ctx, trashSelect+`
		WHERE n.space_id = $1 AND n.trash_id = n.id
		ORDER BY n.deleted_at DESC, n.id DESC
		LIMIT $2`, spaceID, MaxTrashItems)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, scanTrashItem)
	if err != nil {
		return nil, err
	}
	out := make([]TrashItem, 0, len(list))
	for i := range list {
		if err := s.openTrashItem(ctx, &list[i]); err != nil {
			return nil, err
		}
		out = append(out, list[i].TrashItem)
	}
	return out, nil
}

// TrashedNode returns one item in the trash of a space.
func (s *Store) TrashedNode(ctx context.Context, spaceID int64, publicID string) (*TrashItem, error) {
	rows, err := s.pool.Query(ctx, trashSelect+`
		WHERE n.space_id = $1 AND n.public_id = $2 AND n.trash_id = n.id`, spaceID, publicID)
	if err != nil {
		return nil, err
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanTrashItem)
	if err != nil {
		return nil, notFound(err)
	}
	if err := s.openTrashItem(ctx, &t); err != nil {
		return nil, err
	}
	return &t.TrashItem, nil
}

// RestoreNode moves an item and its subtree out of the trash, back to
// their old place. It returns ErrNotFound if the item is not in the trash,
// a *ClassificationError if a document has a level lower than the old
// place permits now, and ErrInvalid if the old parent is in the trash. Then the user must
// restore the parent first. An item cannot go to a different place,
// because the place controls the permissions.
func (s *Store) RestoreNode(ctx context.Context, spaceID int64, publicID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		var parentID *int64
		var sealedTitle []byte
		var parentInTrash bool
		err := tx.QueryRow(ctx, `
			SELECT n.id, n.parent_id, n.title, COALESCE(p.deleted_at IS NOT NULL, false)
			FROM nodes n LEFT JOIN nodes p ON p.id = n.parent_id
			WHERE n.space_id = $1 AND n.public_id = $2 AND n.trash_id = n.id`,
			spaceID, publicID).Scan(&id, &parentID, &sealedTitle, &parentInTrash)
		if err != nil {
			return notFound(err)
		}
		title, err := s.openText(ctx, spaceID, titlePlace(spaceID, publicID), sealedTitle)
		if err != nil {
			return err
		}
		if parentInTrash {
			return ErrInvalid
		}
		// The limits of the old place can be stricter now.
		limits, _, err := classLimits(ctx, tx, spaceID, parentID)
		if err != nil {
			return err
		}
		if err := checkSubtree(ctx, tx, spaceID, &id, true, limits.Min); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE nodes SET deleted_at = NULL, deleted_by = NULL, trash_id = NULL
			WHERE space_id = $1 AND trash_id = $2`, spaceID, id)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodeRestore, Target: title,
			TargetID: publicID, Details: itemCount(tag.RowsAffected() - 1)})
	})
}

// PurgeNode removes an item in the trash permanently, with its subtree and
// all revisions. The subtree can contain items that are separate items in
// the trash. The function also removes these items.
func (s *Store) PurgeNode(ctx context.Context, spaceID int64, publicID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var sealedTitle []byte
		err := tx.QueryRow(ctx, `
			DELETE FROM nodes WHERE space_id = $1 AND public_id = $2 AND trash_id = id RETURNING title`,
			spaceID, publicID).Scan(&sealedTitle)
		if err != nil {
			return notFound(err)
		}
		title, err := s.openText(ctx, spaceID, titlePlace(spaceID, publicID), sealedTitle)
		if err != nil {
			return err
		}
		if err := s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodePurge, Target: title,
			TargetID: publicID}); err != nil {
			return err
		}
		return deleteUnusedPaths(ctx, tx, spaceID)
	})
}

// purgeTrash removes the items that are in the trash for more than
// TrashDays days. Each space has its own transaction with the lock on the
// space.
func (s *Store) purgeTrash(ctx context.Context) error {
	before := s.now().AddDate(0, 0, -TrashDays)
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT space_id FROM nodes WHERE trash_id = id AND deleted_at < $1`, before)
	if err != nil {
		return err
	}
	spaces, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	var errs []error
	for _, spaceID := range spaces {
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if err := lockSpace(ctx, tx, spaceID); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `
				DELETE FROM nodes WHERE space_id = $1 AND trash_id = id AND deleted_at < $2
				RETURNING public_id, title`, spaceID, before)
			if err != nil {
				return err
			}
			type item struct {
				id    string
				title []byte
			}
			items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
				var it item
				err := row.Scan(&it.id, &it.title)
				return it, err
			})
			if err != nil {
				return err
			}
			// The cleanup task has no actor. Thus the log shows "System".
			for _, it := range items {
				title, err := s.openText(ctx, spaceID, titlePlace(spaceID, it.id), it.title)
				if errors.Is(err, ErrKey) {
					// Delete the item also if the title is not readable.
					title = "(The title cannot be decrypted.)"
				} else if err != nil {
					return err
				}
				if err := s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodePurge, Target: title,
					TargetID: it.id, Details: fmt.Sprintf("After %d days in the trash.", TrashDays)}); err != nil {
					return err
				}
			}
			return deleteUnusedPaths(ctx, tx, spaceID)
		})
		if err != nil {
			// Try the other spaces also.
			errs = append(errs, fmt.Errorf("space %d: %w", spaceID, err))
		}
	}
	return errors.Join(errs...)
}

// itemCount describes the number of items in a subtree for the audit log.
func itemCount(n int64) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "With 1 item in it."
	}
	return fmt.Sprintf("With %d items in it.", n)
}
