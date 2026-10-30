package web

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/clpe/skriva/internal/store"
)

// loginTTL is the maximum time between the start of a login and the
// callback from the identity provider.
const loginTTL = 10 * time.Minute

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	returnTo := safeReturnTo(r.URL.Query().Get("return_to"))
	if sessionFrom(r.Context()) != nil {
		http.Redirect(w, r, returnTo, http.StatusSeeOther)
		return
	}

	state := rand.Text()
	st := store.LoginState{
		Nonce:        rand.Text(),
		CodeVerifier: oauth2.GenerateVerifier(),
		ReturnTo:     returnTo,
	}
	authURL, err := s.auth.AuthCodeURL(r.Context(), state, st.Nonce, st.CodeVerifier)
	if err != nil {
		s.log.Error("start login", "error", err)
		s.renderError(w, r, http.StatusServiceUnavailable,
			"The identity provider is not available. Try again later.")
		return
	}
	if err := s.store.SaveLoginState(r.Context(), state, st, loginTTL); err != nil {
		s.serverError(w, r, err)
		return
	}
	// The cookie binds the login to this browser. The callback accepts
	// the state value only from the browser that started the login.
	s.setCookie(w, s.stateCookie, state, loginTTL)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.log.Warn("identity provider returned an error", "error", e, "description", q.Get("error_description"))
		s.renderError(w, r, http.StatusUnauthorized, "The sign-in was not successful.")
		return
	}

	state := q.Get("state")
	c, err := r.Cookie(s.stateCookie)
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		s.renderError(w, r, http.StatusBadRequest, "The sign-in request is not valid or is too old. Try again.")
		return
	}
	s.clearCookie(w, s.stateCookie)

	st, err := s.store.TakeLoginState(r.Context(), state)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusBadRequest, "The sign-in request is not valid or is too old. Try again.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	id, err := s.auth.Exchange(r.Context(), q.Get("code"), st.CodeVerifier, st.Nonce)
	if err != nil {
		s.log.Warn("token exchange failed", "error", err)
		s.renderError(w, r, http.StatusUnauthorized, "The sign-in was not successful.")
		return
	}
	user, err := s.store.SyncLogin(r.Context(), id)
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
	if old, err := r.Cookie(s.sessionCookie); err == nil {
		_, _ = s.store.DeleteSession(r.Context(), old.Value)
	}
	token, err := s.store.CreateSession(r.Context(), user.ID, id.IDToken, s.opts.SessionTTL)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setCookie(w, s.sessionCookie, token, s.opts.SessionTTL)
	s.log.Info("user signed in", "user_id", user.ID, "username", user.Username)
	http.Redirect(w, r, st.ReturnTo, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie(s.sessionCookie); err == nil {
		if idToken, err = s.store.DeleteSession(r.Context(), c.Value); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.clearCookie(w, s.sessionCookie)
	target := s.auth.LogoutURL(r.Context(), idToken, s.absURL("/signed-out"))
	if target == "" {
		target = "/signed-out"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleSignedOut(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "signed-out", "Signed out", nil)
}

// safeReturnTo returns p if it is a path on this site. Otherwise it
// returns "/". This prevents open redirects.
func safeReturnTo(p string) string {
	if p == "" || p[0] != '/' || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return p
}
