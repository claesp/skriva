package web

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/clpe/skriva/internal/store"
)

// loginTTL is the maximum time between the start of a login and the
// callback from the identity provider.
const loginTTL = 10 * time.Minute

// authScope contains the data that is different for a tenant login and
// for a system administrator login.
type authScope struct {
	// tenantID is nil for the system administration.
	tenantID      *int64
	base          string
	sessionCookie string
	stateCookie   string
	sessionTTL    time.Duration
	auth          Authenticator
	// allow decides whether the identity can sign in. Nil permits all.
	allow func(store.Identity) bool
}

// scope returns the authScope for the request. It returns false and
// writes an error if the identity provider is not available.
func (s *Server) scope(w http.ResponseWriter, r *http.Request) (*authScope, bool) {
	ri := reqInfoFrom(r.Context())
	if ri.Tenant == nil {
		return &authScope{
			base:          "/admin",
			sessionCookie: s.systemCookie(),
			stateCookie:   s.cookieName("skriva_login_system"),
			sessionTTL:    s.opts.SystemSessionTTL,
			auth:          s.opts.SystemAuth,
			allow: func(id store.Identity) bool {
				return s.opts.SystemAdminGroup != "" && slices.Contains(id.Groups, s.opts.SystemAdminGroup)
			},
		}, true
	}
	t := ri.Tenant
	auth, err := s.opts.TenantAuth(r.Context(), t)
	if err != nil {
		s.log.Warn("no identity provider for tenant", "tenant", t.Code, "error", err)
		s.renderError(w, r, http.StatusServiceUnavailable,
			"Sign-in is not available for this tenant. Ask a system administrator to set the identity provider.")
		return nil, false
	}
	return &authScope{
		tenantID:      &t.ID,
		base:          tenantPath(t),
		sessionCookie: s.tenantCookie(t),
		stateCookie:   s.cookieName("skriva_login_" + t.Code),
		sessionTTL:    t.SessionTTL,
		auth:          auth,
	}, true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	returnTo := safeReturnTo(r.URL.Query().Get("return_to"), sc.base)
	if sess := reqInfoFrom(r.Context()).Session; sess != nil {
		http.Redirect(w, r, returnTo, http.StatusSeeOther) // #nosec G710 -- safeReturnTo checks the value.
		return
	}

	// Each login writes a row to the database before the user signs in.
	// The limit prevents that anonymous clients fill the database.
	if !s.loginLimit.Allow() {
		s.log.Warn("login rate limit reached")
		w.Header().Set("Retry-After", "10")
		s.renderError(w, r, http.StatusTooManyRequests, "There are too many sign-in requests. Try again in a short time.")
		return
	}

	state := rand.Text()
	st := store.LoginState{
		Nonce:        rand.Text(),
		CodeVerifier: oauth2.GenerateVerifier(),
		ReturnTo:     returnTo,
	}
	authURL, err := sc.auth.AuthCodeURL(r.Context(), state, st.Nonce, st.CodeVerifier)
	if err != nil {
		s.log.Error("start login", "error", err)
		s.renderError(w, r, http.StatusServiceUnavailable,
			"The identity provider is not available. Try again later.")
		return
	}
	if err := s.store.SaveLoginState(r.Context(), sc.tenantID, state, st, loginTTL); err != nil {
		s.serverError(w, r, err)
		return
	}
	// The cookie binds the login to this browser. The callback accepts
	// the state value only from the browser that started the login.
	s.setCookie(w, sc.stateCookie, state, loginTTL)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.log.Warn("identity provider returned an error", "error", e, "description", q.Get("error_description"))
		s.renderError(w, r, http.StatusUnauthorized, "The sign-in was not successful.")
		return
	}

	state := q.Get("state")
	c, err := r.Cookie(sc.stateCookie)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		s.renderError(w, r, http.StatusBadRequest, "The sign-in request is not valid or is too old. Try again.")
		return
	}
	s.clearCookie(w, sc.stateCookie)

	st, err := s.store.TakeLoginState(r.Context(), sc.tenantID, state)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusBadRequest, "The sign-in request is not valid or is too old. Try again.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	id, err := sc.auth.Exchange(r.Context(), q.Get("code"), st.CodeVerifier, st.Nonce)
	if err != nil {
		s.log.Warn("token exchange failed", "error", err)
		s.renderError(w, r, http.StatusUnauthorized, "The sign-in was not successful.")
		return
	}
	if sc.allow != nil && !sc.allow(id) {
		s.log.Warn("sign-in refused", "base", sc.base, "username", id.Username)
		s.renderError(w, r, http.StatusForbidden, "Your account does not have access here.")
		return
	}
	user, err := s.store.SyncLogin(r.Context(), sc.tenantID, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if user.Disabled {
		s.log.Warn("disabled user tried to sign in", "user_id", user.ID)
		s.renderError(w, r, http.StatusForbidden, "Your account is disabled.")
		return
	}

	// Remove an old session. A new token after login prevents session
	// fixation.
	if old, err := r.Cookie(sc.sessionCookie); err == nil {
		_, _ = s.store.DeleteSession(r.Context(), old.Value)
	}
	token, err := s.store.CreateSession(r.Context(), sc.tenantID, user.ID, id.IDToken, sc.sessionTTL)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setCookie(w, sc.sessionCookie, token, sc.sessionTTL)
	s.log.Info("user signed in", "base", sc.base, "user_id", user.ID, "username", user.Username)
	// Check the value again. The database is a different trust boundary.
	http.Redirect(w, r, safeReturnTo(st.ReturnTo, sc.base), http.StatusSeeOther) // #nosec G710 -- checked.
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	var idToken string
	if c, err := r.Cookie(sc.sessionCookie); err == nil {
		if idToken, err = s.store.DeleteSession(r.Context(), c.Value); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.clearCookie(w, sc.sessionCookie)
	target := sc.auth.LogoutURL(r.Context(), idToken, s.absURL(sc.base+"/signed-out"))
	if target == "" {
		target = sc.base + "/signed-out"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleSignedOut(w http.ResponseWriter, r *http.Request) {
	base := "/admin"
	if t := reqInfoFrom(r.Context()).Tenant; t != nil {
		base = tenantPath(t)
	}
	s.render(w, r, http.StatusOK, "signed-out", "Signed out", base)
}

// safeReturnTo returns p if it is a path on this site. Otherwise it
// returns base + "/". This prevents open redirects.
func safeReturnTo(p, base string) string {
	def := base + "/"
	if p == "" || p[0] != '/' || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return def
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return def
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return def
	}
	return p
}
