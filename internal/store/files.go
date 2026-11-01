package store

import (
	"context"
	"time"
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

// CreateFile saves an uploaded file in a space. It returns the public ID
// of the file. The caller must check the content type and the size.
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
	_, err = s.pool.Exec(ctx, `
		INSERT INTO files (space_id, public_id, content_type, data, key_version, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, spaceID, publicID, contentType, sealed, version, nullID(userID), s.now())
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
