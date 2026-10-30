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
	pageSize       = 100
	maxTitleLen    = 200
	maxCommentLen  = 500
	maxSearchLen   = 200
	searchPageSize = 50
	maxListPage    = 100_000
)

// parseVersion parses a page version. Versions are PostgreSQL integer
// values, thus the function accepts only values that fit in 32 bits.
func parseVersion(s string) (int, bool) {
	v, err := strconv.ParseInt(s, 10, 32)
	return int(v), err == nil && v >= 0
}

// pageSlug returns the slug from the URL, or "" if it is not valid.
func pageSlug(r *http.Request) string {
	slug := r.PathValue("slug")
	if !markdown.ValidSlug(slug) {
		return ""
	}
	return slug
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	// The limit prevents an integer overflow in the offset.
	page = min(max(page, 1), maxListPage)
	list, err := s.store.ListPages(r.Context(), pageSize+1, (page-1)*pageSize)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	more := len(list) > pageSize
	if more {
		list = list[:pageSize]
	}
	s.render(w, r, http.StatusOK, "index", "All pages", struct {
		Pages []store.PageSummary
		Page  int
		More  bool
	}{list, page, more})
}

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.RecentPages(r.Context(), 50)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "recent", "Recent changes", list)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > maxSearchLen {
		s.badRequest(w, r, "The search text is too long.")
		return
	}
	var results []store.PageSummary
	if q != "" {
		var err error
		results, err = s.store.SearchPages(r.Context(), q, searchPageSize)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, http.StatusOK, "search", "Search", struct {
		Query   string
		Results []searchResult
	}{q, toSearchResults(results)})
}

type searchResult struct {
	store.PageSummary
	SnippetHTML template.HTML
}

// toSearchResults converts the « and » marks from the database to <mark>
// elements. The function escapes all other text.
func toSearchResults(in []store.PageSummary) []searchResult {
	out := make([]searchResult, len(in))
	for i, p := range in {
		esc := template.HTMLEscapeString(p.Snippet)
		esc = strings.ReplaceAll(esc, "«", "<mark>")
		esc = strings.ReplaceAll(esc, "»", "</mark>")
		out[i] = searchResult{PageSummary: p, SnippetHTML: template.HTML(esc)} // #nosec G203 -- esc is escaped above.
	}
	return out
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	p, err := s.store.Page(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, http.StatusNotFound, "missing", "Page not found", slug)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	html, err := s.md.Render(p.Body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "view", p.Title, struct {
		Page *store.Page
		HTML template.HTML
	}{p, html})
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	p, err := s.store.Page(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(p.Body))
}

// editData is the data for the edit form.
type editData struct {
	Slug    string
	IsNew   bool
	Title   string
	Body    string
	Comment string
	Version int
	Preview template.HTML
	Error   string
}

func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	sess := sessionFrom(r.Context())
	p, err := s.store.Page(r.Context(), slug)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if !sess.Permissions.Has(rbac.PageCreate) {
			s.forbidden(w, r)
			return
		}
		title := strings.TrimSpace(r.URL.Query().Get("title"))
		if title == "" || markdown.Slugify(title) != slug {
			title = slug
		}
		s.render(w, r, http.StatusOK, "edit", "New page", editData{Slug: slug, IsNew: true, Title: title})
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if !sess.Permissions.Has(rbac.PageEdit) {
		s.forbidden(w, r)
		return
	}

	d := editData{Slug: slug, Title: p.Title, Body: p.Body, Version: p.Version}
	// "from" loads the text of an old revision. The user can then save
	// it as a new version to restore the page.
	if from, ok := parseVersion(r.URL.Query().Get("from")); ok && from > 0 && from != p.Version {
		rev, err := s.store.Revision(r.Context(), slug, from)
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		d.Title, d.Body = rev.Title, rev.Body
		d.Comment = "Restore version " + strconv.Itoa(from)
	}
	s.render(w, r, http.StatusOK, "edit", "Edit "+p.Title, d)
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	sess := sessionFrom(r.Context())
	version, ok := parseVersion(r.PostFormValue("version"))
	if !ok {
		s.badRequest(w, r, "The form is not valid.")
		return
	}
	var err error
	d := editData{
		Slug:    slug,
		IsNew:   version == 0,
		Title:   strings.TrimSpace(r.PostFormValue("title")),
		Body:    normalizeNewlines(r.PostFormValue("body")),
		Comment: strings.TrimSpace(r.PostFormValue("comment")),
		Version: version,
	}
	need := rbac.PageEdit
	if d.IsNew {
		need = rbac.PageCreate
	}
	if !sess.Permissions.Has(need) {
		s.forbidden(w, r)
		return
	}

	d.Error = s.validatePage(d.Title, d.Body, d.Comment)
	if d.Error == "" && r.PostFormValue("action") == "preview" {
		d.Preview, err = s.md.Render(d.Body)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if d.Error != "" || d.Preview != "" {
		status := http.StatusOK
		if d.Error != "" {
			status = http.StatusUnprocessableEntity
		}
		s.render(w, r, status, "edit", "Edit "+d.Title, d)
		return
	}

	if d.IsNew {
		err = s.store.CreatePage(r.Context(), slug, d.Title, d.Body, d.Comment, sess.User.ID)
	} else {
		err = s.store.UpdatePage(r.Context(), slug, version, d.Title, d.Body, d.Comment, sess.User.ID)
	}
	switch {
	case errors.Is(err, store.ErrConflict):
		d.Error = "Another user changed this page after you started to edit it. " +
			"Copy your text, reload the page and add your changes again."
		if d.IsNew {
			d.Error = "A page with this name exists already."
		}
		s.render(w, r, http.StatusConflict, "edit", "Edit "+d.Title, d)
		return
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/wiki/"+slug, http.StatusSeeOther)
}

func (s *Server) validatePage(title, body, comment string) string {
	switch {
	case title == "":
		return "The title is necessary."
	case utf8.RuneCountInString(title) > maxTitleLen:
		return "The title is too long."
	case !utf8.ValidString(title) || !utf8.ValidString(body) || !utf8.ValidString(comment):
		return "The text contains characters that are not valid."
	case int64(len(body)) > s.opts.MaxPageBytes:
		return "The page text is too long."
	case utf8.RuneCountInString(comment) > maxCommentLen:
		return "The comment is too long."
	}
	return ""
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "new", "New page", "")
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.PostFormValue("title"))
	slug := markdown.Slugify(title)
	if slug == "" || utf8.RuneCountInString(title) > maxTitleLen {
		s.render(w, r, http.StatusUnprocessableEntity, "new", "New page",
			"The title must contain at least one letter (a-z) or digit, and have 200 characters or less.")
		return
	}
	http.Redirect(w, r, "/wiki/"+slug+"/edit?title="+url.QueryEscape(title), http.StatusSeeOther)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	p, err := s.store.Page(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	revs, err := s.store.Revisions(r.Context(), slug)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "history", "History of "+p.Title, struct {
		Page      *store.Page
		Revisions []store.Revision
	}{p, revs})
}

func (s *Server) handleRevision(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	version, ok := parseVersion(r.PathValue("version"))
	if slug == "" || !ok || version < 1 {
		s.notFound(w, r)
		return
	}
	rev, err := s.store.Revision(r.Context(), slug, version)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	html, err := s.md.Render(rev.Body)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "revision", rev.Title+" (version "+strconv.Itoa(version)+")", struct {
		Slug     string
		Revision *store.Revision
		HTML     template.HTML
	}{slug, rev, html})
}

func (s *Server) handleDeleteForm(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	p, err := s.store.Page(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "delete", "Delete "+p.Title, p)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	slug := pageSlug(r)
	if slug == "" {
		s.notFound(w, r)
		return
	}
	err := s.store.DeletePage(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("page deleted", "slug", slug, "user_id", sessionFrom(r.Context()).User.ID)
	http.Redirect(w, r, "/wiki/", http.StatusSeeOther)
}
