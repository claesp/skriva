// Package web contains the HTTP handlers, the middleware and the HTML
// templates of the application.
package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/clpe/skriva/internal/markdown"
	"github.com/clpe/skriva/internal/store"
)

// Authenticator is the interface to the identity provider.
type Authenticator interface {
	AuthCodeURL(ctx context.Context, state, nonce, verifier string) (string, error)
	Exchange(ctx context.Context, code, verifier, nonce string) (store.Identity, error)
	LogoutURL(ctx context.Context, postLogoutRedirect string) string
}

// Options contains the settings for the Server.
type Options struct {
	BaseURL       *url.URL
	SecureCookies bool
	SessionTTL    time.Duration
	DefaultRole   string
	MaxPageBytes  int64
	// RequestTimeout is the maximum time for one request. Zero gives
	// a default value.
	RequestTimeout time.Duration
}

// Server is the HTTP handler for the application.
type Server struct {
	store *store.Store
	auth  Authenticator
	opts  Options
	log   *slog.Logger
	md    *markdown.Renderer
	tmpl  *templates

	sessionCookie string
	stateCookie   string
	handler       http.Handler
}

// New makes a Server.
func New(st *store.Store, auth Authenticator, opts Options, log *slog.Logger) (*Server, error) {
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 30 * time.Second
	}
	if opts.MaxPageBytes == 0 {
		opts.MaxPageBytes = 1 << 20
	}
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:         st,
		auth:          auth,
		opts:          opts,
		log:           log,
		md:            markdown.New("/wiki/"),
		tmpl:          tmpl,
		sessionCookie: "skriva_session",
		stateCookie:   "skriva_login",
	}
	if opts.SecureCookies {
		// The __Host- prefix makes the browser refuse the cookie if it
		// does not have Secure and Path=/, or if it has a Domain.
		s.sessionCookie = "__Host-skriva_session"
		s.stateCookie = "__Host-skriva_login"
	}
	s.handler = s.routes()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Public routes.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /static/", s.staticHandler())
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("GET /signed-out", s.handleSignedOut)

	// Routes for signed-in users. The "" permission means that a
	// session is sufficient.
	mux.Handle("POST /logout", s.require("", s.handleLogout))

	mux.Handle("GET /{$}", http.RedirectHandler("/wiki/home", http.StatusSeeOther))
	mux.Handle("GET /wiki/{$}", s.require(permRead, s.handleIndex))
	mux.Handle("GET /recent", s.require(permRead, s.handleRecent))
	mux.Handle("GET /search", s.require(permRead, s.handleSearch))
	mux.Handle("GET /new", s.require(permCreate, s.handleNewForm))
	mux.Handle("POST /new", s.require(permCreate, s.handleNew))
	mux.Handle("GET /wiki/{slug}", s.require(permRead, s.handleView))
	mux.Handle("GET /wiki/{slug}/raw", s.require(permRead, s.handleRaw))
	mux.Handle("GET /wiki/{slug}/edit", s.require(permRead, s.handleEditForm))
	mux.Handle("POST /wiki/{slug}/edit", s.require(permRead, s.handleEdit))
	mux.Handle("GET /wiki/{slug}/history", s.require(permRead, s.handleHistory))
	mux.Handle("GET /wiki/{slug}/history/{version}", s.require(permRead, s.handleRevision))
	mux.Handle("GET /wiki/{slug}/delete", s.require(permDelete, s.handleDeleteForm))
	mux.Handle("POST /wiki/{slug}/delete", s.require(permDelete, s.handleDelete))

	s.adminRoutes(mux)

	var h http.Handler = mux
	h = http.NewCrossOriginProtection().Handler(h)
	h = s.withSession(h)
	h = s.withLimits(h)
	h = s.withSecurityHeaders(h)
	h = s.withRecover(h)
	h = s.withLogging(h)
	return h
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
	u.Path += path
	return u.String()
}
