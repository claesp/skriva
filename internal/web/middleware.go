package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/claesp/skriva/internal/rbac"
	"github.com/claesp/skriva/internal/store"
)

// maxFormBytes is the largest permitted request body.
const maxFormBytes = 2 << 20

// reqInfo contains the tenant, the space and the session of a request.
// The middleware fills it in order. A handler reads it.
type reqInfo struct {
	Tenant  *store.Tenant
	Space   *store.Space
	Session *store.Session
	// Access contains the permissions for the nodes of the space. It is
	// nil outside a space and for anonymous requests.
	Access *store.Access
	// New is the place for the items of the "New" drop-down
	// in the page header. It is nil on pages without the drop-down.
	New *newTarget
}

// newTarget is the place where the buttons in the page header make new
// items.
type newTarget struct {
	// Parent is the public ID of the parent node, or "" for the top level
	// of the space.
	Parent string
	// Folder is true if the parent can contain a folder.
	Folder bool
}

// Query returns the query of the URL of the form for a new item of the
// given kind.
func (t *newTarget) Query(kind string) string {
	q := url.Values{"kind": {kind}}
	if t.Parent != "" {
		q.Set("parent", t.Parent)
	}
	return "?" + q.Encode()
}

// setPermissions sets the permissions of the request. It uses a copy of
// the session, because the session can be in use by a different part of
// the code.
func (ri *reqInfo) setPermissions(p rbac.Set) {
	sess := *ri.Session
	sess.Permissions = p
	ri.Session = &sess
}

type ctxKey int

const reqInfoKey ctxKey = 0

// reqInfoFrom returns the request data. It is never nil inside the
// handler chain.
func reqInfoFrom(ctx context.Context) *reqInfo {
	if ri, ok := ctx.Value(reqInfoKey).(*reqInfo); ok {
		return ri
	}
	return &reqInfo{}
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

// withLogging writes one log line for each request. It also puts the
// empty request data in the context.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ri := &reqInfo{}
		r = r.WithContext(context.WithValue(r.Context(), reqInfoKey, ri))
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if ri.Tenant != nil {
			attrs = append(attrs, "tenant", ri.Tenant.Code)
		}
		if ri.Session != nil {
			attrs = append(attrs, "user_id", ri.Session.User.ID)
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

// contentSecurityPolicy permits no scripts. Only the form of a document
// uses JavaScript. See editorSecurityPolicy.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self' https:; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// editorSecurityPolicy applies to the form of a document. It permits the
// script of the editor from the same site, and requests to the same site
// for uploads. It does not permit inline scripts or inline styles.
const editorSecurityPolicy = "default-src 'none'; script-src 'self'; connect-src 'self'; " +
	"style-src 'self'; font-src 'self'; img-src 'self' https:; " +
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
		limit := int64(maxFormBytes)
		if isUpload(r) {
			limit = maxUploadBytes + 64<<10
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		ctx, cancel := context.WithTimeout(r.Context(), s.opts.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withSystemSession adds the system administrator session to the request
// data if the request has a valid system session cookie.
func (s *Server) withSystemSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.systemCookie())
		if err == nil && c.Value != "" {
			sess, err := s.store.Session(r.Context(), c.Value, nil)
			switch {
			case errors.Is(err, store.ErrNotFound):
				s.clearCookie(w, s.systemCookie())
			case err != nil:
				s.serverError(w, r, err)
				return
			default:
				reqInfoFrom(r.Context()).Session = sess
			}
		}
		next.ServeHTTP(w, r)
	})
}

// require makes sure that the request has a tenant session with
// permission perm. An empty perm only requires a session. For requests
// that change data, it also checks the CSRF token.
func (s *Server) require(perm rbac.Permission, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := reqInfoFrom(r.Context())
		if ri.Tenant == nil {
			s.notFound(w, r)
			return
		}
		if ri.Session == nil || ri.Session.System() {
			s.needLogin(w, r, tenantPath(ri.Tenant))
			return
		}
		if !s.checkCSRF(w, r, ri.Session) {
			return
		}
		if perm != "" && !ri.Session.Permissions.Has(perm) {
			s.forbidden(w, r)
			return
		}
		// The store writes the user into the audit log.
		h(w, r.WithContext(store.WithActor(r.Context(), ri.Session.User.ID)))
	})
}

// requireSystem makes sure that the request has a system administrator
// session. For requests that change data, it also checks the CSRF token.
func (s *Server) requireSystem(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := reqInfoFrom(r.Context()).Session
		if sess == nil || !sess.System() {
			s.needLogin(w, r, "/admin")
			return
		}
		if !s.checkCSRF(w, r, sess) {
			return
		}
		h(w, r.WithContext(store.WithActor(r.Context(), sess.User.ID)))
	})
}

func (s *Server) needLogin(w http.ResponseWriter, r *http.Request, base string) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		http.Redirect(w, r, base+"/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	s.renderError(w, r, http.StatusUnauthorized, "You must sign in.")
}

// checkCSRF checks the CSRF token of requests that change data. It writes
// an error and returns false if the token is not correct.
func (s *Server) checkCSRF(w http.ResponseWriter, r *http.Request, sess *store.Session) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	// ParseForm does not read a multipart body. An upload sends one.
	parse := r.ParseForm
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		parse = func() error { return r.ParseMultipartForm(maxUploadBytes + 64<<10) }
	}
	if err := parse(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.renderError(w, r, http.StatusRequestEntityTooLarge, "The form is too large.")
			return false
		}
		s.badRequest(w, r, "The form is not valid.")
		return false
	}
	token := r.PostFormValue("csrf_token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
		s.log.Warn("bad CSRF token", "user_id", sess.User.ID, "path", r.URL.Path)
		s.renderError(w, r, http.StatusForbidden, "The form is not valid. Go back, reload the page and try again.")
		return false
	}
	return true
}

// cookieName adds the __Host- prefix when the cookies are secure. The
// prefix makes the browser refuse the cookie if it does not have Secure
// and Path=/, or if it has a Domain.
func (s *Server) cookieName(name string) string {
	if s.opts.SecureCookies {
		return "__Host-" + name
	}
	return name
}

// tenantCookie is the name of the session cookie for a tenant. Each
// tenant has its own cookie. Thus a browser can have sessions in more
// than one tenant. The name uses the ID, because the code can change.
func (s *Server) tenantCookie(t *store.Tenant) string {
	return s.cookieName("skriva_t" + strconv.FormatInt(t.ID, 10))
}

func (s *Server) systemCookie() string { return s.cookieName("skriva_system") }

// setCookie sets a session cookie. Secure is false only if the base URL
// uses http, for example in local development.
func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the base URL.
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
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the base URL.
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

// storeResult handles the error from a store call that changes data. It
// returns true if the handler can continue.
func (s *Server) storeResult(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, store.ErrClassification):
		s.renderError(w, r, http.StatusConflict, classificationMessage(err))
	case errors.Is(err, store.ErrConflict):
		s.renderError(w, r, http.StatusConflict, "The name is in use already.")
	case errors.Is(err, store.ErrInvalid):
		s.badRequest(w, r, "The request is not permitted for this item.")
	default:
		s.serverError(w, r, err)
	}
	return false
}

// classificationMessage is the text for a *store.ClassificationError.
func classificationMessage(err error) string {
	var ce *store.ClassificationError
	if !errors.As(err, &ce) {
		return "The classification is lower than the lowest permitted level."
	}
	if ce.Documents == 1 {
		return "1 document has a classification lower than " + ce.Min + ". " +
			"Give it the classification " + ce.Min + " or higher first."
	}
	if ce.Documents > 1 {
		return strconv.Itoa(ce.Documents) + " documents have a classification lower than " + ce.Min + ". " +
			"Give them the classification " + ce.Min + " or higher first."
	}
	return "The classification must be " + ce.Min + " or higher here. The space or a parent folder sets this limit."
}
