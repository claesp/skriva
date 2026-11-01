package store

import (
	"context"
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
	ParentTitle string
	// ParentInTrash is true if the old parent is also in the trash.
	ParentInTrash bool
}

// IsFolder reports whether the item is a folder.
func (t *TrashItem) IsFolder() bool { return t.Kind == KindFolder }

// ExpiresAt returns the time at which DeleteExpired removes the item.
func (t *TrashItem) ExpiresAt() time.Time { return t.DeletedAt.AddDate(0, 0, TrashDays) }

// TrashNode moves a node and its subtree into the trash. The items of the
// subtree that are in the trash already stay separate items. It returns
// ErrNotFound if the node does not exist or is in the trash.
func (s *Store) TrashNode(ctx context.Context, spaceID int64, publicID string, userID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			SELECT id FROM nodes WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL`,
			spaceID, publicID).Scan(&id); err != nil {
			return notFound(err)
		}
		_, err := tx.Exec(ctx, `
			WITH RECURSIVE sub(id) AS (
				SELECT $1::bigint
				UNION
				SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted_at IS NULL
			)
			UPDATE nodes n SET deleted_at = $2, deleted_by = $3, trash_id = $1
			FROM sub WHERE n.id = sub.id`, id, s.now(), nullID(userID))
		return err
	})
}

// trashSelect reads TrashItem values. The query must continue with a
// WHERE clause.
const trashSelect = `
	SELECT n.id, n.public_id, n.kind, n.title, n.access_path_id, n.deleted_at,
	       COALESCE(NULLIF(u.display_name, ''), u.username, ''),
	       (SELECT count(*) FROM nodes d WHERE d.trash_id = n.id AND d.id <> n.id),
	       COALESCE(p.title, ''), COALESCE(p.deleted_at IS NOT NULL, false)
	FROM nodes n
	LEFT JOIN users u ON u.id = n.deleted_by
	LEFT JOIN nodes p ON p.id = n.parent_id
`

func scanTrashItem(row pgx.CollectableRow) (TrashItem, error) {
	var t TrashItem
	err := row.Scan(&t.ID, &t.PublicID, &t.Kind, &t.Title, &t.AccessPathID, &t.DeletedAt,
		&t.DeletedBy, &t.Descendants, &t.ParentTitle, &t.ParentInTrash)
	return t, err
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
	return pgx.CollectRows(rows, scanTrashItem)
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
	return &t, nil
}

// RestoreNode moves an item and its subtree out of the trash, back to
// their old place. It returns ErrNotFound if the item is not in the trash,
// and ErrInvalid if the old parent is in the trash. Then the user must
// restore the parent first. An item cannot go to a different place,
// because the place controls the permissions.
func (s *Store) RestoreNode(ctx context.Context, spaceID int64, publicID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		var parentInTrash bool
		err := tx.QueryRow(ctx, `
			SELECT n.id, COALESCE(p.deleted_at IS NOT NULL, false)
			FROM nodes n LEFT JOIN nodes p ON p.id = n.parent_id
			WHERE n.space_id = $1 AND n.public_id = $2 AND n.trash_id = n.id`,
			spaceID, publicID).Scan(&id, &parentInTrash)
		if err != nil {
			return notFound(err)
		}
		if parentInTrash {
			return ErrInvalid
		}
		_, err = tx.Exec(ctx, `
			UPDATE nodes SET deleted_at = NULL, deleted_by = NULL, trash_id = NULL
			WHERE space_id = $1 AND trash_id = $2`, spaceID, id)
		return err
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
		tag, err := tx.Exec(ctx, `
			DELETE FROM nodes WHERE space_id = $1 AND public_id = $2 AND trash_id = id`, spaceID, publicID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
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
	for _, spaceID := range spaces {
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if err := lockSpace(ctx, tx, spaceID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				DELETE FROM nodes WHERE space_id = $1 AND trash_id = id AND deleted_at < $2`,
				spaceID, before); err != nil {
				return err
			}
			return deleteUnusedPaths(ctx, tx, spaceID)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
