package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/testdb"
)

func TestSafeReturnTo(t *testing.T) {
	tests := map[string]string{
		"":                      "/",
		"/wiki/home":            "/wiki/home",
		"/search?q=a+b":         "/search?q=a+b",
		"//evil.example":        "/",
		"/\\evil.example":       "/",
		"https://evil.example/": "/",
		"javascript:alert(1)":   "/",
		"/ok\r\nLocation: x":    "/",
		"wiki/home":             "/",
		"/a\\b":                 "/",
		"/%2F%2Fevil.example":   "/%2F%2Fevil.example",
	}
	for in, want := range tests {
		if got := safeReturnTo(in); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeIdP is an Authenticator for tests. The authorization code is the
// user name. groups maps a user name to the OIDC groups of the user.
type fakeIdP struct {
	groups map[string][]string
}

func (f *fakeIdP) AuthCodeURL(_ context.Context, state, nonce, verifier string) (string, error) {
	return "https://idp.example/auth?state=" + url.QueryEscape(state), nil
}

func (f *fakeIdP) Exchange(_ context.Context, code, verifier, nonce string) (store.Identity, error) {
	return store.Identity{
		Issuer: "https://idp.example", Subject: "sub-" + code, Username: code,
		Groups: f.groups[code],
	}, nil
}

func (f *fakeIdP) LogoutURL(_ context.Context, redirect string) string {
	return "https://idp.example/logout?post_logout_redirect_uri=" + url.QueryEscape(redirect)
}

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := store.New(testdb.New(t))
	ctx := context.Background()
	for group, role := range map[string]string{"admins": "admin", "editors": "editor", "maintainers": "maintainer"} {
		id, err := st.EnsureOIDCGroup(ctx, group)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.BindRoleToGroup(ctx, role, id); err != nil {
			t.Fatal(err)
		}
	}
	idp := &fakeIdP{groups: map[string][]string{
		"alice": {"admins"},
		"bob":   {"editors"},
		"mary":  {"maintainers"},
	}}
	base, _ := url.Parse("http://example.test")
	s, err := New(st, idp, Options{
		BaseURL:     base,
		SessionTTL:  time.Hour,
		DefaultRole: "viewer",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, st: st}
}

// client is a browser with a cookie jar that does not follow redirects.
type client struct {
	e    *env
	http *http.Client
	csrf string
}

func (e *env) anon() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// login signs in as user with the fake identity provider.
func (e *env) login(user string) *client {
	e.t.Helper()
	c := e.anon()
	resp := c.get("/login?return_to=/wiki/")
	wantStatus(e.t, resp, http.StatusSeeOther)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Host != "idp.example" {
		e.t.Fatalf("login redirects to %s", loc)
	}
	state := loc.Query().Get("state")
	resp = c.get("/auth/callback?code=" + user + "&state=" + url.QueryEscape(state))
	wantStatus(e.t, resp, http.StatusSeeOther)
	if got := resp.Header.Get("Location"); got != "/wiki/" {
		e.t.Fatalf("callback redirects to %q", got)
	}
	c.csrf = c.csrfToken()
	return c
}

var csrfRE = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func (c *client) csrfToken() string {
	c.e.t.Helper()
	body := readBody(c.e.t, c.get("/wiki/"))
	m := csrfRE.FindStringSubmatch(body)
	if m == nil {
		c.e.t.Fatalf("no CSRF token in page:\n%s", body)
	}
	return m[1]
}

func (c *client) get(path string) *http.Response {
	c.e.t.Helper()
	resp, err := c.http.Get(c.e.srv.URL + path)
	if err != nil {
		c.e.t.Fatal(err)
	}
	return resp
}

// post sends a form. It adds the CSRF token if the form has none.
func (c *client) post(path string, form url.Values) *http.Response {
	c.e.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if _, ok := form["csrf_token"]; !ok {
		form.Set("csrf_token", c.csrf)
	}
	req, _ := http.NewRequest(http.MethodPost, c.e.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body := readBody(t, resp)
		t.Fatalf("%s %s: status %d, want %d\n%s", resp.Request.Method, resp.Request.URL.Path,
			resp.StatusCode, want, body)
	}
}

func savePage(c *client, slug, title, body string, version int) *http.Response {
	return c.post("/wiki/"+slug+"/edit", url.Values{
		"title": {title}, "body": {body}, "version": {strconv.Itoa(version)}, "action": {"save"},
	})
}

func TestAnonymousIsRedirected(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	for _, path := range []string{"/wiki/", "/wiki/home", "/admin/users", "/search?q=x"} {
		resp := c.get(path)
		wantStatus(t, resp, http.StatusSeeOther)
		loc := resp.Header.Get("Location")
		if !strings.HasPrefix(loc, "/login?return_to=") {
			t.Fatalf("GET %s redirects to %q", path, loc)
		}
	}
	resp := c.post("/wiki/home/edit", url.Values{"title": {"x"}, "body": {"x"}, "version": {"0"}})
	wantStatus(t, resp, http.StatusUnauthorized)
}

func TestPublicRoutes(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	wantStatus(t, c.get("/healthz"), http.StatusOK)
	wantStatus(t, c.get("/readyz"), http.StatusOK)
	wantStatus(t, c.get("/signed-out"), http.StatusOK)
	resp := c.get("/static/style.css")
	wantStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("style sheet content type %q", ct)
	}
	wantStatus(t, c.get("/static/"), http.StatusNotFound)
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp := e.anon().get("/healthz")
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("header %s is missing", h)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "script-src") {
		t.Errorf("CSP permits scripts: %s", csp)
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	resp := c.get("/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")

	// A different browser (no state cookie) cannot use the state.
	wantStatus(t, e.anon().get("/auth/callback?code=alice&state="+url.QueryEscape(state)), http.StatusBadRequest)
	// A wrong state value fails.
	wantStatus(t, c.get("/auth/callback?code=alice&state=wrong"), http.StatusBadRequest)
}

func TestLoginStateIsOneTime(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	resp := c.get("/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")
	cb := "/auth/callback?code=alice&state=" + url.QueryEscape(state)
	wantStatus(t, c.get(cb), http.StatusSeeOther)

	// Put the state cookie back and try the same state again.
	u, _ := url.Parse(e.srv.URL)
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "skriva_login", Value: state, Path: "/"}})
	wantStatus(t, c.get(cb), http.StatusBadRequest)
}

func TestOpenRedirectBlocked(t *testing.T) {
	e := newEnv(t)
	c := e.login("carol")
	resp := c.get("/login?return_to=//evil.example/")
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("redirect to %q", loc)
	}
}

func TestViewerCannotWrite(t *testing.T) {
	e := newEnv(t)
	carol := e.login("carol")
	wantStatus(t, carol.get("/wiki/home"), http.StatusNotFound)
	wantStatus(t, carol.get("/wiki/home/edit"), http.StatusForbidden)
	wantStatus(t, savePage(carol, "home", "Home", "x", 0), http.StatusForbidden)
	wantStatus(t, carol.get("/new"), http.StatusForbidden)
	wantStatus(t, carol.get("/admin/users"), http.StatusForbidden)

	bob := e.login("bob")
	wantStatus(t, savePage(bob, "home", "Home", "text", 0), http.StatusSeeOther)
	wantStatus(t, carol.get("/wiki/home"), http.StatusOK)
	wantStatus(t, savePage(carol, "home", "Home", "changed", 1), http.StatusForbidden)
	wantStatus(t, carol.post("/wiki/home/delete", nil), http.StatusForbidden)
}

func TestEditFlow(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")

	wantStatus(t, savePage(bob, "home", "Home", "# Hello\n\nSee [[Other Page]].", 0), http.StatusSeeOther)
	body := readBody(t, bob.get("/wiki/home"))
	if !strings.Contains(body, `<h1 id="hello">Hello</h1>`) || !strings.Contains(body, `href="/wiki/other-page"`) {
		t.Fatalf("page does not show rendered Markdown:\n%s", body)
	}

	// A second create of the same page is a conflict.
	wantStatus(t, savePage(bob, "home", "Home", "again", 0), http.StatusConflict)

	wantStatus(t, savePage(bob, "home", "Home", "Version 2", 1), http.StatusSeeOther)
	// An edit that starts from version 1 again is a conflict.
	wantStatus(t, savePage(bob, "home", "Home", "Lost", 1), http.StatusConflict)

	// Preview does not save.
	resp := bob.post("/wiki/home/edit", url.Values{
		"title": {"Home"}, "body": {"**preview**"}, "version": {"2"}, "action": {"preview"},
	})
	wantStatus(t, resp, http.StatusOK)
	if b := readBody(t, resp); !strings.Contains(b, "<strong>preview</strong>") {
		t.Fatalf("preview not shown:\n%s", b)
	}
	raw := readBody(t, bob.get("/wiki/home/raw"))
	if raw != "Version 2" {
		t.Fatalf("raw = %q", raw)
	}

	hist := readBody(t, bob.get("/wiki/home/history"))
	if !strings.Contains(hist, "/wiki/home/history/1") || !strings.Contains(hist, "/wiki/home/history/2") {
		t.Fatalf("history does not list the versions:\n%s", hist)
	}
	wantStatus(t, bob.get("/wiki/home/history/1"), http.StatusOK)
	wantStatus(t, bob.get("/wiki/home/history/9"), http.StatusNotFound)

	edit := readBody(t, bob.get("/wiki/home/edit?from=1"))
	if !strings.Contains(edit, "See [[Other Page]].") {
		t.Fatalf("restore form does not contain old text:\n%s", edit)
	}

	search := readBody(t, bob.get("/search?q=version"))
	if !strings.Contains(search, `href="/wiki/home"`) || !strings.Contains(search, "<mark>") {
		t.Fatalf("search does not find the page:\n%s", search)
	}

	// Editors cannot delete.
	wantStatus(t, bob.post("/wiki/home/delete", nil), http.StatusForbidden)
	mary := e.login("mary")
	wantStatus(t, mary.get("/wiki/home/delete"), http.StatusOK)
	wantStatus(t, mary.post("/wiki/home/delete", nil), http.StatusSeeOther)
	wantStatus(t, mary.get("/wiki/home"), http.StatusNotFound)
}

func TestNewPageForm(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	resp := bob.post("/new", url.Values{"title": {"Översikt 2026"}})
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/wiki/oversikt-2026/edit?title=") {
		t.Fatalf("redirect to %q", loc)
	}
	wantStatus(t, bob.post("/new", url.Values{"title": {"!!!"}}), http.StatusUnprocessableEntity)
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	wantStatus(t, savePage(bob, "home", "", "body", 0), http.StatusUnprocessableEntity)
	wantStatus(t, savePage(bob, "home", strings.Repeat("x", 201), "body", 0), http.StatusUnprocessableEntity)
	wantStatus(t, savePage(bob, "Bad_Slug", "x", "body", 0), http.StatusNotFound)
	wantStatus(t, bob.post("/wiki/home/edit", url.Values{"title": {"x"}, "version": {"abc"}}), http.StatusBadRequest)
	// A body larger than the request limit fails.
	resp := savePage(bob, "home", "Home", strings.Repeat("x", 3<<20), 0)
	if resp.StatusCode < 400 {
		t.Fatalf("large body accepted: %d", resp.StatusCode)
	}
}

func TestXSSIsEscaped(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	wantStatus(t, savePage(bob, "xss", `<script>alert("t")</script>`,
		"<script>alert(1)</script>\n\n[x](javascript:alert(2))", 0), http.StatusSeeOther)
	body := readBody(t, bob.get("/wiki/xss"))
	if strings.Contains(body, "<script") || strings.Contains(body, "javascript:") {
		t.Fatalf("page contains script:\n%s", body)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	form := url.Values{"title": {"Home"}, "body": {"x"}, "version": {"0"}, "csrf_token": {"wrong"}}
	wantStatus(t, bob.post("/wiki/home/edit", form), http.StatusForbidden)
	form.Del("csrf_token")
	form.Set("csrf_token", "")
	wantStatus(t, bob.post("/wiki/home/edit", form), http.StatusForbidden)

	// A cross-site request fails also with a correct token.
	form.Set("csrf_token", bob.csrf)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/wiki/home/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := bob.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, resp, http.StatusForbidden)
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	resp := bob.post("/logout", nil)
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "https://idp.example/logout") {
		t.Fatalf("logout redirects to %q", loc)
	}
	wantStatus(t, bob.get("/wiki/"), http.StatusSeeOther)
}

func TestAdmin(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	carol := e.login("carol")
	ctx := context.Background()

	for _, p := range []string{"/admin/users", "/admin/groups", "/admin/roles", "/admin/bindings"} {
		wantStatus(t, alice.get(p), http.StatusOK)
	}

	users, err := e.st.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var carolID, aliceID int64
	for _, u := range users {
		switch u.Username {
		case "carol":
			carolID = u.ID
		case "alice":
			aliceID = u.ID
		}
	}
	carolPath := "/admin/users/" + fmtID(carolID)
	wantStatus(t, alice.get(carolPath), http.StatusOK)

	// Give carol the editor role. Then carol can create pages.
	wantStatus(t, alice.post("/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {"user:" + fmtID(carolID)}, "return_to": {carolPath},
	}), http.StatusSeeOther)
	wantStatus(t, savePage(carol, "carol", "Carol", "x", 0), http.StatusSeeOther)

	// A custom role with an unknown permission fails.
	wantStatus(t, alice.post("/admin/roles", url.Values{
		"name": {"bad"}, "permission": {"page.everything"},
	}), http.StatusBadRequest)
	wantStatus(t, alice.post("/admin/roles", url.Values{
		"name": {"deleter"}, "permission": {"page.read", "page.delete"},
	}), http.StatusSeeOther)

	// A local group with carol as member.
	resp := alice.post("/admin/groups", url.Values{"name": {"Team"}})
	wantStatus(t, resp, http.StatusSeeOther)
	groupPath := resp.Header.Get("Location")
	wantStatus(t, alice.post(groupPath+"/members", url.Values{"user_id": {fmtID(carolID)}}), http.StatusSeeOther)
	if b := readBody(t, alice.get(groupPath)); !strings.Contains(b, "carol") {
		t.Fatalf("group page does not show member:\n%s", b)
	}

	// Admins cannot disable themselves.
	wantStatus(t, alice.post("/admin/users/"+fmtID(aliceID)+"/disable", nil), http.StatusBadRequest)

	// Disabling carol ends the session of carol at once.
	wantStatus(t, alice.post(carolPath+"/disable", nil), http.StatusSeeOther)
	wantStatus(t, carol.get("/wiki/"), http.StatusSeeOther)
}

func fmtID(id int64) string { return strconv.FormatInt(id, 10) }
