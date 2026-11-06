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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type templates struct {
	// pages are the templates by language code, then by name. Each
	// language has its own copy with the template functions of the
	// language (see langFuncs).
	pages map[string]map[string]*template.Template
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
	// Disabled shows the button without a link. The toolbar of a new
	// item uses it: the buttons stay at their places.
	Disabled bool
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

// headArgs is the argument of the "listhead" and "pagehead" templates:
// the title of the page and the buttons at the right side of the title.
type headArgs struct {
	Title string
	Tools []toolArgs
}

// sectionArgs is the argument of the "sectionhead" template: the title
// of a section, its ID (or "") and the buttons at the right side of the
// title.
type sectionArgs struct {
	Title string
	ID    string
	Tools []toolArgs
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

// itemsArgs is the argument of the "itemtable" template. Details has the
// classification and the last author of each node.
type itemsArgs struct {
	Base    string
	Nodes   []store.NodeSummary
	Details map[int64]store.ItemDetail
}

// defaultRoleArgs is the argument of the "defaultroletable" template.
type defaultRoleArgs struct {
	Text    string
	EditURL string
}

var funcs = template.FuncMap{
	"sub":     func(a, b int) int { return a - b },
	"mib":     mebibytes,
	"docpath": docPath,
	"upper":   strings.ToUpper,
	"tree":    func(base string, nodes []*store.TreeNode) treeArgs { return treeArgs{base, nodes} },
	"tool":    func(href, icon, label string) toolArgs { return toolArgs{Href: href, Icon: icon, Label: label} },
	"tab": func(href, icon, label string, current bool) toolArgs {
		return toolArgs{Href: href, Icon: icon, Label: label, Current: current}
	},
	"post": func(action, icon, label, csrf, returnTo string) postArgs {
		return postArgs{action, icon, label, csrf, returnTo}
	},
	"head":    func(title string, tools ...toolArgs) headArgs { return headArgs{title, tools} },
	"section": func(title, id string, tools ...toolArgs) sectionArgs { return sectionArgs{title, id, tools} },
	"crumb": func(parentURL, parentLabel, title, icon string) crumbArgs {
		return crumbArgs{ParentURL: parentURL, ParentLabel: parentLabel, Title: title, Icon: icon}
	},
	"crumb3": func(rootURL, rootLabel, parentURL, parentLabel, title, icon string) crumbArgs {
		return crumbArgs{rootURL, rootLabel, parentURL, parentLabel, title, icon}
	},
	"spacetop": func(v *view, tab string) spaceTopArgs { return spaceTopArgs{v, tab} },
	// ebtn gives the data of a button in the toolbar of the editor.
	"ebtn": func(command, icon, label string) toolArgs {
		return toolArgs{Href: command, Icon: icon, Label: label}
	},
	"defaultrole": func(text, editURL string) defaultRoleArgs { return defaultRoleArgs{text, editURL} },
	// ntool is a button in the toolbar of a node. The button of the
	// current tab is marked and opens closeURL. Thus a second click ends
	// the mode, for example the editor.
	// when disables a button if ok is false. A toolbar shows all its
	// buttons at the same places. A button for an action that the user
	// cannot do is disabled, not hidden.
	"when": func(ok bool, t toolArgs) toolArgs {
		if !ok {
			t.Disabled, t.Current = true, false
		}
		return t
	},
	"menuitem": func(enabled bool, href, icon, label string, current bool) menuArgs {
		return menuArgs{enabled, href, icon, label, current}
	},
	// dtool is a disabled button in a toolbar.
	"dtool": func(icon, label string) toolArgs { return toolArgs{Icon: icon, Label: label, Disabled: true} },
	// langname gives the name of a language in the language itself.
	"langname": i18n.Name,
}

// langFuncs returns the template functions of the language of p:
//   - t gives a text: {{t "Save"}} or {{t "%d of %d" .A .B}}.
//   - tn gives the singular or the plural text for a number:
//     {{tn .N "%d item" "%d items"}}.
//   - thtml gives a text with HTML tags. The arguments are escaped:
//     {{thtml "Type <code>%s</code>." "-word"}}.
//
// The texts are the keys of the catalogs in internal/i18n/locales.
func langFuncs(p *i18n.Printer) template.FuncMap {
	return template.FuncMap{
		"t":      p.T,
		"tn":     p.N,
		"thtml":  p.HTML,
		"date":   func(t time.Time) template.HTML { return timeElement(p, t, now()) },
		"source": func(source string) string { return groupSource(p, source) },
		// permtext gives the description of a permission.
		"permtext": func(perm rbac.Permission) string { return permissionText(p, perm) },
		// editortexts gives the texts of the editor script as JSON.
		"editortexts": func() string { return editorTexts(p) },
		"ntool": func(currentTab, tab, href, closeURL, icon, label string) toolArgs {
			if currentTab == tab {
				return toolArgs{Href: closeURL, Icon: icon, Label: p.T("%s (close)", label), Current: true}
			}
			return toolArgs{Href: href, Icon: icon, Label: label}
		},
	}
}

func loadTemplates() (*templates, error) {
	// Each static file has a version: a part of the hash of the content.
	// The URL contains the version, thus a browser can keep the file.
	versions := map[string]string{}
	for _, name := range []string{"style.css", "editor.js", "icons.svg"} {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		versions[name] = hex.EncodeToString(sum[:8])
	}
	icons, err := iconNames()
	if err != nil {
		return nil, err
	}
	iconsURL := "/static/icons.svg?v=" + versions["icons.svg"]

	all := template.FuncMap{
		// iconhref gives the URL of an icon. An unknown icon is an error,
		// thus the tests find a wrong name.
		"iconhref": func(name string) (string, error) {
			if !icons[name] {
				return "", fmt.Errorf("unknown icon %q", name)
			}
			return iconsURL + "#i_" + name, nil
		},
	}
	for k, f := range funcs {
		all[k] = f
	}
	for k, f := range langFuncs(i18n.For(i18n.Default)) {
		all[k] = f
	}
	// parts.html contains templates that more than one page uses.
	base, err := template.New("layout.html").Funcs(all).ParseFS(templateFS,
		"templates/layout.html", "templates/parts.html")
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	parsed := map[string]*template.Template{}
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
		parsed[strings.TrimSuffix(name, ".html")] = clone
	}
	t := &templates{pages: map[string]map[string]*template.Template{}}
	for _, l := range i18n.UILanguages() {
		lf := langFuncs(i18n.For(l.Code))
		pages := map[string]*template.Template{}
		for name, p := range parsed {
			c, err := p.Clone()
			if err != nil {
				return nil, err
			}
			pages[name] = c.Funcs(lf)
		}
		t.pages[l.Code] = pages
	}

	t.versions = versions
	return t, nil
}

// iconRE finds the names of the icons in static/icons.svg.
var iconRE = regexp.MustCompile(`<symbol id="i_([a-z0-9-]+)"`)

// iconNames returns the names of the icons in static/icons.svg.
func iconNames() (map[string]bool, error) {
	data, err := staticFS.ReadFile("static/icons.svg")
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, m := range iconRE.FindAllSubmatch(data, -1) {
		names[string(m[1])] = true
	}
	return names, nil
}

// menuArgs is the argument of the template "menuitem".
type menuArgs struct {
	Enabled bool
	Href    string
	Icon    string
	Label   string
	Current bool
}

// spaceTopArgs is the argument of the template "spacetop". Tab is the
// current button in the toolbar, or "" on the space page.
type spaceTopArgs struct {
	V   *view
	Tab string
}

// view is the data for all templates.
type view struct {
	// Lang is the language code of the page.
	Lang     string
	Title    string
	Tenant   *store.Tenant
	Space    *store.Space
	Session  *store.Session
	StyleURL string
	// System is true on the system administration pages.
	System bool
	// Menu is "admin" or "system" on the administration pages. The
	// layout then shows the administration menu at the left side.
	Menu string
	// Section is the menu item of the page, for example "groups".
	Section string
	// New is the place for the buttons for new items in the header.
	New *newTarget
	// Refresh is a URL. The page goes to it at once (meta refresh).
	Refresh string
	Data    any
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
		Lang:     s.lang(r),
		Title:    title,
		Tenant:   ri.Tenant,
		Space:    ri.Space,
		Session:  ri.Session,
		StyleURL: s.staticURL("style.css"),
		System:   strings.HasPrefix(r.URL.Path, "/admin/"),
		New:      ri.New,
		Data:     data,
	}
}

// write runs the template name into a buffer first. Thus a template error
// gives a clean error page and not a half page.
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, name string, v *view) {
	t, ok := s.tmpl.pages[v.Lang][name]
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
	s.render(w, r, status, "error", statusText(s.tr(r), status), struct {
		Status  int
		Message string
	}{status, msg})
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
func groupSource(p *i18n.Printer, source string) string {
	switch source {
	case store.GroupOIDC:
		return "OpenID Connect"
	case store.GroupLocal:
		return p.T("Local")
	}
	return source
}

// permissionText gives the description of a permission in the language
// of p. The texts are the same as rbac.Permission.Description.
func permissionText(p *i18n.Printer, perm rbac.Permission) string {
	switch perm {
	case rbac.DocRead:
		return p.T("Read folders, documents, history and search results")
	case rbac.DocCreate:
		return p.T("Create folders and documents")
	case rbac.DocEdit:
		return p.T("Edit and move folders and documents")
	case rbac.DocDelete:
		return p.T("Delete folders and documents")
	case rbac.SpaceManage:
		return p.T("Create, change and delete spaces")
	case rbac.TenantManage:
		return p.T("Manage users, groups, roles and role bindings of the tenant")
	}
	return perm.Description()
}

// statusText gives the title of an error page for an HTTP status code.
func statusText(p *i18n.Printer, status int) string {
	switch status {
	case http.StatusBadRequest:
		return p.T("Bad request")
	case http.StatusUnauthorized:
		return p.T("Not signed in")
	case http.StatusForbidden:
		return p.T("Forbidden")
	case http.StatusNotFound:
		return p.T("Not found")
	case http.StatusMethodNotAllowed:
		return p.T("Method not allowed")
	case http.StatusConflict:
		return p.T("Conflict")
	case http.StatusRequestEntityTooLarge:
		return p.T("Too large")
	case http.StatusUnsupportedMediaType:
		return p.T("Unsupported file type")
	case http.StatusTooManyRequests:
		return p.T("Too many requests")
	case http.StatusServiceUnavailable:
		return p.T("Service unavailable")
	case http.StatusInternalServerError:
		return p.T("Internal server error")
	}
	return http.StatusText(status)
}

// now gives the current time. Tests can replace it.
var now = time.Now

// timeElement gives a time element for t. The text is relative to now,
// for example "24 minutes ago". The title shows the full time in ISO 8601
// format.
func timeElement(p *i18n.Printer, t, now time.Time) template.HTML {
	t = t.UTC()
	s := fmt.Sprintf(`<time datetime="%s" title="%s">%s</time>`,
		t.Format(time.RFC3339), t.Format("2006-01-02T15:04:05-07:00"), template.HTMLEscapeString(relativeTime(p, t, now)))
	return template.HTML(s) // #nosec G203 -- all values come from the time, not from a user.
}

// relativeDays is the maximum number of days that relativeTime shows as
// a relative value. For older times it shows the date.
const relativeDays = 10

// relativeTime gives the time t relative to now, as in "2 seconds ago",
// "24 minutes ago", "3 hours ago", "Yesterday" and "2 days ago". If t is
// more than relativeDays days ago, it gives the date, as in "2026-10-31".
// The calculation of days uses the calendar days in UTC.
func relativeTime(p *i18n.Printer, t, now time.Time) string {
	t, now = t.UTC(), now.UTC()
	d := now.Sub(t)
	switch {
	case d < time.Second:
		return p.T("Just now")
	case d < time.Minute:
		return p.N(int(d/time.Second), "%d second ago", "%d seconds ago")
	case d < time.Hour:
		return p.N(int(d/time.Minute), "%d minute ago", "%d minutes ago")
	case d < 24*time.Hour:
		return p.N(int(d/time.Hour), "%d hour ago", "%d hours ago")
	}
	y, m, dd := t.Date()
	ny, nm, nd := now.Date()
	days := int(time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC).Sub(time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
	switch {
	case days <= 1:
		return p.T("Yesterday")
	case days <= relativeDays:
		return p.N(days, "%d day ago", "%d days ago")
	}
	return t.Format("2006-01-02")
}

// mebibytes shows a size in bytes as MiB with one decimal, for example
// "12.5 MiB". A size that is more than zero is at least "0.1 MiB".
func mebibytes(n int64) string {
	if n > 0 && n < 1<<20/10 {
		n = 1 << 20 / 10
	}
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MiB"
}
