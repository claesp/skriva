package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// File is an uploaded file.
type File struct {
	ID          int64
	PublicID    string
	ContentType string
	Data        []byte
	// CreatedBy is the ID of the user that uploaded the file, or 0.
	CreatedBy int64
	CreatedAt time.Time
	// AccessPaths are the access paths of the nodes that use the file.
	// A user can read the file if the user can read one of these nodes.
	AccessPaths []int64
}

// ErrQuota tells that the files of the space use all of the upload quota
// of the space (Space.UploadQuotaMB).
var ErrQuota = errors.New("the upload quota of the space is full")

// CreateFile saves an uploaded file in a space. It returns the public ID
// of the file. The caller must check the content type and the size. It
// returns ErrQuota if the files of the space and the new file are larger
// than the upload quota of the space.
func (s *Store) CreateFile(ctx context.Context, spaceID int64, contentType string, data []byte, userID int64) (string, error) {
	publicID := NewPublicID()
	tenantID, err := s.tenantOfSpace(ctx, spaceID)
	if err != nil {
		return "", err
	}
	sealed, version, err := s.seal(ctx, tenantID, filePlace(spaceID, publicID), data)
	if err != nil {
		return "", err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// The lock makes sure that two uploads at the same time cannot
		// both use the last part of the quota.
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var room bool
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT COALESCE(sum(size), 0) FROM files WHERE space_id = $1) + $2
			       <= upload_quota_mb::bigint << 20
			FROM spaces WHERE id = $1`, spaceID, len(data)).Scan(&room); err != nil {
			return err
		}
		if !room {
			return ErrQuota
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO files (space_id, public_id, content_type, data, size, key_version, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			spaceID, publicID, contentType, sealed, len(data), version, nullID(userID), s.now())
		return err
	})
	if err != nil {
		return "", err
	}
	return publicID, nil
}

// File returns a file of a space with the access paths of the nodes that
// use it.
func (s *Store) File(ctx context.Context, spaceID int64, publicID string) (*File, error) {
	var f File
	var createdBy *int64
	err := s.pool.QueryRow(ctx, `
		SELECT f.id, f.public_id, f.content_type, f.data, f.created_by, f.created_at,
		       ARRAY(SELECT DISTINCT n.access_path_id FROM file_refs r JOIN nodes n ON n.id = r.node_id
		             WHERE r.file_id = f.id AND n.deleted_at IS NULL)
		FROM files f WHERE f.space_id = $1 AND f.public_id = $2`, spaceID, publicID,
	).Scan(&f.ID, &f.PublicID, &f.ContentType, &f.Data, &createdBy, &f.CreatedAt, &f.AccessPaths)
	if err != nil {
		return nil, notFound(err)
	}
	if createdBy != nil {
		f.CreatedBy = *createdBy
	}
	if f.Data, err = s.openBytes(ctx, spaceID, filePlace(spaceID, f.PublicID), f.Data); err != nil {
		return nil, err
	}
	return &f, nil
}
