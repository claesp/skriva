package web

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clpe/skriva/internal/doc"
	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/slug"
	"github.com/clpe/skriva/internal/store"
)

const (
	maxTitleLen   = 200
	maxCommentLen = 500
)

// nodeHandler is a handler for one node. The node wrapper loads the node
// and checks the permission first.
type nodeHandler func(w http.ResponseWriter, r *http.Request, n *store.Node)

// The permissions in a space can be different for each folder. Thus the
// routes require only a session. The handlers check the permission for
// the node, or for the parent of a new node.
func (s *Server) spaceRoutes(mux *http.ServeMux) {
	p := "/{tenant}/{space}"
	mux.Handle("GET "+p+"/{$}", s.require("", s.handleSpaceHome))
	mux.Handle("GET "+p+"/d/{id}", s.node(rbac.DocRead, s.handleView))
	mux.Handle("GET "+p+"/d/{id}/{slug}", s.node(rbac.DocRead, s.handleView))
	mux.Handle("GET "+p+"/new", s.require("", s.handleNewForm))
	mux.Handle("POST "+p+"/new", s.require("", s.handleNew))
	mux.Handle("GET "+p+"/edit/{id}", s.node(rbac.DocEdit, s.handleEditForm))
	mux.Handle("POST "+p+"/edit/{id}", s.node(rbac.DocEdit, s.handleEdit))
	mux.Handle("GET "+p+"/move/{id}", s.node(rbac.DocEdit, s.handleMoveForm))
	mux.Handle("POST "+p+"/move/{id}", s.node(rbac.DocEdit, s.handleMove))
	mux.Handle("GET "+p+"/delete/{id}", s.node(rbac.DocDelete, s.handleDeleteForm))
	mux.Handle("POST "+p+"/delete/{id}", s.node(rbac.DocDelete, s.handleDelete))
	mux.Handle("GET "+p+"/history/{id}", s.node(rbac.DocRead, s.handleHistory))
	mux.Handle("GET "+p+"/history/{id}/{version}", s.node(rbac.DocRead, s.handleRevision))
	mux.Handle("GET "+p+"/compare/{id}", s.node(rbac.DocRead, s.handleCompare))
	mux.Handle("GET "+p+"/export/{id}", s.node(rbac.DocRead, s.handleExportPage))
	mux.Handle("GET "+p+"/export/{id}/{format}", s.node(rbac.DocRead, s.handleExport))
	mux.Handle("GET "+p+"/export/{id}/{format}/text", s.node(rbac.DocRead, s.handleExportText))
	mux.Handle("GET "+p+"/access/{id}", s.node(rbac.TenantManage, s.handleNodeAccess))
	mux.Handle("GET "+p+"/access/{id}/default-role", s.node(rbac.TenantManage, s.handleNodeDefaultRoleForm))
	mux.Handle("POST "+p+"/access/{id}/default-role", s.node(rbac.TenantManage, s.handleNodeDefaultRole))
	mux.Handle("GET "+p+"/w/{slug}", s.require("", s.handleWikiLink))
	mux.Handle("GET "+p+"/trash", s.require("", s.handleTrash))
	mux.Handle("POST "+p+"/trash/{id}/restore", s.trashed(rbac.DocDelete, s.handleRestore))
	mux.Handle("GET "+p+"/trash/{id}/delete", s.trashed(rbac.SpaceManage, s.handlePurgeForm))
	mux.Handle("POST "+p+"/trash/{id}/delete", s.trashed(rbac.SpaceManage, s.handlePurge))
	mux.Handle("GET "+p+"/search", s.require("", s.handleSearch))
	mux.Handle("GET "+p+"/link-targets", s.require("", s.handleLinkTargets))
	mux.Handle("POST "+p+"/files", s.require("", s.handleUpload))
	mux.Handle("GET "+p+"/files/{id}", s.require("", s.handleFile))
}

// parseVersion parses a node version. Versions are PostgreSQL integer
// values, thus the function accepts only values that fit in 32 bits.
func parseVersion(s string) (int, bool) {
	v, err := strconv.ParseInt(s, 10, 32)
	return int(v), err == nil && v >= 0
}

// base returns the URL path of the space of the request.
func base(r *http.Request) string {
	ri := reqInfoFrom(r.Context())
	return spacePath(ri.Tenant, ri.Space)
}

// node loads the node from the {id} path value and checks perm for that
// node. A node that the user cannot read is "not found". Thus the server
// does not tell that the node exists. The handler and the templates then
// see the permissions of the node.
func (s *Server) node(perm rbac.Permission, h nodeHandler) http.Handler {
	return s.require("", func(w http.ResponseWriter, r *http.Request) {
		ri := reqInfoFrom(r.Context())
		// Public IDs are in lower case. An ID in upper case also works.
		id := strings.ToLower(r.PathValue("id"))
		if !store.ValidPublicID(id) {
			s.notFound(w, r)
			return
		}
		n, err := s.store.Node(r.Context(), ri.Space.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		perms := ri.Access.For(n.AccessPathID)
		if !perms.Has(rbac.DocRead) {
			s.notFound(w, r)
			return
		}
		if !perms.Has(perm) {
			s.forbidden(w, r)
			return
		}
		ri.setPermissions(perms)
		h(w, r, n)
	})
}

// canRead reports whether the user can read a node with the given access
// path.
func canRead(r *http.Request, pathID int64) bool {
	return reqInfoFrom(r.Context()).Access.Can(pathID, rbac.DocRead)
}

// readableOnly returns the nodes that the user can read.
func readableOnly(r *http.Request, nodes []store.NodeSummary) []store.NodeSummary {
	out := nodes[:0:0]
	for _, n := range nodes {
		if canRead(r, n.AccessPathID) {
			out = append(out, n)
		}
	}
	return out
}

// tree returns the tree of the space with the nodes that the user can
// read. The current node and its ancestors are open. ancestors are the
// ancestors of the current node. For a large space, the tree contains
// only the top level and the children of the current node and its
// ancestors.
func (s *Server) tree(r *http.Request, current int64, ancestors []store.NodeSummary) ([]*store.TreeNode, error) {
	spaceID := reqInfoFrom(r.Context()).Space.ID
	large, err := s.store.IsLargeSpace(r.Context(), spaceID)
	if err != nil {
		return nil, err
	}
	var nodes []store.NodeSummary
	if large {
		ids := make([]int64, 0, len(ancestors)+1)
		for _, a := range ancestors {
			ids = append(ids, a.ID)
		}
		if current != 0 {
			ids = append(ids, current)
		}
		nodes, err = s.store.PathTreeNodes(r.Context(), spaceID, ids)
	} else {
		nodes, err = s.store.SpaceNodes(r.Context(), spaceID)
	}
	if err != nil {
		return nil, err
	}
	paths := make(map[int64]int64, len(nodes))
	for _, n := range nodes {
		paths[n.ID] = n.AccessPathID
	}
	return store.BuildVisibleTree(nodes, current, func(id int64) bool {
		p, ok := paths[id]
		return ok && canRead(r, p)
	}), nil
}

func (s *Server) handleSpaceHome(w http.ResponseWriter, r *http.Request) {
	ri := reqInfoFrom(r.Context())
	tree, err := s.tree(r, 0, nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(tree) == 0 && !ri.Access.Root().Has(rbac.DocRead) {
		s.forbidden(w, r)
		return
	}
	top := make([]store.NodeSummary, len(tree))
	for i, t := range tree {
		top[i] = t.NodeSummary
	}
	items, err := s.items(r, top)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ri.New = &newTarget{Folder: true}
	s.render(w, r, http.StatusOK, "space", ri.Space.Name, struct {
		spacePage
		Items itemsArgs
	}{spacePage{Tree: tree, Trash: ri.Access.Any(rbac.DocDelete)}, items})
}

// items returns the data for a table of the nodes. The nodes must be
// nodes that the user can read.
func (s *Server) items(r *http.Request, nodes []store.NodeSummary) (itemsArgs, error) {
	ids := make([]int64, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	details, err := s.store.ItemDetails(r.Context(), reqInfoFrom(r.Context()).Space.ID, ids)
	return itemsArgs{Base: base(r), Nodes: nodes, Details: details}, err
}

// spacePage is the data that the pages of a space show: the tree, and
// whether the toolbar has the button of the trash.
type spacePage struct {
	Tree  []*store.TreeNode
	Trash bool
}

// nodePage is the data that all pages of a node show: the path, the
// toolbar and the tree. Tab is the current button in the toolbar, or ""
// on the page of the node. The current button opens CloseURL: by default
// the page of the node. Crumb is an optional last part of the path.
type nodePage struct {
	Node      *store.Node
	Tab       string
	CloseURL  string
	Crumb     string
	Ancestors []store.NodeSummary
	Tree      []*store.TreeNode
	// Editor is true on the form of a document.
	Editor bool
	// SaveForm is the ID of the form of the page, or "". The bar at the
	// top then has the Save button of the form.
	SaveForm string
	// NewKind is the kind of the item on the form of a new item, or "".
	// The toolbar then shows the buttons of that kind.
	NewKind string
	// Classification is the information classification of the document,
	// or "" for a folder. The page shows it in a bar below the toolbar.
	Classification string
}

// loadNodePage loads the path and the tree for a page of node n. It also
// sets the place for the buttons for new items in the page header.
func (s *Server) loadNodePage(r *http.Request, n *store.Node, tab, crumb string) (nodePage, error) {
	ancestors, err := s.store.Ancestors(r.Context(), n.ID)
	if err != nil {
		return nodePage{}, err
	}
	tree, err := s.tree(r, n.ID, ancestors)
	if err != nil {
		return nodePage{}, err
	}
	reqInfoFrom(r.Context()).New = &newTarget{Parent: n.PublicID, Folder: n.IsFolder()}
	return nodePage{
		Node: n, Tab: tab, Crumb: crumb, CloseURL: docPath(base(r), n.PublicID, n.Slug),
		Ancestors: readableOnly(r, ancestors), Tree: tree, Classification: n.Classification,
	}, nil
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request, n *store.Node) {
	// Send the browser to the canonical URL if the slug is missing or
	// old. The slug changes when the title changes. The case of the
	// letters has no effect, but the redirect gives the ID and the slug
	// in lower case.
	if (r.PathValue("slug") != n.Slug && n.Slug != "") || r.PathValue("id") != n.PublicID {
		http.Redirect(w, r, docPath(base(r), n.PublicID, n.Slug), http.StatusFound)
		return
	}
	page, err := s.loadNodePage(r, n, "", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	children, err := s.store.Children(r.Context(), n.SpaceID, &n.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var html template.HTML
	if !n.IsFolder() {
		if html, err = renderBody(r, n.Body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	children = readableOnly(r, children)
	var items itemsArgs
	if n.IsFolder() {
		if items, err = s.items(r, children); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	backlinks, err := s.store.Backlinks(r.Context(), n)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "view", n.Title, struct {
		nodePage
		HTML      template.HTML
		Children  []store.NodeSummary
		Items     itemsArgs
		Backlinks []store.NodeSummary
	}{page, html, children, items, readableOnly(r, backlinks)})
}

// links returns the URL prefixes of wiki links, document links and files
// in the space of the request.
func links(r *http.Request) doc.Links {
	b := base(r)
	return doc.Links{Wiki: b + "/w/", Files: b + "/files/", Doc: b + "/d/"}
}

// renderBody converts a saved body to HTML.
func renderBody(r *http.Request, body string) (template.HTML, error) {
	d, err := doc.Parse([]byte(body), nil)
	if err != nil {
		return "", err
	}
	return d.HTML(links(r)), nil
}

// editorBody converts a saved body to the JSON for the editor. The
// "file:" sources of images become URLs.
func editorBody(r *http.Request, body string) (string, error) {
	d, err := doc.Parse([]byte(body), nil)
	if err != nil {
		return "", err
	}
	d.MapImages(links(r).Src)
	return string(d.JSON()), nil
}

// parseEditorBody reads the JSON from the editor and removes all items
// that are not permitted. The URLs of files in the space of the request
// become "file:" sources.
func (s *Server) parseEditorBody(r *http.Request, body string) (*doc.Node, error) {
	prefix := links(r).Files
	site := strings.TrimSuffix(s.opts.BaseURL.String(), "/")
	return doc.Parse([]byte(body), func(src string) string {
		src = strings.TrimPrefix(src, site)
		if id, ok := strings.CutPrefix(src, prefix); ok {
			return doc.FilePrefix + strings.ToLower(id)
		}
		return src
	})
}

// nodeForm is the data for the form that makes or edits a node.
type nodeForm struct {
	// Action is the URL of the form.
	Action      string
	IsNew       bool
	Kind        string
	ParentID    string
	ParentTitle string
	Title       string
	// Body is the text in the JSON format of the editor. The sources of
	// uploaded images are URLs.
	Body    string
	Comment string
	Version int
	// RestoredFrom is the old version that the form restores, or 0.
	RestoredFrom int
	// ClassificationID is the selected classification of a document, or
	// 0 for the default. Classifications are the levels of the tenant.
	ClassificationID int64
	Classifications  []store.Classification
	Error            string
	// UploadURL is the address for uploaded images.
	UploadURL string
	// EditorURL is the address of the script of the editor.
	EditorURL string
	// The Node of nodePage is the node that the form edits, or nil for a
	// new node. renderNodeForm sets the other fields.
	nodePage
	// parent is the parent of a new node, or nil for the top level.
	parent *store.Node
	// content is the checked text. validateForm sets it.
	content store.Content
}

// IsFolder reports whether the form is for a folder.
func (f nodeForm) IsFolder() bool { return f.Kind == store.KindFolder }

func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := nodeForm{
		Action:   base(r) + "/new",
		IsNew:    true,
		Kind:     q.Get("kind"),
		ParentID: q.Get("parent"),
		Title:    strings.TrimSpace(q.Get("title")),
	}
	if f.Kind != store.KindFolder {
		f.Kind = store.KindDocument
	}
	if utf8.RuneCountInString(f.Title) > maxTitleLen {
		f.Title = ""
	}
	if !s.checkParent(w, r, &f) {
		return
	}
	s.renderNodeForm(w, r, http.StatusOK, "New "+f.Kind, &f)
}

// checkParent makes sure that the parent in the form exists, that the user
// can create nodes in it, and that it can contain a node of the kind in
// the form. It sets ParentTitle and the permissions of the request.
func (s *Server) checkParent(w http.ResponseWriter, r *http.Request, f *nodeForm) bool {
	ri := reqInfoFrom(r.Context())
	perms := ri.Access.Root()
	if f.ParentID != "" {
		if !store.ValidPublicID(f.ParentID) {
			s.badRequest(w, r, "The parent is not valid.")
			return false
		}
		p, err := s.store.Node(r.Context(), ri.Space.ID, f.ParentID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.serverError(w, r, err)
			return false
		}
		if err != nil || !canRead(r, p.AccessPathID) {
			s.badRequest(w, r, "The parent does not exist.")
			return false
		}
		if f.Kind == store.KindFolder && !p.IsFolder() {
			s.badRequest(w, r, "A folder can be only in a folder or at the top of the space.")
			return false
		}
		f.ParentTitle = p.Title
		f.parent = p
		perms = ri.Access.For(p.AccessPathID)
	}
	if !perms.Has(rbac.DocCreate) {
		s.forbidden(w, r)
		return false
	}
	ri.setPermissions(perms)
	return true
}

// readNodeForm reads the fields of the node form from the request.
func readNodeForm(r *http.Request, f *nodeForm) {
	f.Title = strings.TrimSpace(r.PostFormValue("title"))
	f.Body = r.PostFormValue("body")
	f.Comment = strings.TrimSpace(r.PostFormValue("comment"))
	// The store checks that the level belongs to the tenant.
	f.ClassificationID, _ = strconv.ParseInt(r.PostFormValue("classification"), 10, 64)
	if f.IsFolder() {
		f.Body = ""
	}
}

// validateForm checks the form. It returns an error text, or "". It
// removes the items that are not permitted from the text and sets
// f.content.
func (s *Server) validateForm(r *http.Request, f *nodeForm) string {
	t := reqInfoFrom(r.Context()).Tenant
	switch {
	case !utf8.ValidString(f.Title) || !utf8.ValidString(f.Body) || !utf8.ValidString(f.Comment):
		return "The text contains characters that are not valid."
	case len(f.Body) > t.MaxDocBytes:
		return "The text is too long. The maximum is " + strconv.Itoa(t.MaxDocBytes/1024) + " KiB."
	}
	if !f.IsFolder() {
		d, err := s.parseEditorBody(r, f.Body)
		if err != nil {
			return "The text is not in the correct format."
		}
		docLinks, wikiLinks := d.Links()
		f.content = store.Content{
			Body: string(d.JSON()), PlainText: d.PlainText(), Files: d.Files(),
			DocLinks: docLinks, WikiLinks: wikiLinks,
		}
		d.MapImages(links(r).Src)
		f.Body = string(d.JSON())
	}
	switch {
	case f.Title == "":
		return "The title is necessary."
	case utf8.RuneCountInString(f.Title) > maxTitleLen:
		return "The title is too long."
	case utf8.RuneCountInString(f.Comment) > maxCommentLen:
		return "The comment is too long."
	}
	return ""
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	f := nodeForm{
		Action:   base(r) + "/new",
		IsNew:    true,
		Kind:     r.PostFormValue("kind"),
		ParentID: r.PostFormValue("parent"),
	}
	if f.Kind != store.KindFolder && f.Kind != store.KindDocument {
		s.badRequest(w, r, "The form is not valid.")
		return
	}
	readNodeForm(r, &f)
	if !s.checkParent(w, r, &f) {
		return
	}
	if msg := s.validateForm(r, &f); msg != "" {
		f.Error = msg
		s.renderNodeForm(w, r, http.StatusUnprocessableEntity, "New "+f.Kind, &f)
		return
	}
	ri := reqInfoFrom(r.Context())
	sl := slug.Make(f.Title)
	id, err := s.store.CreateNode(r.Context(), ri.Space.ID, store.NewNode{
		ParentPublicID: f.ParentID, Kind: f.Kind, Title: f.Title,
		Content: f.content, Comment: f.Comment, AuthorID: ri.Session.User.ID,
		ClassificationID: f.ClassificationID,
	})
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, docPath(base(r), id, sl), http.StatusSeeOther)
}

// renderNodeForm shows the form with the tree of the space and the path
// to the node, as the page of a node. The buttons for new items in the
// page header use the node, or the parent of a new node.
func (s *Server) renderNodeForm(w http.ResponseWriter, r *http.Request, status int, title string, f *nodeForm) {
	var err error
	switch {
	case f.Node != nil:
		f.nodePage, err = s.loadNodePage(r, f.Node, "edit", "")
	case f.parent != nil:
		f.nodePage, err = s.loadNodePage(r, f.parent, "", "")
		if err == nil {
			// The path of a new node ends with the parent.
			p := f.parent
			f.Ancestors = append(f.Ancestors, store.NodeSummary{
				ID: p.ID, PublicID: p.PublicID, Kind: p.Kind, Title: p.Title, Slug: p.Slug,
			})
			f.Node = nil
		}
	default:
		reqInfoFrom(r.Context()).New = &newTarget{Folder: true}
		f.Tree, err = s.tree(r, 0, nil)
		f.CloseURL = base(r) + "/"
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if f.IsNew {
		f.Crumb = "New " + f.Kind
		f.NewKind = f.Kind
	}
	f.Editor = !f.IsFolder()
	f.SaveForm = "nodeform"
	f.Classification = ""
	if f.Editor {
		if f.Classifications, err = s.store.Classifications(r.Context(), reqInfoFrom(r.Context()).Tenant.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		// The form shows the selected level, the level of the document,
		// or the default level of the tenant.
		if f.ClassificationID == 0 && f.Node != nil {
			f.ClassificationID = f.Node.ClassificationID
		}
		for _, c := range f.Classifications {
			if c.ID == f.ClassificationID || (f.ClassificationID == 0 && c.IsDefault) {
				f.ClassificationID, f.Classification = c.ID, c.Name
			}
		}
	}
	if f.Editor {
		// The upload checks the permission for the node, or for the
		// parent of a new node.
		f.UploadURL = base(r) + "/files"
		switch {
		case !f.IsNew:
			f.UploadURL += "?node=" + url.QueryEscape(f.Node.PublicID)
		case f.ParentID != "":
			f.UploadURL += "?parent=" + url.QueryEscape(f.ParentID)
		}
		f.EditorURL = s.staticURL("editor.js")
		if f.Body == "" {
			f.Body = doc.EmptyJSON
		}
		// Only the form of a document runs a script: the editor.
		w.Header().Set("Content-Security-Policy", editorSecurityPolicy)
	}
	s.render(w, r, status, "edit", title, f)
}

func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request, n *store.Node) {
	f := nodeForm{
		Action: base(r) + "/edit/" + n.PublicID, Kind: n.Kind, nodePage: nodePage{Node: n},
		Title: n.Title, Version: n.Version,
	}
	body := n.Body
	// "from" loads the text of an old revision. The user can then save
	// it as a new version to restore the node.
	if from, ok := parseVersion(r.URL.Query().Get("from")); ok && from > 0 && from != n.Version {
		rev, err := s.store.Revision(r.Context(), n.ID, from)
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		f.Title, body = rev.Title, rev.Body
		f.Comment = "Restore version " + strconv.Itoa(from)
		f.RestoredFrom = from
	}
	if !n.IsFolder() {
		var err error
		if f.Body, err = editorBody(r, body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.renderNodeForm(w, r, http.StatusOK, "Edit "+n.Title, &f)
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request, n *store.Node) {
	version, ok := parseVersion(r.PostFormValue("version"))
	if !ok || version < 1 {
		s.badRequest(w, r, "The form is not valid.")
		return
	}
	f := nodeForm{Action: base(r) + "/edit/" + n.PublicID, Kind: n.Kind, nodePage: nodePage{Node: n}, Version: version}
	if v := r.PostFormValue("restored_from"); v != "" {
		from, ok := parseVersion(v)
		if !ok || from < 1 || from >= version+1 {
			s.badRequest(w, r, "The form is not valid.")
			return
		}
		f.RestoredFrom = from
	}
	readNodeForm(r, &f)
	if msg := s.validateForm(r, &f); msg != "" {
		f.Error = msg
		s.renderNodeForm(w, r, http.StatusUnprocessableEntity, "Edit "+f.Title, &f)
		return
	}
	ri := reqInfoFrom(r.Context())
	sl := slug.Make(f.Title)
	err := s.store.UpdateNode(r.Context(), ri.Space.ID, n.PublicID, store.NodeUpdate{
		BaseVersion: version, Title: f.Title, Content: f.content,
		Comment: f.Comment, AuthorID: ri.Session.User.ID, RestoredFrom: f.RestoredFrom,
		ClassificationID: f.ClassificationID,
	})
	if errors.Is(err, store.ErrConflict) {
		f.Error = "A different user changed this item after you started to edit it. " +
			"Copy your text, open the item again and add your changes."
		s.renderNodeForm(w, r, http.StatusConflict, "Edit "+f.Title, &f)
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, docPath(base(r), n.PublicID, sl), http.StatusSeeOther)
}

// moveTarget is one possible new parent in the move form.
type moveTarget struct {
	PublicID string
	Label    string
	Selected bool
}

func (s *Server) handleMoveForm(w http.ResponseWriter, r *http.Request, n *store.Node) {
	nodes, err := s.store.SpaceNodes(r.Context(), n.SpaceID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadNodePage(r, n, "move", "Move")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	acc := reqInfoFrom(r.Context()).Access
	s.render(w, r, http.StatusOK, "move", "Move "+n.Title, struct {
		nodePage
		Targets  []moveTarget
		AtRoot   bool
		RootOpen bool
	}{page, moveTargets(nodes, n, acc), n.ParentID == nil, acc.Root().Has(rbac.DocCreate)})
}

// moveTargets returns the nodes that can be the new parent of n. The
// result does not include n and the subtree of n. For a folder, the
// result includes only folders. The result includes only nodes in which
// the user can create nodes. Each label is the path from the root, with
// only the titles that the user can read.
func moveTargets(nodes []store.NodeSummary, n *store.Node, acc *store.Access) []moveTarget {
	byID := make(map[int64]*store.NodeSummary, len(nodes))
	for i := range nodes {
		byID[nodes[i].ID] = &nodes[i]
	}
	// inSubtree reports whether id is n or a descendant of n. The depth
	// limit stops the loop if the data contains a cycle.
	inSubtree := func(id int64) bool {
		for i := 0; i <= len(nodes); i++ {
			if id == n.ID {
				return true
			}
			p := byID[id]
			if p == nil || p.ParentID == nil {
				return false
			}
			id = *p.ParentID
		}
		return true
	}
	label := func(x *store.NodeSummary) string {
		parts := []string{x.Title}
		for i := 0; x.ParentID != nil && i < len(nodes); i++ {
			if x = byID[*x.ParentID]; x == nil {
				break
			}
			if acc.Can(x.AccessPathID, rbac.DocRead) {
				parts = append(parts, x.Title)
			} else {
				parts = append(parts, "…")
			}
		}
		for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
		return strings.Join(parts, " / ")
	}
	var out []moveTarget
	for i := range nodes {
		x := &nodes[i]
		if (n.IsFolder() && !x.IsFolder()) || inSubtree(x.ID) ||
			!acc.Can(x.AccessPathID, rbac.DocRead) || !acc.Can(x.AccessPathID, rbac.DocCreate) {
			continue
		}
		out = append(out, moveTarget{
			PublicID: x.PublicID,
			Label:    label(x),
			Selected: n.ParentID != nil && *n.ParentID == x.ID,
		})
	}
	return out
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request, n *store.Node) {
	ri := reqInfoFrom(r.Context())
	parent := r.PostFormValue("parent")
	// The user must be able to create nodes at the new place.
	target := ri.Access.Root()
	if parent != "" {
		if !store.ValidPublicID(parent) {
			s.badRequest(w, r, "The new place is not valid.")
			return
		}
		p, err := s.store.Node(r.Context(), n.SpaceID, parent)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.serverError(w, r, err)
			return
		}
		if err != nil || !canRead(r, p.AccessPathID) {
			s.badRequest(w, r, "The new place does not exist.")
			return
		}
		target = ri.Access.For(p.AccessPathID)
	}
	if !target.Has(rbac.DocCreate) {
		s.forbidden(w, r)
		return
	}
	err := s.store.MoveNode(r.Context(), n.SpaceID, n.PublicID, parent)
	if errors.Is(err, store.ErrInvalid) {
		s.badRequest(w, r, "You cannot move the item to that place. "+
			"A folder can be only in a folder, and no item can be in its own subtree.")
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, docPath(base(r), n.PublicID, n.Slug), http.StatusSeeOther)
}

// canDeleteSubtree reports whether the user can delete each node in the
// subtree of n. A deletion also removes the subtree. Thus a user cannot
// delete items that the user cannot delete one by one. The check uses the
// access paths: many nodes have the same path.
func (s *Server) canDeleteSubtree(r *http.Request, n *store.Node) (bool, error) {
	acc := reqInfoFrom(r.Context()).Access
	paths, err := s.store.SubtreePaths(r.Context(), n.ID)
	if err != nil {
		return false, err
	}
	for _, p := range paths {
		if !acc.Can(p, rbac.DocDelete) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Server) handleDeleteForm(w http.ResponseWriter, r *http.Request, n *store.Node) {
	count, err := s.store.CountDescendants(r.Context(), n.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	permitted, err := s.canDeleteSubtree(r, n)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadNodePage(r, n, "delete", "Delete")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "delete", "Delete "+n.Title, struct {
		nodePage
		Descendants int
		Permitted   bool
		TrashDays   int
	}{page, count, permitted, store.TrashDays})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, n *store.Node) {
	permitted, err := s.canDeleteSubtree(r, n)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !permitted {
		s.renderError(w, r, http.StatusForbidden,
			"You cannot delete this item. It contains items that you do not have permission to delete.")
		return
	}
	ancestors, err := s.store.Ancestors(r.Context(), n.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ri := reqInfoFrom(r.Context())
	if !s.storeResult(w, r, s.store.TrashNode(r.Context(), n.SpaceID, n.PublicID, ri.Session.User.ID)) {
		return
	}
	s.log.Info("node moved to the trash", "tenant", ri.Tenant.Code, "space", ri.Space.Slug,
		"node", n.PublicID, "user_id", ri.Session.User.ID)
	target := base(r) + "/"
	if a := readableOnly(r, ancestors); len(a) > 0 {
		p := a[len(a)-1]
		target = docPath(base(r), p.PublicID, p.Slug)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, n *store.Node) {
	revs, err := s.store.Revisions(r.Context(), n.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadNodePage(r, n, "history", "History")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "history", "History of "+n.Title, struct {
		nodePage
		Revisions []store.Revision
		Compare   compareForm
	}{page, revs, compareForm{
		Action: base(r) + "/compare/" + n.PublicID, Versions: revs, From: n.Version - 1, To: n.Version,
	}})
}

func (s *Server) handleRevision(w http.ResponseWriter, r *http.Request, n *store.Node) {
	version, ok := parseVersion(r.PathValue("version"))
	if !ok || version < 1 {
		s.notFound(w, r)
		return
	}
	rev, err := s.store.Revision(r.Context(), n.ID, version)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	html, err := renderBody(r, rev.Body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadNodePage(r, n, "history", "Version "+strconv.Itoa(version))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page.CloseURL = base(r) + "/history/" + n.PublicID
	// The bar shows the classification of the version.
	if rev.Classification != "" {
		page.Classification = rev.Classification
	}
	s.render(w, r, http.StatusOK, "revision", rev.Title+" (version "+strconv.Itoa(version)+")", struct {
		nodePage
		Revision *store.Revision
		HTML     template.HTML
	}{page, rev, html})
}

// handleWikiLink finds the target of a wiki link [[Title]]. One readable
// match sends the browser to the node. More matches show a list. No match
// shows a page with a link to make the document.
func (s *Server) handleWikiLink(w http.ResponseWriter, r *http.Request) {
	sl := slug.Normalize(r.PathValue("slug"))
	if sl == "" {
		s.notFound(w, r)
		return
	}
	nodes, err := s.store.NodesBySlug(r.Context(), reqInfoFrom(r.Context()).Space.ID, sl)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	nodes = readableOnly(r, nodes)
	if len(nodes) == 1 {
		http.Redirect(w, r, docPath(base(r), nodes[0].PublicID, nodes[0].Slug), http.StatusSeeOther)
		return
	}
	status := http.StatusOK
	if len(nodes) == 0 {
		status = http.StatusNotFound
	}
	title := strings.ReplaceAll(sl, "-", " ")
	s.render(w, r, status, "wikilink", "Link: "+title, struct {
		Title     string
		CreateURL string
		Nodes     []store.NodeSummary
	}{title, base(r) + "/new?title=" + url.QueryEscape(title), nodes})
}

// handleNodeAccess shows the default role and the role bindings of a
// folder or a document.
func (s *Server) handleNodeAccess(w http.ResponseWriter, r *http.Request, n *store.Node) {
	table, err := s.bindings(r, func(b store.Binding) bool { return b.NodeID == n.ID }, r.URL.Path, true, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page, err := s.loadNodePage(r, n, "access", "Access")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "node-access", "Access to "+n.Title, struct {
		nodePage
		DefaultRoleText string
		Table           bindingTable
	}{page, defaultRoleText(n.DefaultRole, "parent"), table})
}

func (s *Server) handleNodeDefaultRoleForm(w http.ResponseWriter, r *http.Request, n *store.Node) {
	roles, err := s.store.Roles(r.Context(), tenantOf(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// The select uses "-" for "same as the parent".
	defaultRole := "-"
	if n.DefaultRole != nil {
		defaultRole = *n.DefaultRole
	}
	page, err := s.loadNodePage(r, n, "access", "Default role")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page.CloseURL = base(r) + "/access/" + n.PublicID
	s.render(w, r, http.StatusOK, "node-default-role", "Default role", struct {
		nodePage
		DefaultRole string
		Roles       []store.Role
	}{page, defaultRole, roles})
}

// handleNodeDefaultRole sets the default role of a folder or a document.
// The value "-" means "same as the parent", and "" means no default role.
func (s *Server) handleNodeDefaultRole(w http.ResponseWriter, r *http.Request, n *store.Node) {
	ri := reqInfoFrom(r.Context())
	role, ok := defaultRoleValue(r)
	if !ok {
		s.badRequest(w, r, "The role is not valid.")
		return
	}
	if !s.storeResult(w, r, s.store.SetNodeDefaultRole(r.Context(), ri.Tenant.ID, ri.Space.ID, n.PublicID, role)) {
		return
	}
	s.log.Info("node default role changed", "tenant", ri.Tenant.Code, "space", ri.Space.Slug,
		"node", n.PublicID, "user_id", ri.Session.User.ID)
	http.Redirect(w, r, base(r)+"/access/"+n.PublicID, http.StatusSeeOther)
}
