package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/claesp/skriva/internal/slug"
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
	// The store makes it from the title.
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
	// ClassificationID and Classification are the information
	// classification of a document. A folder has none.
	ClassificationID int64
	Classification   string
	// DefaultClassificationID and MinClassificationID are the
	// classification setting of a folder. 0 means the same as the
	// parent. See SetFolderClassification.
	DefaultClassificationID int64
	MinClassificationID     int64
	CreatedAt               time.Time
	UpdatedAt               time.Time
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
	SpaceID      int64
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
	Version int
	Title   string
	Body    string
	Author  string
	Comment string
	// Classification is the name of the classification of the version.
	Classification string
	CreatedAt      time.Time
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
	// DocLinks are the public IDs of the targets of the "doc:" links.
	// Targets in other spaces have no effect.
	DocLinks []string
	// WikiLinks are the slugs of the titles of the wiki links.
	WikiLinks []string
}

// NewNode contains the data for a new node.
type NewNode struct {
	// ParentPublicID is the public ID of the parent. Empty means the root.
	ParentPublicID string
	Kind           string
	Title          string
	Content
	Comment  string
	AuthorID int64
	// ClassificationID is the classification of a document. 0 gives the
	// default classification of the tenant.
	ClassificationID int64
}

// NodeUpdate contains the data for a new version of a node.
type NodeUpdate struct {
	// BaseVersion is the version that the author edited.
	BaseVersion int
	Title       string
	Content
	Comment  string
	AuthorID int64
	// RestoredFrom is the old version that the author restored, or 0.
	// The audit log records a restored version.
	RestoredFrom int
	// ClassificationID is the classification of a document. 0 keeps the
	// current classification.
	ClassificationID int64
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

// setLinks replaces the links of a node. wikiTokens are the keyed hashes
// of the slugs of the wiki links.
func setLinks(ctx context.Context, tx pgx.Tx, spaceID, nodeID int64, c Content, wikiTokens []int64) error {
	if _, err := tx.Exec(ctx, "DELETE FROM node_links WHERE source_id = $1", nodeID); err != nil {
		return err
	}
	if len(c.DocLinks) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO node_links (space_id, source_id, target_id)
			SELECT $1, $2, n.id FROM nodes n
			WHERE n.space_id = $1 AND n.public_id = ANY($3) AND n.id <> $2
			ON CONFLICT DO NOTHING`, spaceID, nodeID, c.DocLinks); err != nil {
			return err
		}
	}
	if len(wikiTokens) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO node_links (space_id, source_id, target_token)
			SELECT $1, $2, t FROM unnest($3::bigint[]) t
			ON CONFLICT DO NOTHING`, spaceID, nodeID, wikiTokens); err != nil {
			return err
		}
	}
	return nil
}

// Backlinks returns the nodes that link to node n: with a "doc:" link,
// or with a wiki link to the title of n. The result does not contain n
// and the nodes in the trash. The caller must remove the nodes that the
// user cannot read.
func (s *Store) Backlinks(ctx context.Context, n *Node) ([]NodeSummary, error) {
	tk, err := s.spaceKeys(ctx, n.SpaceID)
	if err != nil {
		return nil, err
	}
	list, err := s.summaries(ctx, `
		SELECT `+summaryCols+`
		FROM nodes n
		JOIN (SELECT source_id FROM node_links WHERE target_id = $2
		      UNION
		      SELECT source_id FROM node_links WHERE space_id = $1 AND target_token = $3
		) l ON l.source_id = n.id
		WHERE n.space_id = $1 AND n.id <> $2 AND n.deleted_at IS NULL
		ORDER BY n.id
		LIMIT 200`, n.SpaceID, n.ID, slugToken(tk.search, n.Slug))
	sortByTitle(list, func(n NodeSummary) string { return n.Title })
	return list, err
}

// LinkTarget is a possible target for a link in the editor.
type LinkTarget struct {
	NodeSummary
	// ParentTitle and ParentPathID are the title and the access path of
	// the parent, or empty for the top level.
	ParentTitle  string
	ParentPathID int64
}

// FindLinkTargets returns the nodes of a space with a title that contains
// query, without the nodes in the trash. The case of the letters has no
// effect. Titles that start with the query come first. The titles are
// encrypted, thus the function decrypts the titles of the space and
// filters them in memory. The caller must remove the nodes that the user
// cannot read.
func (s *Store) FindLinkTargets(ctx context.Context, spaceID int64, query string, limit int) ([]LinkTarget, error) {
	nodes, err := s.summaries(ctx, `
		SELECT `+summaryCols+` FROM nodes n
		WHERE n.space_id = $1 AND n.deleted_at IS NULL
		ORDER BY n.id LIMIT $2`, spaceID, MaxTreeNodes)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*NodeSummary, len(nodes))
	for i := range nodes {
		byID[nodes[i].ID] = &nodes[i]
	}
	q := strings.ToLower(query)
	// rank 0: the same title, 1: the title starts with the query, 2: the
	// title contains the query.
	type found struct {
		LinkTarget
		rank int
	}
	var list []found
	for _, n := range nodes {
		t := strings.ToLower(n.Title)
		if !strings.Contains(t, q) {
			continue
		}
		f := found{LinkTarget: LinkTarget{NodeSummary: n}, rank: 2}
		switch {
		case t == q:
			f.rank = 0
		case strings.HasPrefix(t, q):
			f.rank = 1
		}
		if n.ParentID != nil {
			if p := byID[*n.ParentID]; p != nil {
				f.ParentTitle, f.ParentPathID = p.Title, p.AccessPathID
			}
		}
		list = append(list, f)
	}
	sortByTitle(list, func(f found) string { return f.Title })
	slices.SortStableFunc(list, func(a, b found) int { return a.rank - b.rank })
	out := make([]LinkTarget, 0, min(limit, len(list)))
	for _, f := range list[:min(limit, len(list))] {
		out = append(out, f.LinkTarget)
	}
	return out, nil
}

// Node returns the node with the given public ID in a space.
func (s *Store) Node(ctx context.Context, spaceID int64, publicID string) (*Node, error) {
	var n Node
	var title, body []byte
	err := s.pool.QueryRow(ctx, `
		SELECT n.id, n.public_id, n.space_id, n.parent_id, n.kind, n.title, n.body,
		       n.version, n.default_role, n.access_path_id, n.created_at, n.updated_at,
		       COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       COALESCE(c.id, 0), COALESCE(c.name, ''),
		       COALESCE(n.default_classification_id, 0), COALESCE(n.min_classification_id, 0)
		FROM nodes n LEFT JOIN users u ON u.id = n.updated_by
		LEFT JOIN classifications c ON c.id = n.classification_id
		WHERE n.space_id = $1 AND n.public_id = $2 AND n.deleted_at IS NULL`, spaceID, publicID,
	).Scan(&n.ID, &n.PublicID, &n.SpaceID, &n.ParentID, &n.Kind, &title, &body,
		&n.Version, &n.DefaultRole, &n.AccessPathID, &n.CreatedAt, &n.UpdatedAt, &n.UpdatedBy,
		&n.ClassificationID, &n.Classification, &n.DefaultClassificationID, &n.MinClassificationID)
	if err != nil {
		return nil, notFound(err)
	}
	if n.Title, err = s.openText(ctx, spaceID, titlePlace(spaceID, publicID), title); err != nil {
		return nil, err
	}
	n.Slug = slug.Make(n.Title)
	if n.Body, err = s.openText(ctx, spaceID, bodyPlace(spaceID, publicID), body); err != nil {
		return nil, err
	}
	return &n, nil
}

// The places of the encrypted values, for the associated data. They must
// agree with the queries of ReencryptBatch.
func bodyPlace(spaceID int64, publicID string) string {
	return "nodes.body:" + strconv.FormatInt(spaceID, 10) + ":" + publicID
}

func titlePlace(spaceID int64, publicID string) string {
	return "nodes.title:" + strconv.FormatInt(spaceID, 10) + ":" + publicID
}

func revisionTitlePlace(spaceID int64, publicID string, version int) string {
	return "revisions.title:" + strconv.FormatInt(spaceID, 10) + ":" + publicID + ":" + strconv.Itoa(version)
}

func plainTextPlace(spaceID int64, publicID string) string {
	return "nodes.plain_text:" + strconv.FormatInt(spaceID, 10) + ":" + publicID
}

func revisionPlace(spaceID int64, publicID string, version int) string {
	return "revisions.body:" + strconv.FormatInt(spaceID, 10) + ":" + publicID + ":" + strconv.Itoa(version)
}

func filePlace(spaceID int64, publicID string) string {
	return "files.data:" + strconv.FormatInt(spaceID, 10) + ":" + publicID
}

// openText decrypts a text value of a space.
func (s *Store) openText(ctx context.Context, spaceID int64, place string, data []byte) (string, error) {
	plain, err := s.openBytes(ctx, spaceID, place, data)
	return string(plain), err
}

// openBytes decrypts a value of a space.
func (s *Store) openBytes(ctx context.Context, spaceID int64, place string, data []byte) ([]byte, error) {
	tenantID, err := s.tenantOfSpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return s.open(ctx, tenantID, place, data)
}

// MaxTitleLength is the maximum length of a title in characters.
const MaxTitleLength = 200

// validTitle reports whether a title has 1 to MaxTitleLength characters.
func validTitle(title string) bool {
	n := utf8.RuneCountInString(title)
	return n >= 1 && n <= MaxTitleLength
}

// sealedContent is the encrypted title and content of a version of a
// node, and its keyed hashes.
type sealedContent struct {
	title, revisionTitle      []byte
	body, plainText, revision []byte
	version                   int
	index                     searchIndex
	// slugToken is the hash of the slug of the title, or nil.
	slugToken *int64
	// wikiTokens are the hashes of the slugs of the wiki links.
	wikiTokens []int64
}

// sealContent encrypts the title and the content of version v of a node
// and makes the search index from the title and the plain text.
func (s *Store) sealContent(ctx context.Context, spaceID int64, publicID string, v int, title string, c Content) (*sealedContent, error) {
	if !validTitle(title) {
		return nil, ErrInvalid
	}
	tenantID, err := s.tenantOfSpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	tk, err := s.tenantKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	aead := tk.data[tk.current]
	seal := func(place, plain string) []byte { return sealWith(aead, tk.current, tenantID, place, []byte(plain)) }
	sc := &sealedContent{
		title:         seal(titlePlace(spaceID, publicID), title),
		revisionTitle: seal(revisionTitlePlace(spaceID, publicID, v), title),
		body:          seal(bodyPlace(spaceID, publicID), c.Body),
		plainText:     seal(plainTextPlace(spaceID, publicID), c.PlainText),
		revision:      seal(revisionPlace(spaceID, publicID, v), c.Body),
		version:       tk.current,
		index:         makeSearchIndex(tk.search, title, c.PlainText),
		slugToken:     slugToken(tk.search, slug.Make(title)),
	}
	for _, l := range c.WikiLinks {
		if t := slugToken(tk.search, l); t != nil {
			sc.wikiTokens = append(sc.wikiTokens, *t)
		}
	}
	return sc, nil
}

// spaceKeys returns the keys of the tenant of a space.
func (s *Store) spaceKeys(ctx context.Context, spaceID int64) (*tenantKeys, error) {
	tenantID, err := s.tenantOfSpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return s.tenantKeys(ctx, tenantID)
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
	sealed, err := s.sealContent(ctx, spaceID, publicID, 1, nn.Title, nn.Content)
	if err != nil {
		return "", err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockSpace(ctx, tx, spaceID); err != nil {
			return err
		}
		parentID, pathID, err := parentFor(ctx, tx, spaceID, nn.ParentPublicID, nn.Kind)
		if err != nil {
			return err
		}
		var classID *int64
		var className string
		if nn.Kind == KindDocument {
			c, err := permittedClass(ctx, tx, spaceID, parentID, nn.ClassificationID)
			if err != nil {
				return err
			}
			classID, className = &c.ID, c.Name
		}
		now := s.now()
		var id int64
		// A new node has no own access settings. Thus it gets the access
		// path of its parent.
		err = tx.QueryRow(ctx, `
			INSERT INTO nodes (public_id, space_id, parent_id, kind, title, slug_token, body, plain_text, key_version,
			                   search_tokens, title_tokens, prefix_tokens, stem_tokens, search_version, version,
			                   created_by, updated_by, created_at, updated_at, access_path_id, classification_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $16, $18, $17, 1, $12, $12, $13, $13, $14, $15)
			RETURNING id`,
			publicID, spaceID, parentID, nn.Kind, sealed.title, sealed.slugToken, sealed.body, sealed.plainText,
			sealed.version, sealed.index.tokens, sealed.index.titleTokens, nullID(nn.AuthorID), now, pathID, classID,
			sealed.index.prefixTokens, searchIndexVersion, sealed.index.stemTokens).Scan(&id)
		if err != nil {
			return err
		}
		if err := setLinks(ctx, tx, spaceID, id, nn.Content, sealed.wikiTokens); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO revisions (node_id, version, title, body, key_version, author_id, comment, classification, created_at)
			VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8)`,
			id, sealed.revisionTitle, sealed.revision, sealed.version, nullID(nn.AuthorID), nn.Comment, className, now)
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
		// The shared lock makes sure that the classification limits do
		// not change during the update. Other updates do not wait.
		if _, err := tx.Exec(ctx, "SELECT id FROM spaces WHERE id = $1 FOR SHARE", spaceID); err != nil {
			return err
		}
		// The old classification, for the audit log. A folder has no
		// content.
		var oldClass string
		var folder bool
		var parentID *int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(c.name, ''), n.kind = 'folder', n.parent_id
			FROM nodes n LEFT JOIN classifications c ON c.id = n.classification_id
			WHERE n.space_id = $1 AND n.public_id = $2`, spaceID, publicID).Scan(&oldClass, &folder, &parentID); err != nil {
			return notFound(err)
		}
		var newClass *int64
		if u.ClassificationID != 0 && !folder {
			c, err := permittedClass(ctx, tx, spaceID, parentID, u.ClassificationID)
			if err != nil {
				return err
			}
			newClass = &c.ID
		}
		if folder {
			u.Content = Content{Body: emptyBody}
		}
		// The new version is BaseVersion+1: the update applies only to
		// that version.
		sealed, err := s.sealContent(ctx, spaceID, publicID, u.BaseVersion+1, u.Title, u.Content)
		if err != nil {
			return err
		}
		now := s.now()
		var id int64
		var version int
		var className string
		err = tx.QueryRow(ctx, `
			UPDATE nodes n SET title = $4, slug_token = $5, body = $6, plain_text = $7, key_version = $11,
			                 search_tokens = $12, title_tokens = $13, prefix_tokens = $14, search_version = $15,
			                 stem_tokens = $16,
			                 classification_id = CASE WHEN kind = 'folder' THEN NULL
			                                          ELSE COALESCE($10, classification_id) END,
			                 version = version + 1, updated_by = $8, updated_at = $9
			WHERE space_id = $1 AND public_id = $2 AND version = $3 AND deleted_at IS NULL
			RETURNING id, version,
			          COALESCE((SELECT name FROM classifications WHERE id = n.classification_id), '')`,
			spaceID, publicID, u.BaseVersion, sealed.title, sealed.slugToken, sealed.body, sealed.plainText,
			nullID(u.AuthorID), now, newClass, sealed.version, sealed.index.tokens, sealed.index.titleTokens,
			sealed.index.prefixTokens, searchIndexVersion, sealed.index.stemTokens,
		).Scan(&id, &version, &className)
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
			INSERT INTO revisions (node_id, version, title, body, key_version, author_id, comment, classification, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			id, version, sealed.revisionTitle, sealed.revision, sealed.version, nullID(u.AuthorID), u.Comment, className, now)
		if err != nil {
			return err
		}
		if oldClass != className {
			if err := s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodeClassify, Target: u.Title,
				TargetID: publicID, Details: "From " + oldClass + " to " + className + "."}); err != nil {
				return err
			}
		}
		if u.RestoredFrom > 0 {
			if err := s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodeRevert, Target: u.Title,
				TargetID: publicID, Details: fmt.Sprintf("Version %d restored as version %d.", u.RestoredFrom, version)}); err != nil {
				return err
			}
		}
		if folder {
			return nil
		}
		if err := setLinks(ctx, tx, spaceID, id, u.Content, sealed.wikiTokens); err != nil {
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
		var sealedTitle []byte
		if err := tx.QueryRow(ctx, `
			SELECT id, kind, title FROM nodes WHERE space_id = $1 AND public_id = $2 AND deleted_at IS NULL`,
			spaceID, publicID).Scan(&id, &kind, &sealedTitle); err != nil {
			return notFound(err)
		}
		title, err := s.openText(ctx, spaceID, titlePlace(spaceID, publicID), sealedTitle)
		if err != nil {
			return err
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
		// The documents must be permitted at the new place.
		limits, _, err := classLimits(ctx, tx, spaceID, parentID)
		if err != nil {
			return err
		}
		if err := checkSubtree(ctx, tx, spaceID, &id, false, limits.Min); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE nodes SET parent_id = $2, updated_at = $3 WHERE id = $1",
			id, parentID, s.now()); err != nil {
			return err
		}
		place := "To the top level."
		if parentID != nil {
			var sealedParent []byte
			if err := tx.QueryRow(ctx, "SELECT title FROM nodes WHERE id = $1", *parentID).Scan(&sealedParent); err != nil {
				return err
			}
			parent, err := s.openText(ctx, spaceID, titlePlace(spaceID, newParentPublicID), sealedParent)
			if err != nil {
				return err
			}
			place = "Into \"" + parent + "\" (" + newParentPublicID + ")."
		}
		if err := s.audit(ctx, tx, auditEntry{SpaceID: spaceID, Action: AuditNodeMove, Target: title,
			TargetID: publicID, Details: place}); err != nil {
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
		var sealedTitle []byte
		err := tx.QueryRow(ctx, `
			UPDATE nodes n SET default_role = $4
			FROM spaces sp
			WHERE sp.id = n.space_id AND sp.tenant_id = $1 AND n.space_id = $2 AND n.public_id = $3
			  AND n.deleted_at IS NULL
			RETURNING n.id, n.title`, tenantID, spaceID, publicID, role).Scan(&id, &sealedTitle)
		if err != nil {
			return notFound(err)
		}
		title, err := s.openText(ctx, spaceID, titlePlace(spaceID, publicID), sealedTitle)
		if err != nil {
			return err
		}
		if err := s.audit(ctx, tx, auditEntry{TenantID: &tenantID, SpaceID: spaceID, Action: AuditNodeDefaultRole,
			Target: title, TargetID: publicID, Details: roleText(role, "parent")}); err != nil {
			return err
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
	return s.sortedSummaries(ctx, `
		SELECT `+summaryCols+` FROM nodes n
		WHERE n.space_id = $1 AND n.deleted_at IS NULL
		ORDER BY n.kind = 'document', n.id
		LIMIT $2`, spaceID, MaxTreeNodes)
}

// ItemDetail is the data of a node for a table of items, in addition to
// the NodeSummary.
type ItemDetail struct {
	// Classification is the name of the classification of a document,
	// or "" for a folder.
	Classification string
	// UpdatedBy is the name of the last author. It is empty if the user
	// does not exist now.
	UpdatedBy string
}

// ItemDetails returns the details of the nodes with the IDs in a space.
// The tree does not need these values, thus the queries of NodeSummary
// do not read them. A table of items shows only a part of the nodes.
func (s *Store) ItemDetails(ctx context.Context, spaceID int64, ids []int64) (map[int64]ItemDetail, error) {
	out := make(map[int64]ItemDetail, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, COALESCE(c.name, ''), COALESCE(NULLIF(u.display_name, ''), u.username, '')
		FROM nodes n LEFT JOIN users u ON u.id = n.updated_by
		LEFT JOIN classifications c ON c.id = n.classification_id
		WHERE n.space_id = $1 AND n.id = ANY($2)`, spaceID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var d ItemDetail
		if err := rows.Scan(&id, &d.Classification, &d.UpdatedBy); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}

// Children returns the children of a node, or the root nodes of the space
// if parentID is nil.
func (s *Store) Children(ctx context.Context, spaceID int64, parentID *int64) ([]NodeSummary, error) {
	return s.sortedSummaries(ctx, `
		SELECT `+summaryCols+` FROM nodes n
		WHERE n.space_id = $1 AND n.parent_id IS NOT DISTINCT FROM $2 AND n.deleted_at IS NULL
		ORDER BY n.id`, spaceID, parentID)
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
		SELECT `+summaryCols+`
		FROM up JOIN nodes n ON n.id = up.id
		WHERE NOT up.is_cycle
		ORDER BY up.depth DESC`, nodeID)
	return list, err
}

// NodesBySlug returns the nodes in a space with the given slug. Wiki links
// use it.
func (s *Store) NodesBySlug(ctx context.Context, spaceID int64, sl string) ([]NodeSummary, error) {
	tk, err := s.spaceKeys(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	list, err := s.summaries(ctx, `
		SELECT `+summaryCols+` FROM nodes n
		WHERE n.space_id = $1 AND n.slug_token = $2 AND n.deleted_at IS NULL
		ORDER BY n.kind = 'folder', n.created_at, n.id LIMIT 50`, spaceID, slugToken(tk.search, sl))
	// Two slugs can have the same hash. The decrypted title tells.
	return slices.DeleteFunc(list, func(n NodeSummary) bool { return n.Slug != sl }), err
}

// RecentChange is a document in the list of recent changes of a tenant.
type RecentChange struct {
	NodeSummary
	SpaceSlug string
	SpaceName string
}

// RecentChanges returns the documents of a tenant that changed last. The
// caller must remove the documents that the user cannot read.
func (s *Store) RecentChanges(ctx context.Context, tenantID int64, limit int) ([]RecentChange, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+summaryCols+`, sp.slug, sp.name
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
		var title []byte
		if err := rows.Scan(append(summaryDest(&c.NodeSummary, &title), &c.SpaceSlug, &c.SpaceName)...); err != nil {
			return nil, err
		}
		if err := s.openSummary(ctx, &c.NodeSummary, title); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// summaryCols are the columns of a NodeSummary for summaryDest. The
// query must name the table of the nodes "n".
const summaryCols = "n.id, n.public_id, n.space_id, n.parent_id, n.kind, n.title, n.updated_at, n.access_path_id"

// summaryDest returns the scan targets for summaryCols. The encrypted
// title goes into title. Then openSummary decrypts it.
func summaryDest(n *NodeSummary, title *[]byte) []any {
	return []any{&n.ID, &n.PublicID, &n.SpaceID, &n.ParentID, &n.Kind, title, &n.UpdatedAt, &n.AccessPathID}
}

// openSummary decrypts the title of a node and makes the slug.
func (s *Store) openSummary(ctx context.Context, n *NodeSummary, title []byte) error {
	t, err := s.openText(ctx, n.SpaceID, titlePlace(n.SpaceID, n.PublicID), title)
	if err != nil {
		return err
	}
	n.Title, n.Slug = t, slug.Make(t)
	return nil
}

// summaries runs a query that returns summaryCols.
func (s *Store) summaries(ctx context.Context, sql string, args ...any) ([]NodeSummary, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeSummary
	for rows.Next() {
		var n NodeSummary
		var title []byte
		if err := rows.Scan(summaryDest(&n, &title)...); err != nil {
			return nil, err
		}
		if err := s.openSummary(ctx, &n, title); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// sortedSummaries runs a query that returns summaryCols, and sorts the
// result: folders first, then by title. For the same title, the order of
// the query stays.
func (s *Store) sortedSummaries(ctx context.Context, sql string, args ...any) ([]NodeSummary, error) {
	list, err := s.summaries(ctx, sql, args...)
	sortByTitle(list, func(n NodeSummary) string { return n.Title })
	slices.SortStableFunc(list, func(a, b NodeSummary) int {
		switch {
		case a.IsFolder() == b.IsFolder():
			return 0
		case a.IsFolder():
			return -1
		}
		return 1
	})
	return list, err
}

// sortByTitle sorts list by title in a stable sort. The order is the
// root collation of Unicode (CLDR), and the case of the letters has no
// effect. The titles are encrypted, thus the database cannot sort them.
func sortByTitle[T any](list []T, title func(T) string) {
	if len(list) < 2 {
		return
	}
	col := collate.New(language.Und, collate.IgnoreCase)
	var buf collate.Buffer
	type item struct {
		key []byte
		v   T
	}
	items := make([]item, len(list))
	for i, v := range list {
		items[i] = item{bytes.Clone(col.KeyFromString(&buf, title(v))), v}
		buf.Reset()
	}
	slices.SortStableFunc(items, func(a, b item) int { return bytes.Compare(a.key, b.key) })
	for i := range items {
		list[i] = items[i].v
	}
}

// SearchResult is one result of a full text search.
type SearchResult struct {
	NodeSummary
	SpaceSlug string
	SpaceName string
	// Snippet is a part of the text.
	Snippet []SnippetPart
}

// maxSearchScan is the largest number of matches that Search reads
// before the permission check. It limits the work for one search.
const maxSearchScan = 10000

// Search finds the folders and documents of the given spaces of a tenant
// with all words of the query in the title or in the text. A word with
// "-" at the start excludes the documents with that word. The text is
// encrypted, thus the search uses the index of keyed hashes of the words
// and of their prefixes and stems. A search word of 3 letters or more
// also finds the words that start with it ("describe" finds
// "describes"), and the words with the same English stem ("describing"
// finds "describe"). A shorter search word finds only the whole word.
// The order of the results:
//  1. all search words are whole words in the title,
//  2. all search words are whole words in the title or the text,
//  3. each search word has the same stem as a word of the node,
//  4. the other results, with parts of words.
//
// In each group, the newest change comes first.
//
// can tells whether the user can read a node of a space with the given
// access path. Search first removes the nodes that the user cannot read.
// Then it keeps at most Space.SearchResults results of each space. Thus
// a user gets the same number of results, also if many matches are in
// folders that the user cannot read.
func (s *Store) Search(ctx context.Context, tenantID int64, spaceIDs []int64, query string,
	can func(spaceID, accessPathID int64) (bool, error)) ([]SearchResult, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	include, exclude := parseSearchQuery(query)
	if len(include) == 0 {
		return nil, nil
	}
	tk, err := s.tenantKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	qt := makeQueryTokens(tk.search, include, exclude)
	args := []any{tenantID, spaceIDs, qt.short, qt.exclude, qt.exact, qt.stems, maxSearchScan}
	// Each long word has its own condition: a word that starts with it,
	// or a word with the same stem. The SQL text contains only
	// placeholders, the values are parameters.
	var long strings.Builder
	for _, w := range qt.long {
		args = append(args, []int64{w.prefix}, []int64{w.stem})
		fmt.Fprintf(&long, " AND (n.prefix_tokens @> $%d OR n.stem_tokens @> $%d)", len(args)-1, len(args))
	}
	// The query does not read the text. Only the results need it.
	rows, err := s.pool.Query(ctx, `
		SELECT `+summaryCols+`, sp.slug, sp.name, sp.search_results
		FROM nodes n
		JOIN spaces sp ON sp.id = n.space_id
		WHERE sp.tenant_id = $1 AND sp.id = ANY($2) AND n.deleted_at IS NULL
		  AND n.search_tokens @> $3 AND NOT (n.search_tokens && $4)`+long.String()+`
		ORDER BY CASE WHEN n.title_tokens @> $5 THEN 0 WHEN n.search_tokens @> $5 THEN 1
		              WHEN n.stem_tokens @> $6 THEN 2 ELSE 3 END,
		         n.updated_at DESC, n.id DESC
		LIMIT $7`, args...)
	if err != nil {
		return nil, err
	}
	type found struct {
		SearchResult
		title []byte
		limit int
	}
	// Collect the rows first. can can use the database, and an open
	// result holds a connection.
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (found, error) {
		var r found
		err := row.Scan(append(summaryDest(&r.NodeSummary, &r.title), &r.SpaceSlug, &r.SpaceName, &r.limit)...)
		return r, err
	})
	if err != nil {
		return nil, err
	}
	var out []SearchResult
	var ids []int64
	count := map[int64]int{}
	for _, r := range list {
		if count[r.SpaceID] >= r.limit {
			continue
		}
		ok, err := can(r.SpaceID, r.AccessPathID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		count[r.SpaceID]++
		if err := s.openSummary(ctx, &r.NodeSummary, r.title); err != nil {
			return nil, err
		}
		out = append(out, r.SearchResult)
		ids = append(ids, r.ID)
	}
	if len(out) == 0 {
		return out, nil
	}
	rows, err = s.pool.Query(ctx, "SELECT id, plain_text FROM nodes WHERE id = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	texts := map[int64][]byte{}
	var id int64
	var text []byte
	if _, err := pgx.ForEachRow(rows, []any{&id, &text}, func() error {
		texts[id] = text
		return nil
	}); err != nil {
		return nil, err
	}
	for i := range out {
		r := &out[i]
		sealed, ok := texts[r.ID]
		if !ok {
			// The node was removed after the first query.
			continue
		}
		plain, err := s.open(ctx, tenantID, plainTextPlace(r.SpaceID, r.PublicID), sealed)
		if err != nil {
			return nil, err
		}
		r.Snippet = snippet(string(plain), include)
	}
	return out, nil
}

// Revisions returns all revisions of a node, newest first. The Body field
// is empty in the result.
func (s *Store) Revisions(ctx context.Context, nodeID int64) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.version, r.title, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       r.comment, r.created_at, n.space_id, n.public_id
		FROM revisions r
		JOIN nodes n ON n.id = r.node_id
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
		var title []byte
		var spaceID int64
		var publicID string
		if err := rows.Scan(&r.Version, &title, &r.Author, &r.Comment, &r.CreatedAt, &spaceID, &publicID); err != nil {
			return nil, err
		}
		var err error
		if r.Title, err = s.openText(ctx, spaceID, revisionTitlePlace(spaceID, publicID, r.Version), title); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revision returns one revision of a node.
func (s *Store) Revision(ctx context.Context, nodeID int64, version int) (*Revision, error) {
	var r Revision
	var title, body []byte
	var spaceID int64
	var publicID string
	err := s.pool.QueryRow(ctx, `
		SELECT r.version, r.title, r.body, COALESCE(NULLIF(u.display_name, ''), u.username, ''),
		       r.comment, r.classification, r.created_at, n.space_id, n.public_id
		FROM revisions r
		JOIN nodes n ON n.id = r.node_id
		LEFT JOIN users u ON u.id = r.author_id
		WHERE r.node_id = $1 AND r.version = $2`, nodeID, version,
	).Scan(&r.Version, &title, &body, &r.Author, &r.Comment, &r.Classification, &r.CreatedAt, &spaceID, &publicID)
	if err != nil {
		return nil, notFound(err)
	}
	if r.Title, err = s.openText(ctx, spaceID, revisionTitlePlace(spaceID, publicID, r.Version), title); err != nil {
		return nil, err
	}
	if r.Body, err = s.openText(ctx, spaceID, revisionPlace(spaceID, publicID, r.Version), body); err != nil {
		return nil, err
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
	return s.sortedSummaries(ctx, `
		SELECT `+summaryCols+` FROM nodes n
		WHERE n.space_id = $1 AND (n.parent_id IS NULL OR n.parent_id = ANY($2)) AND n.deleted_at IS NULL
		ORDER BY n.kind = 'document', n.id
		LIMIT $3`, spaceID, nodeIDs, MaxTreeNodes)
}
