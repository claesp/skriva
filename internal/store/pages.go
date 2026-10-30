package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/clpe/skriva/internal/db"
)

// Page is the current version of a wiki page.
type Page struct {
	ID        int64
	Slug      string
	Title     string
	Body      string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	// UpdatedBy is the name of the last author. It is empty if the user
	// does not exist now.
	UpdatedBy string
}

// PageSummary is a short form of a page for lists.
type PageSummary struct {
	Slug      string
	Title     string
	UpdatedAt time.Time
	// Snippet is a part of the text. Only search results have it.
	Snippet string
}

// Revision is one saved version of a page.
type Revision struct {
	Version   int
	Title     string
	Body      string
	Author    string
	Comment   string
	CreatedAt time.Time
}

// Page returns the page with the given slug.
func (s *Store) Page(ctx context.Context, slug string) (*Page, error) {
	var p Page
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.slug, p.title, p.body, p.version, p.created_at, p.updated_at,
		       COALESCE(NULLIF(u.display_name, ''), u.username, '')
		FROM pages p LEFT JOIN users u ON u.id = p.updated_by
		WHERE p.slug = $1`, slug,
	).Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &p.Version, &p.CreatedAt, &p.UpdatedAt, &p.UpdatedBy)
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// CreatePage makes a new page with version 1. It returns ErrConflict if
// a page with the same slug exists.
func (s *Store) CreatePage(ctx context.Context, slug, title, body, comment string, authorID int64) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now := s.now()
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO pages (slug, title, body, version, created_by, updated_by, created_at, updated_at)
			VALUES ($1, $2, $3, 1, $4, $4, $5, $5)
			RETURNING id`, slug, title, body, authorID, now).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO page_revisions (page_id, version, title, body, author_id, comment, created_at)
			VALUES ($1, 1, $2, $3, $4, $5, $6)`, id, title, body, authorID, comment, now)
		return err
	})
	if db.IsUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdatePage saves a new version of a page. baseVersion is the version
// that the author edited. If the page has a different version now, the
// function returns ErrConflict and changes nothing. This prevents lost
// updates when two users edit the same page.
func (s *Store) UpdatePage(ctx context.Context, slug string, baseVersion int, title, body, comment string, authorID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now := s.now()
		var id int64
		var version int
		err := tx.QueryRow(ctx, `
			UPDATE pages SET title = $3, body = $4, version = version + 1,
			                 updated_by = $5, updated_at = $6
			WHERE slug = $1 AND version = $2
			RETURNING id, version`, slug, baseVersion, title, body, authorID, now).Scan(&id, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pages WHERE slug = $1)", slug).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrConflict
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO page_revisions (page_id, version, title, body, author_id, comment, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, version, title, body, authorID, comment, now)
		return err
	})
}

// DeletePage removes a page and all its revisions.
func (s *Store) DeletePage(ctx context.Context, slug string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM pages WHERE slug = $1", slug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPages returns at most limit pages, sorted by title, from offset.
func (s *Store) ListPages(ctx context.Context, limit, offset int) ([]PageSummary, error) {
	return s.summaries(ctx, `
		SELECT slug, title, updated_at, '' FROM pages
		ORDER BY lower(title), slug LIMIT $1 OFFSET $2`, limit, offset)
}

// RecentPages returns the pages that changed last.
func (s *Store) RecentPages(ctx context.Context, limit int) ([]PageSummary, error) {
	return s.summaries(ctx, `
		SELECT slug, title, updated_at, '' FROM pages
		ORDER BY updated_at DESC LIMIT $1`, limit)
}

// SearchPages does a full text search. The query uses the syntax of the
// PostgreSQL function websearch_to_tsquery.
func (s *Store) SearchPages(ctx context.Context, query string, limit int) ([]PageSummary, error) {
	return s.summaries(ctx, `
		SELECT slug, title, updated_at,
		       ts_headline('simple', body, q, 'StartSel=«, StopSel=», MaxFragments=2, MaxWords=20, MinWords=5')
		FROM pages, websearch_to_tsquery('simple', $1) q
		WHERE search @@ q
		ORDER BY ts_rank(search, q) DESC, updated_at DESC
		LIMIT $2`, query, limit)
}

func (s *Store) summaries(ctx context.Context, sql string, args ...any) ([]PageSummary, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageSummary
	for rows.Next() {
		var p PageSummary
		if err := rows.Scan(&p.Slug, &p.Title, &p.UpdatedAt, &p.Snippet); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Revisions returns all revisions of a page, newest first. The Body field
// is empty in the result.
func (s *Store) Revisions(ctx context.Context, slug string) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.version, r.title, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       r.comment, r.created_at
		FROM page_revisions r
		JOIN pages p ON p.id = r.page_id
		LEFT JOIN users u ON u.id = r.author_id
		WHERE p.slug = $1
		ORDER BY r.version DESC`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.Version, &r.Title, &r.Author, &r.Comment, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revision returns one revision of a page.
func (s *Store) Revision(ctx context.Context, slug string, version int) (*Revision, error) {
	var r Revision
	err := s.pool.QueryRow(ctx, `
		SELECT r.version, r.title, r.body, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       r.comment, r.created_at
		FROM page_revisions r
		JOIN pages p ON p.id = r.page_id
		LEFT JOIN users u ON u.id = r.author_id
		WHERE p.slug = $1 AND r.version = $2`, slug, version,
	).Scan(&r.Version, &r.Title, &r.Body, &r.Author, &r.Comment, &r.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}
