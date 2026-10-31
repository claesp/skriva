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
	// styleVersion is a hash of the style sheet. It changes the URL when
	// the file changes, thus browsers can keep the file in a cache.
	styleVersion string
}

// treeArgs is the argument of the recursive "tree" template.
type treeArgs struct {
	Base  string
	Nodes []*store.TreeNode
}

// toolArgs is the argument of the "tool" template: one icon link in the
// toolbar.
type toolArgs struct {
	Href  string
	Icon  string
	Label string
}

var funcs = template.FuncMap{
	"date":    func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") },
	"add":     func(a, b int) int { return a + b },
	"sub":     func(a, b int) int { return a - b },
	"docpath": docPath,
	"tree":    func(base string, nodes []*store.TreeNode) treeArgs { return treeArgs{base, nodes} },
	"tool":    func(href, icon, label string) toolArgs { return toolArgs{href, icon, label} },
}

func loadTemplates() (*templates, error) {
	base, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html")
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
		if name == "layout.html" {
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

	css, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(css)
	t.styleVersion = hex.EncodeToString(sum[:8])
	return t, nil
}

// view is the data for all templates.
type view struct {
	Title    string
	Tenant   *store.Tenant
	Space    *store.Space
	Session  *store.Session
	StyleURL string
	// System is true on the system administration pages.
	System bool
	Data   any
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

// render runs the template name and writes the result. It runs the
// template into a buffer first. Thus a template error gives a clean
// error page and not a half page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name, title string, data any) {
	t, ok := s.tmpl.pages[name]
	if !ok {
		s.log.Error("no template", "template", name)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	ri := reqInfoFrom(r.Context())
	v := &view{
		Title:    title,
		Tenant:   ri.Tenant,
		Space:    ri.Space,
		Session:  ri.Session,
		StyleURL: "/static/style.css?v=" + s.tmpl.styleVersion,
		System:   strings.HasPrefix(r.URL.Path, "/admin/"),
		Data:     data,
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
		if r.URL.Query().Get("v") == s.tmpl.styleVersion {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		files.ServeHTTP(w, r)
	})
}
