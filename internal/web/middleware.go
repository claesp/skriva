package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
)

// Short names for the permissions in the route table.
const (
	permRead   = rbac.PageRead
	permCreate = rbac.PageCreate
	permDelete = rbac.PageDelete
	permAdmin  = rbac.AdminManage
)

// maxFormBytes is the largest permitted request body.
const maxFormBytes = 2 << 20

type ctxKey int

const sessionKey ctxKey = 0

// sessionFrom returns the session of the request, or nil.
func sessionFrom(ctx context.Context) *store.Session {
	s, _ := ctx.Value(sessionKey).(*store.Session)
	return s
}

// statusWriter records the status code for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if sess := sessionFrom(r.Context()); sess != nil {
			attrs = append(attrs, "user_id", sess.User.ID)
		}
		s.log.Info("request", attrs...)
	})
}

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic in handler", "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy permits no scripts. The application does not use
// JavaScript.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'; img-src 'self' https:; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		// Pages can contain private data. The static handler changes
		// this value for public files.
		h.Set("Cache-Control", "no-store")
		if s.opts.SecureCookies {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		ctx, cancel := context.WithTimeout(r.Context(), s.opts.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withSession adds the session to the request context if the request has
// a valid session cookie.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := s.store.Session(r.Context(), c.Value, s.opts.DefaultRole)
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.clearCookie(w, s.sessionCookie)
		case err != nil:
			s.serverError(w, r, err)
			return
		default:
			r = r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
		}
		next.ServeHTTP(w, r)
	})
}

// require makes sure that the request has a session with permission perm.
// An empty perm only requires a session. For requests that change data,
// it also checks the CSRF token.
func (s *Server) require(perm rbac.Permission, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess == nil {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			s.renderError(w, r, http.StatusUnauthorized, "You must sign in.")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			token := r.PostFormValue("csrf_token")
			if subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
				s.log.Warn("bad CSRF token", "user_id", sess.User.ID, "path", r.URL.Path)
				s.renderError(w, r, http.StatusForbidden, "The form is not valid. Go back, reload the page and try again.")
				return
			}
		}
		if perm != "" && !sess.Permissions.Has(perm) {
			s.forbidden(w, r)
			return
		}
		h(w, r)
	})
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   s.opts.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.opts.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		s.log.Warn("request ended early", "path", r.URL.Path, "error", err)
	} else {
		s.log.Error("internal error", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	s.renderError(w, r, http.StatusInternalServerError, "An internal error occurred. Try again later.")
}

func (s *Server) forbidden(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusForbidden, "You do not have permission for this action.")
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusNotFound, "The page does not exist.")
}

func (s *Server) badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	s.renderError(w, r, http.StatusBadRequest, msg)
}
