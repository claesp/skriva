package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Node kinds.
const (
	KindFolder   = "folder"
	KindDocument = "document"
)

// Node is a folder or a document in a space. A document can be a child of
// a folder or of a document. A folder can be a child of a folder. There
// is no limit to the depth of the tree.
type Node struct {
	ID       int64
	PublicID string
	SpaceID  int64
	ParentID *int64
	Kind     string
	Title    string
	// Slug is a readable form of the title for the URL. It can be empty.
	Slug string
	// Body is the text of a document in the JSON format of the editor.
	// The store does not check the format. See package doc.
	Body    string
	Version int
	// DefaultRole is the default role of the node. Nil means that the
	// node uses the default role of its parent. An empty value means no
	// default role.
	DefaultRole *string
	// AccessPathID identifies the cached access path of the node. The
	// permissions for the node depend only on it. See Access.
	AccessPathID int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// UpdatedBy is the name of the last author. It is empty if the user
	// does not exist now.
	UpdatedBy string
}

// IsFolder reports whether the node is a folder.
func (n *Node) IsFolder() bool { return n.Kind == KindFolder }

// NodeSummary is a short form of a node for lists and trees.
type NodeSummary struct {
	ID           int64
	PublicID     string
	ParentID     *int64
	Kind         string
	Title        string
	Slug         string
	UpdatedAt    time.Time
	AccessPathID int64
}

// IsFolder reports whether the node is a folder.
func (n *NodeSummary) IsFolder() bool { return n.Kind == KindFolder }

// Revision is one saved version of a node.
type Revision struct {
	Version   int
	Title     string
	Body      string
	Author    string
	Comment   string
	CreatedAt time.Time
}

// emptyBody is the body of a folder.
const emptyBody = `{"type":"doc"}`

// Content is the text of a document.
type Content struct {
	// Body is the text in the JSON format of the editor.
	Body string
	// PlainText is the text for the search index.
	PlainText string
	// Files are the public IDs of the uploaded files that the text uses.
	// Files from other spaces have no effect.
	Files []string
}

// NewNode contains the data for a new node.
type NewNode struct {
	// ParentPublicID is the public ID of the parent. Empty means the root.
	ParentPublicID string
	Kind           string
	Title          string
	Slug           string
	Content
	Comment  string
	AuthorID int64
}

// NodeUpdate contains the data for a new version of a node.
type NodeUpdate struct {
	// BaseVersion is the version that the author edited.
	BaseVersion int
	Title       string
	Slug        string
	Content
	Comment  string
	AuthorID int64
}

// addFileRefs records that a node uses files.
func addFileRefs(ctx context.Context, tx pgx.Tx, spaceID, nodeID int64, files []string) error {
	if len(files) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO file_refs (file_id, node_id, space_id)
		SELECT f.id, $2, $1 FROM files f WHERE f.space_id = $1 AND f.public_id = ANY($3)
		ON CONFLICT DO NOTHING`, spaceID, nodeID, files)
	return err
}

// Node returns the node with the given public ID in a space.
func (s *Store) Node(ctx context.Context, spaceID int64, publicID string) (*Node, error) {
	var n Node
	err := s.pool.QueryRow(ctx, `
		SELECT n.id, n.public_id, n.space_id, n.parent_id, n.kind, n.title, n.slug, n.body,
		       n.version, n.default_role, n.access_path_id, n.created_at, n.updated_at,
		       COALESCE(NULLIF(u.display_name, ''), u.username, '')
		FROM nodes n LEFT JOIN users u ON u.id = n.updated_by
		WHERE n.space_id = $1 AND n.public_id = $2 AND n.deleted_at IS NULL`, spaceID, publicID,
	).Scan(&n.ID, &n.PublicID, &n.SpaceID, &n.ParentID, &n.Kind, &n.Title, &n.Slug, &n.Body,
		&n.Version, &n.DefaultRole, &n.AccessPathID, &n.CreatedAt, &n.UpdatedAt, &n.UpdatedBy)
	if err != nil {
		return nil, notFound(err)
	}
	return &n, nil
}

// nullID returns nil for 0. The author columns use NULL for "no user".
func nullID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// lockSpace takes a row lock on the space. All changes to the tree
// structure take this lock. Thus two concurrent moves cannot make a cycle.
func lockSpace(ctx context.Context, tx pgx.Tx, spaceID int64) error {
	var id int64
	err := tx.QueryRow(ctx, "SELECT id FROM spaces WHERE id = $1 FOR UPDATE", spaceID).Scan(&id)
	return notFound(err)
}

// parentFor finds the parent with the given public ID and makes sure that
// a node of the given kind can be its child. It returns nil for the root.
// It also returns the access path that a new child without own settings
// gets: the path of the parent, or the empty path of the space.
func parentFor(ctx context.Context, tx pgx.Tx, spaceID int64, parentPublicID, kind string) (*int64, int64, error) {
	if parentPublicID == "" {
		pathID, err := rootPath(ctx, tx, spaceID)
		return nil, pathID, err
	}
	var id, pathID int64
	var parentKind string
	err := tx.QueryRow(ctx, `
		SELECT id, kind, access_path_id FROM nodes
		WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL`,
		spaceID, parentPublicID).Scan(&id, &parentKind, &pathID)
	if err != nil {
		return nil, 0, notFound(err)
	}
	// A folder can be only in a folder. A document can be in a folder or
	// in a document.
	if kind == KindFolder && parentKind != KindFolder {
		return nil, 0, ErrInvalid
	}
	return &id, pathID, nil
}

// rootPath returns the ID of the empty access path of a space. It makes
// the path if it does not exist.
func rootPath(ctx context.Context, tx pgx.Tx, spaceID int64) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO access_paths (space_id, path) VALUES ($1, '{}')
			ON CONFLICT (space_id, path) DO NOTHING RETURNING id)
		SELECT id FROM ins
		UNION ALL
		SELECT id FROM access_paths WHERE space_id = $1 AND path = '{}'
		LIMIT 1`, spaceID).Scan(&id)
	return id, err
}

// refreshAccess calculates the access paths of a node and of its subtree
// again, and removes the paths that no node uses. The caller must hold
// the lock on the row of the space.
func refreshAccess(ctx context.Context, tx pgx.Tx, spaceID, nodeID int64) error {
	if nodeID != 0 {
		if _, err := tx.Exec(ctx, "SELECT refresh_access_paths($1)", nodeID); err != nil {
			return err
		}
	}
	return deleteUnusedPaths(ctx, tx, spaceID)
}

func deleteUnusedPaths(ctx context.Context, tx pgx.Tx, spaceID int64) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM access_paths ap
		WHERE ap.space_id = $1 AND ap.path <> '{}'
		  AND NOT EXISTS (SELECT 1 FROM nodes n WHERE n.access_path_id = ap.id)`, spaceID)
	return err
}

// CreateNode makes a folder or a document with version 1. It returns the
// public ID of the new node. It returns ErrNotFound if the parent does not
// exist, and ErrInvalid if the parent cannot contain the node.
func (s *Store) CreateNode(ctx context.Context, spaceID int64, nn NewNode) (string, error) {
	if nn.Kind != KindFolder && nn.Kind != KindDocument {
		return "", ErrInvalid
	}
	if nn.Kind == KindFolder || nn.Body == "" {
		nn.Content = Content{Body: emptyBody}
	}
	publicID := NewPublicID()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		parentID, pathID, err := parentFor(ctx, tx, spaceID, nn.ParentPublicID, nn.Kind)
		if err != nil {
			return err
		}
		now := s.now()
		var id int64
		// A new node has no own access settings. Thus it gets the access
		// path of its parent.
		err = tx.QueryRow(ctx, `
			INSERT INTO nodes (public_id, space_id, parent_id, kind, title, slug, body, plain_text, version,
			                   created_by, updated_by, created_at, updated_at, access_path_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9, $9, $10, $10, $11)
			RETURNING id`,
			publicID, spaceID, parentID, nn.Kind, nn.Title, nn.Slug, nn.Body, nn.PlainText,
			nullID(nn.AuthorID), now, pathID).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO revisions (node_id, version, title, body, author_id, comment, created_at)
			VALUES ($1, 1, $2, $3, $4, $5, $6)`, id, nn.Title, nn.Body, nullID(nn.AuthorID), nn.Comment, now)
		if err != nil {
			return err
		}
		return addFileRefs(ctx, tx, spaceID, id, nn.Files)
	})
	if err != nil {
		return "", err
	}
	return publicID, nil
}

// UpdateNode saves a new version of a node. If the node does not have
// the version u.BaseVersion now, the function returns ErrConflict and
// changes nothing. This prevents lost updates when two users edit the
// same node. For a folder, the function ignores the content.
func (s *Store) UpdateNode(ctx context.Context, spaceID int64, publicID string, u NodeUpdate) error {
	if u.Body == "" {
		u.Content = Content{Body: emptyBody}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		now := s.now()
		var id int64
		var version int
		var body string
		var folder bool
		err := tx.QueryRow(ctx, `
			UPDATE nodes SET title = $4, slug = $5,
			                 body = CASE WHEN kind = 'folder' THEN $9::jsonb ELSE $6::jsonb END,
			                 plain_text = CASE WHEN kind = 'folder' THEN '' ELSE $7 END,
			                 version = version + 1, updated_by = $8, updated_at = $10
			WHERE space_id = $1 AND public_id = $2 AND version = $3 AND deleted_at IS NULL
			RETURNING id, version, body, kind = 'folder'`,
			spaceID, publicID, u.BaseVersion, u.Title, u.Slug, u.Body, u.PlainText, nullID(u.AuthorID),
			emptyBody, now).Scan(&id, &version, &body, &folder)
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM nodes WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL)`,
				spaceID, publicID).Scan(&exists); err != nil {
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
			INSERT INTO revisions (node_id, version, title, body, author_id, comment, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, version, u.Title, body, nullID(u.AuthorID), u.Comment, now)
		if err != nil || folder {
			return err
		}
		return addFileRefs(ctx, tx, spaceID, id, u.Files)
	})
}

// MoveNode changes the parent of a node. An empty newParentPublicID moves
// the node to the root. It returns ErrInvalid if the new parent is the
// node itself, is in the subtree of the node, or cannot contain the node.
func (s *Store) MoveNode(ctx context.Context, spaceID int64, publicID, newParentPublicID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		var kind string
		if err := tx.QueryRow(ctx, `
			SELECT id, kind FROM nodes WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL`,
			spaceID, publicID).Scan(&id, &kind); err != nil {
			return notFound(err)
		}
		parentID, _, err := parentFor(ctx, tx, spaceID, newParentPublicID, kind)
		if err != nil {
			return err
		}
		if parentID != nil {
			var inSubtree bool
			// UNION (not UNION ALL) stops the recursion if the data
			// contains a cycle.
			err := tx.QueryRow(ctx, `
				WITH RECURSIVE sub(id) AS (
					SELECT $1::bigint
					UNION
					SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted_at IS NULL
				)
				SELECT EXISTS (SELECT 1 FROM sub WHERE id = $2)`, id, *parentID).Scan(&inSubtree)
			if err != nil {
				return err
			}
			if inSubtree {
				return ErrInvalid
			}
		}
		if _, err := tx.Exec(ctx, "UPDATE nodes SET parent_id = $2, updated_at = $3 WHERE id = $1",
			id, parentID, s.now()); err != nil {
			return err
		}
		// The subtree now has a different parent path.
		return refreshAccess(ctx, tx, spaceID, id)
	})
}

// SetNodeDefaultRole sets the default role of a folder or a document.
// Nil means that the node uses the default role of its parent. A pointer
// to "" means no default role. It returns ErrInvalid if the role does not
// exist.
func (s *Store) SetNodeDefaultRole(ctx context.Context, tenantID, spaceID int64, publicID string, role *string) error {
	if role != nil {
		if err := checkRole(ctx, s.pool, tenantID, *role); err != nil {
			return err
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRow(ctx, `
			UPDATE nodes n SET default_role = $4
			FROM spaces sp
			WHERE sp.id = n.space_id AND sp.tenant_id = $1 AND n.space_id = $2 AND n.public_id = $3
			  AND n.deleted_at IS NULL
			RETURNING n.id`, tenantID, spaceID, publicID, role).Scan(&id)
		if err != nil {
			return notFound(err)
		}
		return refreshAccess(ctx, tx, spaceID, id)
	})
}

// SubtreePaths returns the IDs of the access paths of the descendants of
// a node.
func (s *Store) SubtreePaths(ctx context.Context, nodeID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE sub(id) AS (
			SELECT id FROM nodes WHERE parent_id = $1 AND deleted_at IS NULL
			UNION
			SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted_at IS NULL
		)
		SELECT DISTINCT n.access_path_id FROM nodes n JOIN sub ON sub.id = n.id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountDescendants returns the number of nodes in the subtree of a node,
// without the node itself.
func (s *Store) CountDescendants(ctx context.Context, nodeID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		WITH RECURSIVE sub(id) AS (
			SELECT id FROM nodes WHERE parent_id = $1 AND deleted_at IS NULL
			UNION
			SELECT n.id FROM nodes n JOIN sub ON n.parent_id = sub.id WHERE n.deleted_at IS NULL
		)
		SELECT count(*) FROM sub`, nodeID).Scan(&n)
	return n, err
}

// MaxTreeNodes is the maximum number of nodes that SpaceNodes returns.
const MaxTreeNodes = 10000

// SpaceNodes returns the nodes of a space, without the bodies. Folders
// come first, then the nodes are sorted by title. The result has at most
// MaxTreeNodes nodes.
func (s *Store) SpaceNodes(ctx context.Context, spaceID int64) ([]NodeSummary, error) {
	return s.summaries(ctx, `
		SELECT id, public_id, parent_id, kind, title, slug, updated_at, access_path_id FROM nodes
		WHERE space_id = $1 AND deleted_at IS NULL
		ORDER BY kind = 'document', lower(title), id
		LIMIT $2`, spaceID, MaxTreeNodes)
}

// Children returns the children of a node, or the root nodes of the space
// if parentID is nil.
func (s *Store) Children(ctx context.Context, spaceID int64, parentID *int64) ([]NodeSummary, error) {
	return s.summaries(ctx, `
		SELECT id, public_id, parent_id, kind, title, slug, updated_at, access_path_id FROM nodes
		WHERE space_id = $1 AND parent_id IS NOT DISTINCT FROM $2 AND deleted_at IS NULL
		ORDER BY kind = 'document', lower(title), id`, spaceID, parentID)
}

// Ancestors returns the ancestors of a node, from the root to the parent.
func (s *Store) Ancestors(ctx context.Context, nodeID int64) ([]NodeSummary, error) {
	list, err := s.summaries(ctx, `
		WITH RECURSIVE up(id, depth) AS (
			SELECT parent_id, 1 FROM nodes WHERE id = $1 AND parent_id IS NOT NULL
			UNION
			SELECT n.parent_id, up.depth + 1 FROM nodes n JOIN up ON n.id = up.id
			WHERE n.parent_id IS NOT NULL
		)
		CYCLE id SET is_cycle USING path
		SELECT n.id, n.public_id, n.parent_id, n.kind, n.title, n.slug, n.updated_at, n.access_path_id
		FROM up JOIN nodes n ON n.id = up.id
		WHERE NOT up.is_cycle
		ORDER BY up.depth DESC`, nodeID)
	return list, err
}

// NodesBySlug returns the nodes in a space with the given slug. Wiki links
// use it.
func (s *Store) NodesBySlug(ctx context.Context, spaceID int64, slug string) ([]NodeSummary, error) {
	return s.summaries(ctx, `
		SELECT id, public_id, parent_id, kind, title, slug, updated_at, access_path_id FROM nodes
		WHERE space_id = $1 AND slug = $2 AND deleted_at IS NULL
		ORDER BY kind = 'folder', created_at, id LIMIT 50`, spaceID, slug)
}

// RecentChange is a document in the list of recent changes of a tenant.
type RecentChange struct {
	NodeSummary
	SpaceID   int64
	SpaceSlug string
	SpaceName string
}

// RecentChanges returns the documents of a tenant that changed last. The
// caller must remove the documents that the user cannot read.
func (s *Store) RecentChanges(ctx context.Context, tenantID int64, limit int) ([]RecentChange, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.public_id, n.parent_id, n.kind, n.title, n.slug, n.updated_at, n.access_path_id,
		       sp.id, sp.slug, sp.name
		FROM nodes n JOIN spaces sp ON sp.id = n.space_id
		WHERE sp.tenant_id = $1 AND n.kind = 'document' AND n.deleted_at IS NULL
		ORDER BY n.updated_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecentChange
	for rows.Next() {
		var c RecentChange
		if err := rows.Scan(&c.ID, &c.PublicID, &c.ParentID, &c.Kind, &c.Title, &c.Slug, &c.UpdatedAt,
			&c.AccessPathID, &c.SpaceID, &c.SpaceSlug, &c.SpaceName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) summaries(ctx context.Context, sql string, args ...any) ([]NodeSummary, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeSummary
	for rows.Next() {
		var n NodeSummary
		if err := rows.Scan(&n.ID, &n.PublicID, &n.ParentID, &n.Kind, &n.Title, &n.Slug, &n.UpdatedAt, &n.AccessPathID); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SearchResult is one result of a full text search.
type SearchResult struct {
	NodeSummary
	SpaceID   int64
	SpaceSlug string
	SpaceName string
	// Snippet is a part of the text. The marks « and » enclose the
	// words that match.
	Snippet string
}

// Search does a full text search in the documents of the given spaces of
// a tenant. The caller must give only the spaces that the user can read.
// The query uses the syntax of the PostgreSQL function
// websearch_to_tsquery.
func (s *Store) Search(ctx context.Context, tenantID int64, spaceIDs []int64, query string, limit int) ([]SearchResult, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.public_id, n.parent_id, n.kind, n.title, n.slug, n.updated_at, n.access_path_id,
		       sp.id, sp.slug, sp.name,
		       ts_headline('simple', n.plain_text, q, 'StartSel=«, StopSel=», MaxFragments=2, MaxWords=20, MinWords=5')
		FROM nodes n
		JOIN spaces sp ON sp.id = n.space_id,
		     websearch_to_tsquery('simple', $3) q
		WHERE sp.tenant_id = $1 AND sp.id = ANY($2) AND n.search @@ q AND n.deleted_at IS NULL
		ORDER BY ts_rank(n.search, q) DESC, n.updated_at DESC
		LIMIT $4`, tenantID, spaceIDs, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.PublicID, &r.ParentID, &r.Kind, &r.Title, &r.Slug, &r.UpdatedAt,
			&r.AccessPathID, &r.SpaceID, &r.SpaceSlug, &r.SpaceName, &r.Snippet); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revisions returns all revisions of a node, newest first. The Body field
// is empty in the result.
func (s *Store) Revisions(ctx context.Context, nodeID int64) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.version, r.title, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       r.comment, r.created_at
		FROM revisions r
		LEFT JOIN users u ON u.id = r.author_id
		WHERE r.node_id = $1
		ORDER BY r.version DESC`, nodeID)
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

// Revision returns one revision of a node.
func (s *Store) Revision(ctx context.Context, nodeID int64, version int) (*Revision, error) {
	var r Revision
	err := s.pool.QueryRow(ctx, `
		SELECT r.version, r.title, r.body, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       r.comment, r.created_at
		FROM revisions r
		LEFT JOIN users u ON u.id = r.author_id
		WHERE r.node_id = $1 AND r.version = $2`, nodeID, version,
	).Scan(&r.Version, &r.Title, &r.Body, &r.Author, &r.Comment, &r.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

// TreeNode is a node in a tree that BuildTree makes.
type TreeNode struct {
	NodeSummary
	Children []*TreeNode
	// Open is true for the current node and its ancestors.
	Open bool
	// Current is true for the current node.
	Current bool
}

// BuildTree makes a tree from a flat list of nodes. Nodes with a parent
// that is not in the list are not in the tree. current is the ID of the
// current node, or 0. The function marks the current node and its
// ancestors.
func BuildTree(nodes []NodeSummary, current int64) []*TreeNode {
	byID := make(map[int64]*TreeNode, len(nodes))
	for i := range nodes {
		byID[nodes[i].ID] = &TreeNode{NodeSummary: nodes[i]}
	}
	var roots []*TreeNode
	for i := range nodes {
		t := byID[nodes[i].ID]
		if t.ParentID == nil {
			roots = append(roots, t)
			continue
		}
		if p, ok := byID[*t.ParentID]; ok {
			p.Children = append(p.Children, t)
		}
	}
	// Mark the path to the current node. The visited set stops the loop
	// if the data contains a cycle.
	visited := map[int64]bool{}
	for t := byID[current]; t != nil && !visited[t.ID]; {
		visited[t.ID] = true
		t.Open = true
		t.Current = t.ID == current
		if t.ParentID == nil {
			break
		}
		t = byID[*t.ParentID]
	}
	// The order of the input stays, thus the caller controls the order.
	return roots
}

// BuildVisibleTree makes a tree from the nodes for which visible returns
// true. A visible node with a hidden parent goes below its nearest visible
// ancestor, or to the top level. Thus the tree shows no titles of hidden
// nodes, and the user can still find all visible nodes.
func BuildVisibleTree(nodes []NodeSummary, current int64, visible func(id int64) bool) []*TreeNode {
	byID := make(map[int64]*NodeSummary, len(nodes))
	for i := range nodes {
		byID[nodes[i].ID] = &nodes[i]
	}
	var out []NodeSummary
	for _, n := range nodes {
		if !visible(n.ID) {
			continue
		}
		// Find the nearest visible ancestor. The limit stops the loop if
		// the data contains a cycle.
		var parent *int64
		p := n.ParentID
		for i := 0; p != nil && i <= len(nodes); i++ {
			if visible(*p) {
				parent = p
				break
			}
			anc := byID[*p]
			if anc == nil {
				break
			}
			p = anc.ParentID
		}
		n.ParentID = parent
		out = append(out, n)
	}
	return BuildTree(out, current)
}

// FullTreeLimit is the largest space for which the tree shows all nodes.
// A larger space shows only the top level and the levels on the path to
// the current node.
const FullTreeLimit = 2000

// IsLargeSpace reports whether the space has more than FullTreeLimit
// nodes. The query reads at most FullTreeLimit+1 index entries.
func (s *Store) IsLargeSpace(ctx context.Context, spaceID int64) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM (SELECT 1 FROM nodes WHERE space_id = $1 AND deleted_at IS NULL LIMIT $2) x`,
		spaceID, FullTreeLimit+1).Scan(&n)
	return n > FullTreeLimit, err
}

// PathTreeNodes returns the nodes at the top level of a space and the
// children of the given nodes. The tree of a large space uses it: the
// given nodes are the current node and its ancestors.
func (s *Store) PathTreeNodes(ctx context.Context, spaceID int64, nodeIDs []int64) ([]NodeSummary, error) {
	if nodeIDs == nil {
		nodeIDs = []int64{}
	}
	return s.summaries(ctx, `
		SELECT id, public_id, parent_id, kind, title, slug, updated_at, access_path_id FROM nodes
		WHERE space_id = $1 AND (parent_id IS NULL OR parent_id = ANY($2)) AND deleted_at IS NULL
		ORDER BY kind = 'document', lower(title), id
		LIMIT $3`, spaceID, nodeIDs, MaxTreeNodes)
}
