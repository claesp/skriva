package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type templates struct {
	pages map[string]*template.Template
	// versions contains a hash of each static file. It changes the URL
	// when the file changes, thus browsers can keep the file in a cache.
	versions map[string]string
}

// treeArgs is the argument of the recursive "tree" template.
type treeArgs struct {
	Base  string
	Nodes []*store.TreeNode
}

// toolArgs is the argument of the "tool" and "tab" templates: one icon
// link in a toolbar or a menu. Current marks the link to the current page.
type toolArgs struct {
	Href    string
	Icon    string
	Label   string
	Current bool
}

// postArgs is the argument of the "post" template: an icon button that
// sends a form with only a CSRF token and an optional return address.
type postArgs struct {
	Action   string
	Icon     string
	Label    string
	CSRF     string
	ReturnTo string
}

// headArgs is the argument of the "listhead" template. An empty NewURL
// shows no button.
type headArgs struct {
	Title    string
	NewURL   string
	NewLabel string
}

// crumbArgs is the argument of the "formhead" template. RootURL and
// RootLabel are an optional first part of the path. Icon is the icon of
// the form action.
type crumbArgs struct {
	RootURL     string
	RootLabel   string
	ParentURL   string
	ParentLabel string
	Title       string
	Icon        string
}

// itemsArgs is the argument of the "itemtable" template. Nodes is a slice
// of store.NodeSummary or of *store.TreeNode.
type itemsArgs struct {
	Base  string
	Nodes any
}

// defaultRoleArgs is the argument of the "defaultroletable" template.
type defaultRoleArgs struct {
	Text    string
	EditURL string
}

var funcs = template.FuncMap{
	"date":    func(t time.Time) template.HTML { return timeElement(t, now()) },
	"add":     func(a, b int) int { return a + b },
	"sub":     func(a, b int) int { return a - b },
	"docpath": docPath,
	"tree":    func(base string, nodes []*store.TreeNode) treeArgs { return treeArgs{base, nodes} },
	"tool":    func(href, icon, label string) toolArgs { return toolArgs{Href: href, Icon: icon, Label: label} },
	"tab": func(href, icon, label string, current bool) toolArgs {
		return toolArgs{Href: href, Icon: icon, Label: label, Current: current}
	},
	"post": func(action, icon, label, csrf, returnTo string) postArgs {
		return postArgs{action, icon, label, csrf, returnTo}
	},
	"head": func(title, newURL, newLabel string) headArgs { return headArgs{title, newURL, newLabel} },
	"crumb": func(parentURL, parentLabel, title, icon string) crumbArgs {
		return crumbArgs{ParentURL: parentURL, ParentLabel: parentLabel, Title: title, Icon: icon}
	},
	"crumb3": func(rootURL, rootLabel, parentURL, parentLabel, title, icon string) crumbArgs {
		return crumbArgs{rootURL, rootLabel, parentURL, parentLabel, title, icon}
	},
	"items":    func(base string, nodes any) itemsArgs { return itemsArgs{base, nodes} },
	"spacetop": func(v *view, tab string) spaceTopArgs { return spaceTopArgs{v, tab} },
	"source":   groupSource,
	// ebtn gives the data of a button in the toolbar of the editor.
	"ebtn": func(command, icon, label string) toolArgs {
		return toolArgs{Href: command, Icon: icon, Label: label}
	},
	"defaultrole": func(text, editURL string) defaultRoleArgs { return defaultRoleArgs{text, editURL} },
	// ntool is a button in the toolbar of a node. The button of the
	// current tab is marked and opens closeURL. Thus a second click ends
	// the mode, for example the editor.
	"ntool": func(currentTab, tab, href, closeURL, icon, label string) toolArgs {
		if currentTab == tab {
			return toolArgs{Href: closeURL, Icon: icon, Label: label + " (close)", Current: true}
		}
		return toolArgs{Href: href, Icon: icon, Label: label}
	},
}

func loadTemplates() (*templates, error) {
	// parts.html contains templates that more than one page uses.
	base, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS,
		"templates/layout.html", "templates/parts.html")
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	t := &templates{pages: map[string]*template.Template{}}
	for _, f := range files {
		name := path.Base(f)
		if name == "layout.html" || name == "parts.html" {
			continue
		}
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(templateFS, f); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		t.pages[strings.TrimSuffix(name, ".html")] = clone
	}

	// Each static file has a version: a part of the hash of the content.
	// The URL contains the version, thus a browser can keep the file.
	t.versions = map[string]string{}
	for _, name := range []string{"style.css", "editor.js"} {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		t.versions[name] = hex.EncodeToString(sum[:8])
	}
	return t, nil
}

// spaceTopArgs is the argument of the template "spacetop". Tab is the
// current button in the toolbar, or "" on the space page.
type spaceTopArgs struct {
	V   *view
	Tab string
}

// view is the data for all templates.
type view struct {
	Title    string
	Tenant   *store.Tenant
	Space    *store.Space
	Session  *store.Session
	StyleURL string
	Fonts    []string
	// System is true on the system administration pages.
	System bool
	// Menu is "admin" or "system" on the administration pages. The
	// layout then shows the administration menu at the left side.
	Menu string
	// Section is the menu item of the page, for example "groups".
	Section string
	// New is the place for the buttons for new items in the header.
	New  *newTarget
	Data any
}

// TenantPath returns the URL path of the tenant, for example "/dflt".
func (v *view) TenantPath() string {
	if v.Tenant == nil {
		return ""
	}
	return tenantPath(v.Tenant)
}

// SpacePath returns the URL path of the space, for example
// "/dflt/default0".
func (v *view) SpacePath() string {
	if v.Tenant == nil || v.Space == nil {
		return ""
	}
	return spacePath(v.Tenant, v.Space)
}

// Can reports whether the user has the permission p in the tenant.
func (v *view) Can(p string) bool {
	return v.Session != nil && v.Session.Permissions.Has(rbac.Permission(p))
}

// CSRF returns the CSRF token for forms.
func (v *view) CSRF() string {
	if v.Session == nil {
		return ""
	}
	return v.Session.CSRFToken
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// render runs the template name and writes the result.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name, title string, data any) {
	s.write(w, r, status, name, s.newView(r, title, data))
}

// renderAdmin runs the template name of an administration page. section
// is the menu item that the menu marks as current.
func (s *Server) renderAdmin(w http.ResponseWriter, r *http.Request, status int, section, name, title string, data any) {
	v := s.newView(r, title, data)
	v.Menu = "admin"
	if v.System {
		v.Menu = "system"
	}
	v.Section = section
	s.write(w, r, status, name, v)
}

func (s *Server) newView(r *http.Request, title string, data any) *view {
	ri := reqInfoFrom(r.Context())
	return &view{
		Title:    title,
		Tenant:   ri.Tenant,
		Space:    ri.Space,
		Session:  ri.Session,
		StyleURL: s.staticURL("style.css"),
		Fonts:    preloadFonts,
		System:   strings.HasPrefix(r.URL.Path, "/admin/"),
		New:      ri.New,
		Data:     data,
	}
}

// write runs the template name into a buffer first. Thus a template error
// gives a clean error page and not a half page.
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, name string, v *view) {
	t, ok := s.tmpl.pages[name]
	if !ok {
		s.log.Error("no template", "template", name)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)
	if err := t.ExecuteTemplate(buf, "layout.html", v); err != nil {
		s.log.Error("render template", "template", name, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.render(w, r, status, "error", http.StatusText(status), struct {
		Status  int
		Message string
	}{status, msg})
}

// preloadFonts are the fonts that almost all pages use: the text of the
// interface and the text of a document. The browser gets them before it
// reads the style sheet. The URLs must be the same as in style.css.
var preloadFonts = []string{
	"/static/fonts/inter-5.3.0/inter-latin-wght-normal.woff2",
	"/static/fonts/source-serif-4-5.3.1/source-serif-4-latin-opsz-normal.woff2",
}

// staticURL returns the URL of a static file with its version.
func (s *Server) staticURL(name string) string {
	return "/static/" + name + "?v=" + s.tmpl.versions[name]
}

func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			// Do not show directory lists.
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		// The folder of a font contains the version in its name. Thus
		// the files in it do not change.
		if v := s.tmpl.versions[name]; (v != "" && r.URL.Query().Get("v") == v) || strings.HasPrefix(name, "fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		files.ServeHTTP(w, r)
	})
}

// groupSource gives the text for the source of a group.
func groupSource(source string) string {
	switch source {
	case store.GroupOIDC:
		return "OpenID Connect"
	case store.GroupLocal:
		return "Local"
	}
	return source
}

// now gives the current time. Tests can replace it.
var now = time.Now

// timeElement gives a time element for t. The text is relative to now,
// for example "24 minutes ago". The title shows the full time in ISO 8601
// format.
func timeElement(t, now time.Time) template.HTML {
	t = t.UTC()
	s := fmt.Sprintf(`<time datetime="%s" title="%s">%s</time>`,
		t.Format(time.RFC3339), t.Format("2006-01-02T15:04:05-07:00"), relativeTime(t, now))
	return template.HTML(s) // #nosec G203 -- all values come from the time, not from a user.
}

// relativeDays is the maximum number of days that relativeTime shows as
// a relative value. For older times it shows the date.
const relativeDays = 10

// relativeTime gives the time t relative to now, as in "2 seconds ago",
// "24 minutes ago", "3 hours ago", "Yesterday" and "2 days ago". If t is
// more than relativeDays days ago, it gives the date, as in "2026-10-31".
// The calculation of days uses the calendar days in UTC.
func relativeTime(t, now time.Time) string {
	t, now = t.UTC(), now.UTC()
	d := now.Sub(t)
	switch {
	case d < time.Second:
		return "Just now"
	case d < time.Minute:
		return plural(int(d/time.Second), "second") + " ago"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	}
	y, m, dd := t.Date()
	ny, nm, nd := now.Date()
	days := int(time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC).Sub(time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
	switch {
	case days <= 1:
		return "Yesterday"
	case days <= relativeDays:
		return plural(days, "day") + " ago"
	}
	return t.Format("2006-01-02")
}

// plural gives n and the word, with an "s" if n is not 1.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
