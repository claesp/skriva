package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrClassification tells that the classification of a document is lower
// than the lowest level that the space or a folder permits. The error of
// the store is a *ClassificationError.
var ErrClassification = errors.New("the classification is lower than the lowest permitted level")

// ClassificationError tells which level is the lowest permitted level,
// and how many documents have a lower level.
type ClassificationError struct {
	// Min is the name of the lowest permitted level.
	Min string
	// Documents is the number of documents with a lower level, or 0 if
	// the error is about one document or one setting.
	Documents int
}

func (e *ClassificationError) Error() string {
	if e.Documents > 0 {
		return fmt.Sprintf("%d documents have a classification lower than %s", e.Documents, e.Min)
	}
	return "the classification must be " + e.Min + " or higher"
}

func (e *ClassificationError) Is(target error) bool { return target == ErrClassification }

// ClassLevel is a level of a ClassLimits value. ID 0 means no level.
type ClassLevel struct {
	ID       int64
	Name     string
	Position int
}

// ClassLimits are the classification limits for the documents at one
// place: in a folder, or at the top level of a space.
type ClassLimits struct {
	// Min is the lowest permitted level, or ID 0 for no limit. It is the
	// highest of the lowest levels of the space and of the folders above
	// the place.
	Min ClassLevel
	// Default is the level of a new document. It is the default level of
	// the nearest folder with a default level, else of the space, else of
	// the tenant. If that level is lower than Min, Default is Min.
	Default ClassLevel
}

// Permits reports whether a document with the level c can be at the
// place.
func (l *ClassLimits) Permits(c ClassLevel) bool {
	return l.Min.ID == 0 || c.Position >= l.Min.Position
}

// ClassificationLimits returns the classification limits for a document
// in the folder parentID, or at the top level of the space if parentID is
// nil.
func (s *Store) ClassificationLimits(ctx context.Context, spaceID int64, parentID *int64) (*ClassLimits, error) {
	l, _, err := classLimits(ctx, s.pool, spaceID, parentID)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// classLimits calculates the limits at a place. It also returns the
// levels of the tenant of the space by ID.
func classLimits(ctx context.Context, q querier, spaceID int64, parentID *int64) (ClassLimits, map[int64]ClassLevel, error) {
	var l ClassLimits
	rows, err := q.Query(ctx, `
		SELECT c.id, c.name, c.position, c.is_default
		FROM classifications c JOIN spaces sp ON sp.tenant_id = c.tenant_id WHERE sp.id = $1`, spaceID)
	if err != nil {
		return l, nil, err
	}
	levels := map[int64]ClassLevel{}
	var tenantDefault ClassLevel
	var c ClassLevel
	var isDefault bool
	if _, err := pgx.ForEachRow(rows, []any{&c.ID, &c.Name, &c.Position, &isDefault}, func() error {
		levels[c.ID] = c
		if isDefault {
			tenantDefault = c
		}
		return nil
	}); err != nil {
		return l, nil, err
	}

	// The settings from the place to the top: the folders, then the
	// space.
	type setting struct{ def, min *int64 }
	var chain []setting
	if parentID != nil {
		rows, err := q.Query(ctx, `
			WITH RECURSIVE up(id, parent_id, def, mn, depth) AS (
				SELECT id, parent_id, default_classification_id, min_classification_id, 0
				FROM nodes WHERE id = $1 AND space_id = $2
				UNION ALL
				SELECT n.id, n.parent_id, n.default_classification_id, n.min_classification_id, up.depth + 1
				FROM nodes n JOIN up ON n.id = up.parent_id
				WHERE up.depth < 1000
			)
			SELECT def, mn FROM up ORDER BY depth`, *parentID, spaceID)
		if err != nil {
			return l, nil, err
		}
		var st setting
		if _, err := pgx.ForEachRow(rows, []any{&st.def, &st.min}, func() error {
			chain = append(chain, st)
			return nil
		}); err != nil {
			return l, nil, err
		}
	}
	var sp setting
	if err := q.QueryRow(ctx, "SELECT default_classification_id, min_classification_id FROM spaces WHERE id = $1",
		spaceID).Scan(&sp.def, &sp.min); err != nil {
		return l, nil, notFound(err)
	}
	chain = append(chain, sp)

	for _, st := range chain {
		if st.min != nil {
			if m := levels[*st.min]; l.Min.ID == 0 || m.Position > l.Min.Position {
				l.Min = m
			}
		}
		if st.def != nil && l.Default.ID == 0 {
			l.Default = levels[*st.def]
		}
	}
	if l.Default.ID == 0 {
		l.Default = tenantDefault
	}
	if !l.Permits(l.Default) {
		l.Default = l.Min
	}
	return l, levels, nil
}

// checkSubtree returns a *ClassificationError if a document in the
// subtree of the node rootID has a level lower than min. A nil rootID
// means all documents of the space. trashed selects the subtree of an
// item in the trash (trash_id) instead of the items that are not in the
// trash.
func checkSubtree(ctx context.Context, q querier, spaceID int64, rootID *int64, trashed bool, min ClassLevel) error {
	if min.ID == 0 {
		return nil
	}
	var n int
	var err error
	switch {
	case trashed:
		err = q.QueryRow(ctx, `
			SELECT count(*) FROM nodes n JOIN classifications c ON c.id = n.classification_id
			WHERE n.space_id = $1 AND n.trash_id = $2 AND c.position < $3`, spaceID, *rootID, min.Position).Scan(&n)
	case rootID == nil:
		err = q.QueryRow(ctx, `
			SELECT count(*) FROM nodes n JOIN classifications c ON c.id = n.classification_id
			WHERE n.space_id = $1 AND n.deleted_at IS NULL AND c.position < $2`, spaceID, min.Position).Scan(&n)
	default:
		err = q.QueryRow(ctx, `
			WITH RECURSIVE sub(id) AS (
				SELECT $2::bigint
				UNION
				SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted_at IS NULL
			)
			SELECT count(*) FROM nodes n JOIN sub ON sub.id = n.id
			JOIN classifications c ON c.id = n.classification_id
			WHERE n.space_id = $1 AND n.deleted_at IS NULL AND c.position < $3`, spaceID, *rootID, min.Position).Scan(&n)
	}
	if err != nil {
		return err
	}
	if n > 0 {
		return &ClassificationError{Min: min.Name, Documents: n}
	}
	return nil
}

// FolderClassification is the classification setting of a folder. ID 0
// means "the same as the parent".
type FolderClassification struct {
	DefaultID int64
	MinID     int64
}

// SetFolderClassification sets the default and the lowest classification
// of the documents in a folder. A folder can only make the limits of its
// parents stricter:
//   - The lowest level must be the same as the lowest level of the
//     parents, or higher.
//   - The default level must be permitted by the lowest levels of the
//     parents and of the folder.
//   - All documents in the folder must have the lowest level or a higher
//     level.
//
// It returns a *ClassificationError if a check fails, ErrInvalid if the
// node is not a folder or a level is not a level of the tenant, and
// ErrNotFound if the folder does not exist.
func (s *Store) SetFolderClassification(ctx context.Context, spaceID int64, publicID string, set FolderClassification) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		var parentID *int64
		var kind string
		var sealedTitle []byte
		if err := tx.QueryRow(ctx, `
			SELECT id, parent_id, kind, title FROM nodes WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL`,
			spaceID, publicID).Scan(&id, &parentID, &kind, &sealedTitle); err != nil {
			return notFound(err)
		}
		if kind != KindFolder {
			return ErrInvalid
		}
		inherited, levels, err := classLimits(ctx, tx, spaceID, parentID)
		if err != nil {
			return err
		}
		lookup := func(id int64) (*int64, ClassLevel, error) {
			if id == 0 {
				return nil, ClassLevel{}, nil
			}
			c, ok := levels[id]
			if !ok {
				return nil, c, ErrInvalid
			}
			return &id, c, nil
		}
		defID, def, err := lookup(set.DefaultID)
		if err != nil {
			return err
		}
		minID, min, err := lookup(set.MinID)
		if err != nil {
			return err
		}
		eff := inherited
		if minID != nil {
			if !inherited.Permits(min) {
				return &ClassificationError{Min: inherited.Min.Name}
			}
			eff.Min = min
		}
		if defID != nil && !eff.Permits(def) {
			return &ClassificationError{Min: eff.Min.Name}
		}
		if err := checkSubtree(ctx, tx, spaceID, &id, false, eff.Min); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE nodes SET default_classification_id = $2, min_classification_id = $3 WHERE id = $1`,
			id, defID, minID); err != nil {
			return err
		}
		title, err := s.openText(ctx, spaceID, titlePlace(spaceID, publicID), sealedTitle)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodeClassLimits, Target: title,
			TargetID: publicID, Details: classLimitsText(defID, def, minID, min, "parent")})
	})
}

// classLimitsText describes a classification setting for the audit log.
func classLimitsText(defID *int64, def ClassLevel, minID *int64, min ClassLevel, parent string) string {
	d := "the same as the " + parent
	if parent == "tenant" {
		d = "the default level of the tenant"
	}
	if defID != nil {
		d = def.Name
	}
	m := "the same as the " + parent
	if minID != nil {
		m = min.Name
	}
	if parent == "tenant" {
		m = "no limit"
		if minID != nil {
			m = min.Name
		}
	}
	return "Default level: " + d + ". Lowest level: " + m + "."
}

// permittedClass returns the level for a document in the folder parentID
// (nil for the top level): the level id, or the default level of the
// place if id is 0. It returns ErrInvalid if id is not a level of the
// tenant of the space, and a *ClassificationError if the place does not
// permit the level.
func permittedClass(ctx context.Context, q querier, spaceID int64, parentID *int64, id int64) (ClassLevel, error) {
	l, levels, err := classLimits(ctx, q, spaceID, parentID)
	if err != nil {
		return ClassLevel{}, err
	}
	if id == 0 {
		return l.Default, nil
	}
	c, ok := levels[id]
	if !ok {
		return c, ErrInvalid
	}
	if !l.Permits(c) {
		return c, &ClassificationError{Min: l.Min.Name}
	}
	return c, nil
}
