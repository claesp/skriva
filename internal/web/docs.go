package web

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clpe/skriva/internal/markdown"
	"github.com/clpe/skriva/internal/rbac"
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
	mux.Handle("GET "+p+"/raw/{id}", s.node(rbac.DocRead, s.handleRaw))
	mux.Handle("GET "+p+"/access/{id}", s.node(rbac.TenantManage, s.handleNodeAccess))
	mux.Handle("GET "+p+"/access/{id}/default-role", s.node(rbac.TenantManage, s.handleNodeDefaultRoleForm))
	mux.Handle("POST "+p+"/access/{id}/default-role", s.node(rbac.TenantManage, s.handleNodeDefaultRole))
	mux.Handle("GET "+p+"/w/{slug}", s.require("", s.handleWikiLink))
	mux.Handle("GET "+p+"/search", s.require("", s.handleSearch))
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
	ri.New = &newTarget{Folder: true}
	s.render(w, r, http.StatusOK, "space", ri.Space.Name, tree)
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
	// Editor is true on the form of a document. Previewing is true when
	// the form shows the preview instead of the fields. The toolbar then
	// has a preview button.
	Editor     bool
	Previewing bool
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
		Ancestors: readableOnly(r, ancestors), Tree: tree,
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
		if html, err = s.md.Render(n.Body, base(r)+"/w/"); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, http.StatusOK, "view", n.Title, struct {
		nodePage
		HTML     template.HTML
		Children []store.NodeSummary
	}{page, html, readableOnly(r, children)})
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request, n *store.Node) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(n.Body))
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
	Body        string
	Comment     string
	Version     int
	Preview     template.HTML
	Error       string
	// The Node of nodePage is the node that the form edits, or nil for a
	// new node. renderNodeForm sets the other fields.
	nodePage
	// parent is the parent of a new node, or nil for the top level.
	parent *store.Node
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
	f.Body = strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n")
	f.Comment = strings.TrimSpace(r.PostFormValue("comment"))
	if f.IsFolder() {
		f.Body = ""
	}
}

// validateForm checks the form. It returns an error text, or "". For the
// preview action, it also renders the preview.
func (s *Server) validateForm(r *http.Request, f *nodeForm) (string, error) {
	t := reqInfoFrom(r.Context()).Tenant
	switch {
	case f.Title == "":
		return "The title is necessary.", nil
	case utf8.RuneCountInString(f.Title) > maxTitleLen:
		return "The title is too long.", nil
	case !utf8.ValidString(f.Title) || !utf8.ValidString(f.Body) || !utf8.ValidString(f.Comment):
		return "The text contains characters that are not valid.", nil
	case len(f.Body) > t.MaxDocBytes:
		return "The text is too long. The maximum is " + strconv.Itoa(t.MaxDocBytes/1024) + " KiB.", nil
	case utf8.RuneCountInString(f.Comment) > maxCommentLen:
		return "The comment is too long.", nil
	}
	if r.PostFormValue("action") == "preview" && !f.IsFolder() {
		var err error
		f.Previewing = true
		f.Preview, err = s.md.Render(f.Body, base(r)+"/w/")
		return "", err
	}
	return "", nil
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
	msg, err := s.validateForm(r, &f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if msg != "" || f.Previewing || r.PostFormValue("action") == "edit" {
		f.Error = msg
		s.renderNodeForm(w, r, formStatus(msg), "New "+f.Kind, &f)
		return
	}
	ri := reqInfoFrom(r.Context())
	slug := markdown.Slugify(f.Title)
	id, err := s.store.CreateNode(r.Context(), ri.Space.ID, store.NewNode{
		ParentPublicID: f.ParentID, Kind: f.Kind, Title: f.Title, Slug: slug,
		Body: f.Body, Comment: f.Comment, AuthorID: ri.Session.User.ID,
	})
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, docPath(base(r), id, slug), http.StatusSeeOther)
}

// renderNodeForm shows the form with the tree of the space and the path
// to the node, as the page of a node. The buttons for new items in the
// page header use the node, or the parent of a new node.
func (s *Server) renderNodeForm(w http.ResponseWriter, r *http.Request, status int, title string, f *nodeForm) {
	var err error
	previewing := f.Previewing
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
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if f.IsNew {
		f.Crumb = "New " + f.Kind
	}
	f.Editor = !f.IsFolder()
	f.Previewing = previewing
	s.render(w, r, status, "edit", title, f)
}

func formStatus(msg string) int {
	if msg != "" {
		return http.StatusUnprocessableEntity
	}
	return http.StatusOK
}

func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request, n *store.Node) {
	f := nodeForm{
		Action: base(r) + "/edit/" + n.PublicID, Kind: n.Kind, nodePage: nodePage{Node: n},
		Title: n.Title, Body: n.Body, Version: n.Version,
	}
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
		f.Title, f.Body = rev.Title, rev.Body
		f.Comment = "Restore version " + strconv.Itoa(from)
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
	readNodeForm(r, &f)
	msg, err := s.validateForm(r, &f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if msg != "" || f.Previewing || r.PostFormValue("action") == "edit" {
		f.Error = msg
		s.renderNodeForm(w, r, formStatus(msg), "Edit "+f.Title, &f)
		return
	}
	ri := reqInfoFrom(r.Context())
	slug := markdown.Slugify(f.Title)
	err = s.store.UpdateNode(r.Context(), ri.Space.ID, n.PublicID, version, f.Title, slug, f.Body, f.Comment, ri.Session.User.ID)
	if errors.Is(err, store.ErrConflict) {
		f.Error = "A different user changed this item after you started to edit it. " +
			"Copy your text, reload the page and add your changes again."
		s.renderNodeForm(w, r, http.StatusConflict, "Edit "+f.Title, &f)
		return
	}
	if !s.storeResult(w, r, err) {
		return
	}
	http.Redirect(w, r, docPath(base(r), n.PublicID, slug), http.StatusSeeOther)
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
	}{page, count, permitted})
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
	if !s.storeResult(w, r, s.store.DeleteNode(r.Context(), n.SpaceID, n.PublicID)) {
		return
	}
	ri := reqInfoFrom(r.Context())
	s.log.Info("node deleted", "tenant", ri.Tenant.Code, "space", ri.Space.Slug,
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
	}{page, revs})
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
	html, err := s.md.Render(rev.Body, base(r)+"/w/")
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
	slug := markdown.NormalizeSlug(r.PathValue("slug"))
	if slug == "" {
		s.notFound(w, r)
		return
	}
	nodes, err := s.store.NodesBySlug(r.Context(), reqInfoFrom(r.Context()).Space.ID, slug)
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
	title := strings.ReplaceAll(slug, "-", " ")
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
