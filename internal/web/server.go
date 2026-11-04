// Package web contains the HTTP handlers, the middleware and the HTML
// templates of the application.
//
// The URL structure is:
//
//	/admin/...                      system administration
//	/{tenant}/...                   tenant pages, login and tenant administration
//	/{tenant}/{space}/...           folders and documents of a space
//	/{tenant}/{space}/d/{id}/{slug} one document or folder
//
// A tenant code and a space slug have 2 to 16 characters from a-z, 0-9
// and "-". The first segment of a tenant-level route (for example
// "admin" or "login") cannot be a space slug, and the first segment of a
// system route (for example "admin" or "static") cannot be a tenant code.
// The server records these names when it registers the routes
// (namesMux). The dispatcher sends a request to the tenant router if
// the second segment is the name of a tenant-level route, and to the
// space router if not.
package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/claesp/skriva/internal/diagram"
	"github.com/claesp/skriva/internal/export"
	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

// routeMux is the part of http.ServeMux that the route functions use.
type routeMux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// namesMux is an http.ServeMux that records the names of the routes: the
// first fixed segment after prefix. For the prefix "/{tenant}", the
// pattern "GET /{tenant}/login" gives the name "login".
type namesMux struct {
	*http.ServeMux
	prefix string
	names  map[string]bool
}

func newNamesMux(mux *http.ServeMux, prefix string, names map[string]bool) *namesMux {
	return &namesMux{ServeMux: mux, prefix: prefix + "/", names: names}
}

func (m *namesMux) record(pattern string) {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = pattern[i+1:]
	}
	rest, ok := strings.CutPrefix(pattern, m.prefix)
	if !ok {
		return
	}
	name, _, _ := strings.Cut(rest, "/")
	if name != "" && !strings.HasPrefix(name, "{") {
		m.names[name] = true
	}
}

func (m *namesMux) Handle(pattern string, h http.Handler) {
	m.record(pattern)
	m.ServeMux.Handle(pattern, h)
}

func (m *namesMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.record(pattern)
	m.ServeMux.HandleFunc(pattern, h)
}

// validTenantCode reports whether code can be the code of a tenant.
func (s *Server) validTenantCode(code string) bool {
	return store.ValidCode(code) && !s.systemNames[code]
}

// validSpaceSlug reports whether slug can be the slug of a space.
func (s *Server) validSpaceSlug(slug string) bool {
	return store.ValidCode(slug) && !s.tenantNames[slug]
}

// Texts for an invalid code.
const (
	tenantCodeError = "The code must have 2 to 16 characters from a-z, 0-9 and \"-\". " +
		"It cannot start or end with \"-\". Some words (for example \"admin\" and \"static\") are not permitted."
	spaceCodeError = "The code must have 2 to 16 characters from a-z, 0-9 and \"-\". " +
		"It cannot start or end with \"-\". Some words (for example \"admin\" and \"login\") are not permitted."
)

// Authenticator is the interface to an identity provider.
type Authenticator interface {
	AuthCodeURL(ctx context.Context, state, nonce, verifier string) (string, error)
	Exchange(ctx context.Context, code, verifier, nonce string) (store.Identity, error)
	LogoutURL(ctx context.Context, idTokenHint, postLogoutRedirect string) string
}

// TenantAuthFunc returns the Authenticator for a tenant.
type TenantAuthFunc func(ctx context.Context, t *store.Tenant) (Authenticator, error)

// Options contains the settings for the Server.
type Options struct {
	BaseURL       *url.URL
	SecureCookies bool
	// SystemAuth is the identity provider for system administrators.
	SystemAuth Authenticator
	// SystemAdminGroup is the OIDC group of the system administrators.
	SystemAdminGroup string
	// SystemSessionTTL is the maximum life of a system session.
	SystemSessionTTL time.Duration
	// TenantAuth returns the identity provider of a tenant.
	TenantAuth TenantAuthFunc
	// RequestTimeout is the maximum time for one request. Zero gives
	// a default value.
	RequestTimeout time.Duration
	// LoginRate is the maximum number of login starts per second, for
	// all clients together. Zero gives a default value.
	LoginRate float64
	// Formats are the export formats. Nil gives export.NewFormats(nil):
	// all formats without PDF.
	Formats export.Formats
	// Diagrams makes the images of Mermaid diagrams. If it is nil, a
	// diagram is a code block.
	Diagrams *diagram.Renderer
}

// Server is the HTTP handler for the application.
type Server struct {
	store *store.Store
	opts  Options
	log   *slog.Logger
	tmpl  *templates

	loginLimit *rate.Limiter
	handler    http.Handler

	systemMux *http.ServeMux
	tenantMux *http.ServeMux
	spaceMux  *http.ServeMux

	// systemNames and tenantNames are the names of the routes at the top
	// level and at the tenant level. See namesMux.
	systemNames map[string]bool
	tenantNames map[string]bool
}

// New makes a Server.
func New(st *store.Store, opts Options, log *slog.Logger) (*Server, error) {
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 30 * time.Second
	}
	if opts.LoginRate == 0 {
		opts.LoginRate = 20
	}
	if opts.SystemSessionTTL == 0 {
		opts.SystemSessionTTL = 4 * time.Hour
	}
	if opts.Formats == nil {
		opts.Formats = export.NewFormats(nil)
	}
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:      st,
		opts:       opts,
		log:        log,
		tmpl:       tmpl,
		loginLimit: rate.NewLimiter(rate.Limit(opts.LoginRate), int(opts.LoginRate*5)),
		systemMux:  http.NewServeMux(),
		tenantMux:  http.NewServeMux(),
		spaceMux:   http.NewServeMux(),

		systemNames: map[string]bool{},
		tenantNames: map[string]bool{},
	}
	s.systemRoutes(s.systemMux)
	tenant := newNamesMux(s.tenantMux, "/{tenant}", s.tenantNames)
	s.tenantRoutes(tenant)
	s.adminRoutes(tenant)
	s.spaceRoutes(s.spaceMux)
	s.handler = s.routes()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := newNamesMux(http.NewServeMux(), "", s.systemNames)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /static/", s.staticHandler())
	mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusSeeOther))
	mux.Handle("/admin/", s.withSystemSession(s.systemMux))
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("/", s.dispatchTenant)

	var h http.Handler = mux
	h = http.NewCrossOriginProtection().Handler(h)
	h = s.withLimits(h)
	h = s.withSecurityHeaders(h)
	h = s.withRecover(h)
	h = s.withLogging(h)
	return h
}

// handleRoot sends the browser to the default tenant.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.DefaultTenant(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, tenantPath(t)+"/", http.StatusSeeOther)
}

// dispatchTenant loads the tenant, the session and the space of the
// request. Then it sends the request to the tenant router or to the space
// router.
func (s *Server) dispatchTenant(w http.ResponseWriter, r *http.Request) {
	segs := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
	code := segs[0]
	if !s.validTenantCode(code) {
		s.notFound(w, r)
		return
	}
	if len(segs) == 1 {
		http.Redirect(w, r, "/"+code+"/", http.StatusMovedPermanently) // #nosec G710 -- code is a valid tenant code.
		return
	}

	ri := reqInfoFrom(r.Context())
	t, err := s.store.TenantByCode(r.Context(), code)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if t.Disabled {
		s.renderError(w, r, http.StatusForbidden, "This tenant is disabled.")
		return
	}
	ri.Tenant = t

	if c, err := r.Cookie(s.tenantCookie(t)); err == nil && c.Value != "" {
		sess, err := s.store.Session(r.Context(), c.Value, &t.ID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.clearCookie(w, s.tenantCookie(t))
		case err != nil:
			s.serverError(w, r, err)
			return
		default:
			ri.Session = sess
		}
	}

	if s.validSpaceSlug(segs[1]) {
		sp, err := s.store.Space(r.Context(), t.ID, segs[1])
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		ri.Space = sp
		if ri.Session != nil {
			// In a space, the permissions of the space apply. The node
			// handlers change them to the permissions of the node.
			acc, err := s.store.Access(r.Context(), t.ID, sp.ID, ri.Session.User.ID)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			if !acc.Any(rbac.DocRead) {
				// The user can read nothing in the space. The space
				// is "not found", and the page does not show its name.
				ri.Space = nil
				s.notFound(w, r)
				return
			}
			ri.Access = acc
			ri.setPermissions(acc.Root())
		}
		s.spaceMux.ServeHTTP(w, r)
		return
	}
	s.tenantMux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.store.Ping(ctx); err != nil {
		s.log.Warn("readiness check failed", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database is not available\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// absURL returns the absolute URL for path.
func (s *Server) absURL(path string) string {
	u := *s.opts.BaseURL
	u.Path = path
	return u.String()
}

// tenantPath returns the URL path of a tenant, for example "/dflt".
func tenantPath(t *store.Tenant) string { return "/" + t.Code }

// spacePath returns the URL path of a space, for example "/dflt/default0".
func spacePath(t *store.Tenant, sp *store.Space) string { return "/" + t.Code + "/" + sp.Slug }

// docPath returns the URL path of a node in a space.
func docPath(base, publicID, slug string) string {
	if slug == "" {
		return base + "/d/" + publicID
	}
	return base + "/d/" + publicID + "/" + slug
}
