package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
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
		"":                      "/dflt/",
		"/dflt/default0/":       "/dflt/default0/",
		"/dflt/search?q=a+b":    "/dflt/search?q=a+b",
		"//evil.example":        "/dflt/",
		"/\\evil.example":       "/dflt/",
		"https://evil.example/": "/dflt/",
		"javascript:alert(1)":   "/dflt/",
		"/ok\r\nLocation: x":    "/dflt/",
		"wiki/home":             "/dflt/",
		"/a\\b":                 "/dflt/",
	}
	for in, want := range tests {
		if got := safeReturnTo(in, "/dflt"); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidIssuer(t *testing.T) {
	for u, want := range map[string]bool{
		"":                               true,
		"https://sso.example.com/realm":  true,
		"http://localhost:8180/realms/x": true,
		"http://127.0.0.1/x":             true,
		"http://sso.example.com":         false,
		"ftp://sso.example.com":          false,
		"https://user@sso.example.com":   false,
		"https://sso.example.com/?a=b":   false,
		"not a url":                      false,
	} {
		if got := validIssuer(u); got != want {
			t.Errorf("validIssuer(%q) = %v", u, got)
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
		Groups: f.groups[code], IDToken: "token-" + code,
	}, nil
}

func (f *fakeIdP) LogoutURL(_ context.Context, _, redirect string) string {
	return "https://idp.example/logout?post_logout_redirect_uri=" + url.QueryEscape(redirect)
}

type env struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
	dft *store.Tenant
	sp  *store.Space
}

var ctx = context.Background()

func newEnv(t *testing.T) *env {
	t.Helper()
	st := store.New(testdb.New(t), testdb.Box(t))
	dft, err := st.DefaultTenant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ts := dft.TenantSettings
	ts.OIDCIssuer, ts.OIDCClientID = "https://idp.example", "skriva"
	ts.AdminGroup = "admins"
	if dft, err = st.UpdateTenant(ctx, dft.ID, ts, nil); err != nil {
		t.Fatal(err)
	}
	for group, role := range map[string]string{"editors": "editor", "maintainers": "maintainer"} {
		id, err := st.EnsureOIDCGroup(ctx, dft.ID, group)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.BindRoleToGroup(ctx, dft.ID, role, id, store.Scope{}); err != nil {
			t.Fatal(err)
		}
	}
	sp, err := st.Space(ctx, dft.ID, "default0")
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{groups: map[string][]string{
		"alice": {"admins"},
		"bob":   {"editors"},
		"mary":  {"maintainers"},
		"root":  {"sysadmins"},
	}}
	base, _ := url.Parse("http://example.test")
	s, err := New(st, Options{
		BaseURL:          base,
		SystemAuth:       idp,
		SystemAdminGroup: "sysadmins",
		TenantAuth: func(_ context.Context, t *store.Tenant) (Authenticator, error) {
			if !t.OIDCConfigured() {
				return nil, errors.New("not configured")
			}
			return idp, nil
		},
	}, slog.New(slog.NewTextHandler(testLogOutput(), nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, st: st, dft: dft, sp: sp}
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

// login signs in as user at base ("/dflt" or "/admin") with the fake
// identity provider.
func (e *env) loginAt(base, user string) *client {
	e.t.Helper()
	c := e.anon()
	c.loginAt(base, user)
	return c
}

func (c *client) loginAt(base, user string) {
	t := c.e.t
	t.Helper()
	resp := c.get(base + "/login")
	wantStatus(t, resp, http.StatusSeeOther)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Host != "idp.example" {
		t.Fatalf("login redirects to %s", loc)
	}
	resp = c.get(base + "/auth/callback?code=" + user + "&state=" + url.QueryEscape(loc.Query().Get("state")))
	wantStatus(t, resp, http.StatusSeeOther)
	if got := resp.Header.Get("Location"); got != base+"/" {
		t.Fatalf("callback redirects to %q", got)
	}
	c.csrf = c.csrfToken(base + "/")
}

func (e *env) login(user string) *client { return e.loginAt("/dflt", user) }

var csrfRE = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func (c *client) csrfToken(path string) string {
	c.e.t.Helper()
	body := readBody(c.e.t, c.get(path))
	m := csrfRE.FindStringSubmatch(body)
	if m == nil {
		c.e.t.Fatalf("no CSRF token in %s:\n%s", path, body)
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
	if want != http.StatusOK {
		resp.Body.Close()
	}
}

const sp = "/dflt/default0"

// create makes a node and returns its public ID.
func (c *client) create(kind, parent, title, body string) string {
	c.e.t.Helper()
	resp := c.post(sp+"/new", url.Values{
		"kind": {kind}, "parent": {parent}, "title": {title}, "body": {body}, "action": {"save"},
	})
	wantStatus(c.e.t, resp, http.StatusSeeOther)
	loc := resp.Header.Get("Location")
	m := regexp.MustCompile(`^` + sp + `/d/([0-9A-Za-z]{12})(/|$)`).FindStringSubmatch(loc)
	if m == nil {
		c.e.t.Fatalf("create redirects to %q", loc)
	}
	return m[1]
}

func (c *client) save(id, title, body string, version int) *http.Response {
	return c.post(sp+"/edit/"+id, url.Values{
		"title": {title}, "body": {body}, "version": {strconv.Itoa(version)}, "action": {"save"},
	})
}

func TestRootRedirect(t *testing.T) {
	e := newEnv(t)
	resp := e.anon().get("/")
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/dflt/" {
		t.Fatalf("root redirects to %q", loc)
	}
	wantStatus(t, e.anon().get("/zzzz/"), http.StatusNotFound)
	wantStatus(t, e.anon().get("/toolong/"), http.StatusNotFound)
	wantStatus(t, e.anon().get("/dflt/nospace1/"), http.StatusNotFound)
}

func TestAnonymousIsRedirected(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	for _, path := range []string{"/dflt/", sp + "/", "/dflt/admin/users", "/dflt/search?q=x"} {
		resp := c.get(path)
		wantStatus(t, resp, http.StatusSeeOther)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/dflt/login?return_to=") {
			t.Fatalf("GET %s redirects to %q", path, loc)
		}
	}
	resp := c.get("/admin/")
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/admin/login") {
		t.Fatalf("GET /admin/ redirects to %q", loc)
	}
	wantStatus(t, c.post(sp+"/new", url.Values{"title": {"x"}}), http.StatusUnauthorized)
}

func TestPublicRoutes(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	wantStatus(t, c.get("/healthz"), http.StatusOK)
	wantStatus(t, c.get("/readyz"), http.StatusOK)
	wantStatus(t, c.get("/dflt/signed-out"), http.StatusOK)
	wantStatus(t, c.get("/admin/signed-out"), http.StatusOK)
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
	resp := c.get("/dflt/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")

	wantStatus(t, e.anon().get("/dflt/auth/callback?code=alice&state="+url.QueryEscape(state)), http.StatusBadRequest)
	wantStatus(t, c.get("/dflt/auth/callback?code=alice&state=wrong"), http.StatusBadRequest)
	// A state from a tenant login is not valid for the system login.
	u, _ := url.Parse(e.srv.URL)
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "skriva_login_system", Value: state, Path: "/"}})
	wantStatus(t, c.get("/admin/auth/callback?code=root&state="+url.QueryEscape(state)), http.StatusBadRequest)
}

func TestLoginStateIsOneTime(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	resp := c.get("/dflt/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")
	cb := "/dflt/auth/callback?code=alice&state=" + url.QueryEscape(state)
	wantStatus(t, c.get(cb), http.StatusSeeOther)
	u, _ := url.Parse(e.srv.URL)
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "skriva_login_dflt", Value: state, Path: "/"}})
	wantStatus(t, c.get(cb), http.StatusBadRequest)
}

func TestOpenRedirectBlocked(t *testing.T) {
	e := newEnv(t)
	c := e.login("carol")
	resp := c.get("/dflt/login?return_to=//evil.example/")
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/dflt/" {
		t.Fatalf("redirect to %q", loc)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	for range 200 {
		resp := c.get("/dflt/login")
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatal("200 login requests did not reach the rate limit")
}

func TestTenantWithoutIdP(t *testing.T) {
	e := newEnv(t)
	if _, err := e.st.CreateTenant(ctx, "noid", store.TenantSettings{
		Name: "No IdP", DefaultRole: "viewer", SessionTTL: time.Hour, MaxDocBytes: 1 << 20,
	}, ""); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.anon().get("/noid/login"), http.StatusServiceUnavailable)
}

func TestViewerCannotWrite(t *testing.T) {
	e := newEnv(t)
	carol := e.login("carol")
	wantStatus(t, carol.get(sp+"/"), http.StatusOK)
	wantStatus(t, carol.get(sp+"/new"), http.StatusForbidden)
	wantStatus(t, carol.post(sp+"/new", url.Values{"kind": {"document"}, "title": {"x"}}), http.StatusForbidden)
	wantStatus(t, carol.get("/dflt/admin/users"), http.StatusForbidden)
	wantStatus(t, carol.get("/dflt/admin/spaces"), http.StatusForbidden)

	bob := e.login("bob")
	id := bob.create("document", "", "Home", "text")
	wantStatus(t, carol.get(sp+"/d/"+id+"/home"), http.StatusOK)
	wantStatus(t, carol.save(id, "Home", "changed", 1), http.StatusForbidden)
	wantStatus(t, carol.post(sp+"/delete/"+id, nil), http.StatusForbidden)
	wantStatus(t, carol.post(sp+"/move/"+id, url.Values{"parent": {""}}), http.StatusForbidden)
}

func TestTreeAndDocuments(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")

	guides := bob.create("folder", "", "Guides", "ignored")
	setup := bob.create("folder", guides, "Setup", "")
	install := bob.create("document", setup, "Install", "# Install\n\nSee [[Configure]].")
	linux := bob.create("document", install, "Install on Linux", "Use **Podman**.")

	// A folder cannot be in a document.
	wantStatus(t, bob.post(sp+"/new", url.Values{"kind": {"folder"}, "parent": {install}, "title": {"x"}}), http.StatusBadRequest)
	wantStatus(t, bob.get(sp+"/new?kind=folder&parent="+install), http.StatusBadRequest)
	wantStatus(t, bob.post(sp+"/new", url.Values{"kind": {"document"}, "parent": {"000000000000"}, "title": {"x"}}), http.StatusBadRequest)
	wantStatus(t, bob.post(sp+"/new", url.Values{"kind": {"other"}, "title": {"x"}}), http.StatusBadRequest)

	// The short link redirects to the canonical link.
	resp := bob.get(sp + "/d/" + linux)
	wantStatus(t, resp, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != sp+"/d/"+linux+"/install-on-linux" {
		t.Fatalf("short link redirects to %q", loc)
	}
	body := readBody(t, bob.get(sp+"/d/"+linux+"/install-on-linux"))
	for _, want := range []string{"<strong>Podman</strong>", ">Guides</a>", ">Setup</a>", ">Install</a>", `aria-current="page"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("document page does not contain %q:\n%s", want, body)
		}
	}
	folder := readBody(t, bob.get(sp+"/d/"+guides+"/guides"))
	if !strings.Contains(folder, ">Setup</a>") || strings.Contains(folder, "ignored") {
		t.Fatalf("folder page:\n%s", folder)
	}

	// Wiki links.
	page := readBody(t, bob.get(sp+"/d/"+install+"/install"))
	if !strings.Contains(page, `href="`+sp+`/w/configure"`) {
		t.Fatalf("wiki link not rendered:\n%s", page)
	}
	wantStatus(t, bob.get(sp+"/w/configure"), http.StatusNotFound)
	cfg := bob.create("document", "", "Configure", "x")
	resp = bob.get(sp + "/w/configure")
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, cfg) {
		t.Fatalf("wiki link redirects to %q", loc)
	}

	// Rename changes the slug. The old link redirects.
	wantStatus(t, bob.save(cfg, "Configuration", "x", 1), http.StatusSeeOther)
	resp = bob.get(sp + "/d/" + cfg + "/configure")
	wantStatus(t, resp, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != sp+"/d/"+cfg+"/configuration" {
		t.Fatalf("old slug redirects to %q", loc)
	}

	// Conflict, preview, history and restore.
	wantStatus(t, bob.save(cfg, "Configuration", "lost", 1), http.StatusConflict)
	resp = bob.post(sp+"/edit/"+cfg, url.Values{"title": {"C"}, "body": {"**p**"}, "version": {"2"}, "action": {"preview"}})
	wantStatus(t, resp, http.StatusOK)
	if b := readBody(t, resp); !strings.Contains(b, "<strong>p</strong>") {
		t.Fatalf("no preview:\n%s", b)
	}
	if b := readBody(t, bob.get(sp+"/history/"+cfg)); !strings.Contains(b, "/history/"+cfg+"/1") {
		t.Fatalf("history:\n%s", b)
	}
	wantStatus(t, bob.get(sp+"/history/"+cfg+"/1"), http.StatusOK)
	wantStatus(t, bob.get(sp+"/history/"+cfg+"/9"), http.StatusNotFound)
	if b := readBody(t, bob.get(sp+"/edit/"+cfg+"?from=1")); !strings.Contains(b, `value="Configure"`) {
		t.Fatalf("restore form:\n%s", b)
	}
	if raw := readBody(t, bob.get(sp+"/raw/"+linux)); raw != "Use **Podman**." {
		t.Fatalf("raw = %q", raw)
	}

	// Move.
	wantStatus(t, bob.post(sp+"/move/"+guides, url.Values{"parent": {setup}}), http.StatusBadRequest)
	wantStatus(t, bob.post(sp+"/move/"+setup, url.Values{"parent": {install}}), http.StatusBadRequest)
	form := readBody(t, bob.get(sp+"/move/"+install))
	if strings.Contains(form, linux) || !strings.Contains(form, "Guides / Setup") {
		t.Fatalf("move form targets:\n%s", form)
	}
	wantStatus(t, bob.post(sp+"/move/"+linux, url.Values{"parent": {""}}), http.StatusSeeOther)

	// Search in the space and in the tenant.
	if b := readBody(t, bob.get(sp+"/search?q=podman")); !strings.Contains(b, linux) || !strings.Contains(b, "<mark>") {
		t.Fatalf("space search:\n%s", b)
	}
	if b := readBody(t, bob.get("/dflt/search?q=podman")); !strings.Contains(b, linux) {
		t.Fatalf("tenant search:\n%s", b)
	}

	// Delete the folder with its subtree. Editors cannot delete.
	wantStatus(t, bob.post(sp+"/delete/"+guides, nil), http.StatusForbidden)
	mary := e.login("mary")
	if b := readBody(t, mary.get(sp+"/delete/"+guides)); !strings.Contains(b, "<strong>2</strong>") {
		t.Fatalf("delete form does not show the count:\n%s", b)
	}
	wantStatus(t, mary.post(sp+"/delete/"+guides, nil), http.StatusSeeOther)
	wantStatus(t, mary.get(sp+"/d/"+install), http.StatusNotFound)
	wantStatus(t, mary.get(sp+"/d/"+linux+"/install-on-linux"), http.StatusOK)

	wantStatus(t, bob.get(sp+"/recent"), http.StatusOK)
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	id := bob.create("document", "", "Home", "x")
	wantStatus(t, bob.save(id, "", "x", 1), http.StatusUnprocessableEntity)
	wantStatus(t, bob.save(id, strings.Repeat("x", 201), "x", 1), http.StatusUnprocessableEntity)
	wantStatus(t, bob.save(id, "Home", strings.Repeat("x", 3<<20), 1), http.StatusRequestEntityTooLarge)
	wantStatus(t, bob.save(id, "Home", strings.Repeat("x", 1<<20+1), 1), http.StatusUnprocessableEntity)
	wantStatus(t, bob.post(sp+"/edit/"+id, url.Values{"title": {"x"}, "version": {"abc"}}), http.StatusBadRequest)
	wantStatus(t, bob.post(sp+"/edit/"+id, url.Values{"title": {"x"}, "version": {"99999999999"}}), http.StatusBadRequest)
	wantStatus(t, bob.get(sp+"/history/"+id+"/99999999999"), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/edit/"+id+"?from=99999999999"), http.StatusOK)
	wantStatus(t, bob.get(sp+"/d/short"), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/d/000000000000"), http.StatusNotFound)
}

func TestXSSIsEscaped(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	id := bob.create("document", "", `<script>alert("t")</script>`, "<script>alert(1)</script>\n\n[x](javascript:alert(2))")
	body := readBody(t, bob.get(sp+"/d/"+id+"/script-alert-t-script"))
	if strings.Contains(body, "<script") || strings.Contains(body, "javascript:") {
		t.Fatalf("page contains script:\n%s", body)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	form := url.Values{"kind": {"document"}, "title": {"Home"}, "csrf_token": {"wrong"}}
	wantStatus(t, bob.post(sp+"/new", form), http.StatusForbidden)
	form.Set("csrf_token", "")
	wantStatus(t, bob.post(sp+"/new", form), http.StatusForbidden)

	form.Set("csrf_token", bob.csrf)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+sp+"/new", strings.NewReader(form.Encode()))
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
	resp := bob.post("/dflt/logout", nil)
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "https://idp.example/logout") ||
		!strings.Contains(loc, url.QueryEscape("http://example.test/dflt/signed-out")) {
		t.Fatalf("logout redirects to %q", loc)
	}
	wantStatus(t, bob.get("/dflt/"), http.StatusSeeOther)
}

func TestTenantAdmin(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	carol := e.login("carol")

	for _, p := range []string{"/dflt/admin/users", "/dflt/admin/groups", "/dflt/admin/roles", "/dflt/admin/bindings", "/dflt/admin/spaces"} {
		wantStatus(t, alice.get(p), http.StatusOK)
	}
	users, _ := e.st.Users(ctx, e.dft.ID)
	var carolID, aliceID int64
	for _, u := range users {
		switch u.Username {
		case "carol":
			carolID = u.ID
		case "alice":
			aliceID = u.ID
		}
	}
	carolPath := "/dflt/admin/users/" + strconv.FormatInt(carolID, 10)
	wantStatus(t, alice.get(carolPath), http.StatusOK)

	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {"user:" + strconv.FormatInt(carolID, 10)}, "return_to": {carolPath},
	}), http.StatusSeeOther)
	carol.create("document", "", "Carol", "x")

	wantStatus(t, alice.post("/dflt/admin/roles", url.Values{"name": {"bad"}, "permission": {"doc.everything"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/roles", url.Values{"name": {"deleter"}, "permission": {"doc.read", "doc.delete"}}), http.StatusSeeOther)
	wantStatus(t, alice.post("/dflt/admin/roles", url.Values{"name": {"viewer"}}), http.StatusConflict)

	resp := alice.post("/dflt/admin/groups", url.Values{"name": {"Team"}})
	wantStatus(t, resp, http.StatusSeeOther)
	groupPath := resp.Header.Get("Location")
	wantStatus(t, alice.post(groupPath+"/members", url.Values{"user_id": {strconv.FormatInt(carolID, 10)}}), http.StatusSeeOther)
	if b := readBody(t, alice.get(groupPath)); !strings.Contains(b, "carol") {
		t.Fatalf("group page does not show member:\n%s", b)
	}

	// Spaces.
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Team"}, "slug": {"short"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Team"}, "slug": {"teamspce"}}), http.StatusSeeOther)
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Again"}, "slug": {"teamspce"}}), http.StatusConflict)
	wantStatus(t, alice.get("/dflt/teamspce/"), http.StatusOK)
	wantStatus(t, alice.post("/dflt/admin/spaces/teamspce", url.Values{"name": {"Team space"}}), http.StatusSeeOther)
	wantStatus(t, alice.post("/dflt/admin/spaces/teamspce/delete", url.Values{"confirm": {"wrong"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/teamspce/delete", url.Values{"confirm": {"teamspce"}}), http.StatusSeeOther)
	wantStatus(t, alice.get("/dflt/teamspce/"), http.StatusNotFound)

	wantStatus(t, alice.post("/dflt/admin/users/"+strconv.FormatInt(aliceID, 10)+"/disable", nil), http.StatusBadRequest)
	wantStatus(t, alice.post(carolPath+"/disable", nil), http.StatusSeeOther)
	wantStatus(t, carol.get("/dflt/"), http.StatusSeeOther)
}

func TestSystemAdmin(t *testing.T) {
	e := newEnv(t)

	// A user without the system admin group cannot sign in.
	c := e.anon()
	resp := c.get("/admin/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	wantStatus(t, c.get("/admin/auth/callback?code=alice&state="+url.QueryEscape(loc.Query().Get("state"))), http.StatusForbidden)

	// A tenant session does not give access to the system administration.
	alice := e.login("alice")
	wantStatus(t, alice.get("/admin/"), http.StatusSeeOther)

	root := e.loginAt("/admin", "root")
	// A system session does not give access to a tenant.
	wantStatus(t, root.get("/dflt/"), http.StatusSeeOther)

	tenantForm := url.Values{
		"code": {"acme"}, "name": {"Acme"}, "oidc_issuer": {"https://idp.example"},
		"oidc_client_id": {"acme"}, "oidc_client_secret": {"s3cret"}, "oidc_scopes": {"openid profile"},
		"oidc_groups_claim": {"groups"}, "admin_group": {"acme-admins"}, "default_role": {"viewer"},
		"session_ttl": {"8h"}, "max_doc_kib": {"512"},
	}
	bad := url.Values{}
	for k, v := range tenantForm {
		bad[k] = v
	}
	bad.Set("code", "ACME")
	wantStatus(t, root.post("/admin/tenants", bad), http.StatusUnprocessableEntity)
	bad.Set("code", "acm2")
	bad.Set("oidc_issuer", "http://idp.example")
	wantStatus(t, root.post("/admin/tenants", bad), http.StatusUnprocessableEntity)
	bad.Set("oidc_issuer", "https://idp.example")
	bad.Set("default_role", "nothere")
	wantStatus(t, root.post("/admin/tenants", bad), http.StatusUnprocessableEntity)

	wantStatus(t, root.post("/admin/tenants", tenantForm), http.StatusSeeOther)
	wantStatus(t, root.post("/admin/tenants", tenantForm), http.StatusUnprocessableEntity)
	acme, err := e.st.TenantByCode(ctx, "acme")
	if err != nil || acme.MaxDocBytes != 512*1024 || acme.SessionTTL != 8*time.Hour {
		t.Fatalf("tenant = %+v, %v", acme, err)
	}
	if s, _ := e.st.ClientSecret(ctx, acme); s != "s3cret" {
		t.Fatalf("secret = %q", s)
	}
	page := readBody(t, root.get("/admin/tenants/acme"))
	if strings.Contains(page, "s3cret") {
		t.Fatal("the tenant page shows the client secret")
	}
	if !strings.Contains(page, "http://example.test/acme/auth/callback") {
		t.Fatalf("tenant page does not show the redirect URI:\n%s", page)
	}

	// An update with an empty secret keeps the secret.
	tenantForm.Set("name", "Acme Inc")
	tenantForm.Set("oidc_client_secret", "")
	wantStatus(t, root.post("/admin/tenants/acme", tenantForm), http.StatusSeeOther)
	acme, _ = e.st.TenantByCode(ctx, "acme")
	if s, _ := e.st.ClientSecret(ctx, acme); acme.Name != "Acme Inc" || s != "s3cret" {
		t.Fatalf("after update: %q, %q", acme.Name, s)
	}

	// A space for the new tenant.
	wantStatus(t, root.post("/admin/tenants/acme/spaces", url.Values{"name": {"Docs"}, "slug": {"acmedocs"}}), http.StatusSeeOther)

	// Users of the new tenant. The admin group of the tenant has the
	// admin role.
	idp := e.anon()
	idp.loginAt("/acme", "alice")
	wantStatus(t, idp.get("/acme/acmedocs/"), http.StatusOK)
	wantStatus(t, idp.get("/acme/admin/users"), http.StatusForbidden)

	// The session of one tenant is not valid in a different tenant.
	u, _ := url.Parse(e.srv.URL)
	var acmeToken string
	for _, ck := range idp.http.Jar.Cookies(u) {
		if ck.Name == "skriva_acme" {
			acmeToken = ck.Value
		}
	}
	if acmeToken == "" {
		t.Fatal("no session cookie for tenant acme")
	}
	other := e.anon()
	other.http.Jar.SetCookies(u, []*http.Cookie{{Name: "skriva_dflt", Value: acmeToken, Path: "/"}})
	wantStatus(t, other.get("/dflt/"), http.StatusSeeOther)
	// The default tenant cannot see the space of tenant acme.
	wantStatus(t, alice.get("/dflt/acmedocs/"), http.StatusNotFound)

	// Disable the tenant.
	tenantForm.Set("disabled", "on")
	wantStatus(t, root.post("/admin/tenants/acme", tenantForm), http.StatusSeeOther)
	wantStatus(t, idp.get("/acme/acmedocs/"), http.StatusForbidden)

	// Delete.
	wantStatus(t, root.post("/admin/tenants/acme/delete", url.Values{"confirm": {"nope"}}), http.StatusBadRequest)
	wantStatus(t, root.post("/admin/tenants/acme/delete", url.Values{"confirm": {"acme"}}), http.StatusSeeOther)
	wantStatus(t, root.get("/admin/tenants/acme"), http.StatusNotFound)
	wantStatus(t, root.post("/admin/tenants/dflt/delete", url.Values{"confirm": {"dflt"}}), http.StatusBadRequest)

	resp = root.post("/admin/logout", nil)
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, url.QueryEscape("http://example.test/admin/signed-out")) {
		t.Fatalf("system logout redirects to %q", loc)
	}
}

func testLogOutput() io.Writer {
	if testing.Verbose() {
		return os.Stderr
	}
	return io.Discard
}

func TestSpacePermissions(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	carol := e.login("carol")
	bob := e.login("bob")

	// Make a private space with a document.
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Secret"}, "slug": {"secret00"}}), http.StatusSeeOther)
	wantStatus(t, alice.get("/dflt/admin/spaces/secret00/access"), http.StatusOK)
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/access", url.Values{"default_role": {"bad role"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/access", url.Values{"default_role": {"nothere"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/access", url.Values{"default_role": {""}}), http.StatusSeeOther)
	resp := alice.post("/dflt/secret00/new", url.Values{"kind": {"document"}, "title": {"Plans"}, "body": {"walrus"}, "action": {"save"}})
	wantStatus(t, resp, http.StatusSeeOther)
	doc := resp.Header.Get("Location")

	// Carol has only the tenant default role, thus no access. Bob is an
	// editor through a group binding for all spaces. That binding applies
	// also in the private space.
	wantStatus(t, carol.get("/dflt/secret00/"), http.StatusForbidden)
	wantStatus(t, carol.get(doc), http.StatusNotFound)
	wantStatus(t, bob.get(doc), http.StatusOK)

	// Carol does not see the space on the tenant page or in the search.
	if b := readBody(t, carol.get("/dflt/")); strings.Contains(b, "secret00") || !strings.Contains(b, "default0") {
		t.Fatalf("tenant page of carol:\n%s", b)
	}
	if b := readBody(t, carol.get("/dflt/search?q=walrus")); strings.Contains(b, "Plans") {
		t.Fatalf("search of carol finds the private document:\n%s", b)
	}
	if b := readBody(t, alice.get("/dflt/search?q=walrus")); !strings.Contains(b, "Plans") {
		t.Fatalf("search of alice does not find the document:\n%s", b)
	}

	// Give carol the editor role only in the private space.
	users, _ := e.st.Users(ctx, e.dft.ID)
	var carolID int64
	for _, u := range users {
		if u.Username == "carol" {
			carolID = u.ID
		}
	}
	subject := "user:" + strconv.FormatInt(carolID, 10)
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {subject}, "space": {"nospace0"},
	}), http.StatusNotFound)
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {subject}, "space": {"bad"},
	}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {subject}, "space": {"secret00"},
		"return_to": {"/dflt/admin/spaces/secret00/access"},
	}), http.StatusSeeOther)
	if b := readBody(t, alice.get("/dflt/admin/spaces/secret00/access")); !strings.Contains(b, "carol") {
		t.Fatalf("access page does not show the binding:\n%s", b)
	}

	wantStatus(t, carol.get(doc), http.StatusOK)
	wantStatus(t, carol.get("/dflt/secret00/new"), http.StatusOK)
	// The space binding does not apply in the default space.
	wantStatus(t, carol.get(sp+"/new"), http.StatusForbidden)
	// The new space appears for carol.
	if b := readBody(t, carol.get("/dflt/")); !strings.Contains(b, "secret00") {
		t.Fatalf("tenant page of carol after the binding:\n%s", b)
	}
	// The user page shows the permissions per space.
	if b := readBody(t, alice.get("/dflt/admin/users/"+strconv.FormatInt(carolID, 10))); !strings.Contains(b, "<code>doc.edit</code>") {
		t.Fatalf("user page does not show the space permissions:\n%s", b)
	}

	// The admin role for one space does not give tenant administration.
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"admin"}, "subject": {subject}, "space": {"secret00"},
	}), http.StatusSeeOther)
	wantStatus(t, carol.post("/dflt/secret00/delete/"+doc[strings.Index(doc, "/d/")+3:][:12], nil), http.StatusSeeOther)
	wantStatus(t, carol.get("/dflt/admin/users"), http.StatusForbidden)
	wantStatus(t, carol.get("/dflt/admin/spaces"), http.StatusForbidden)
	if b := readBody(t, carol.get("/dflt/secret00/")); strings.Contains(b, "Administration") {
		t.Fatalf("space admin sees the administration link:\n%s", b)
	}

	// "Same as the tenant" gives the tenant default role back.
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/access", url.Values{"default_role": {"-"}}), http.StatusSeeOther)
	sp2, _ := e.st.Space(ctx, e.dft.ID, "secret00")
	if sp2.DefaultRole != nil {
		t.Fatalf("default role = %q", *sp2.DefaultRole)
	}
}

func TestFolderPermissions(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	bob := e.login("bob")
	carol := e.login("carol")
	users, _ := e.st.Users(ctx, e.dft.ID)
	ids := map[string]string{}
	for _, u := range users {
		ids[u.Username] = "user:" + strconv.FormatInt(u.ID, 10)
	}

	hr := alice.create("folder", "", "HR", "")
	salaries := alice.create("document", hr, "Salaries", "the payroll text")
	public := alice.create("document", "", "Public", "x")
	bobFolder := bob.create("folder", "", "Bob folder", "")

	// Only users with tenant.manage can open the access page of a folder.
	// Then make HR private. Bob is an editor for all spaces: he still has
	// access. Carol (default role viewer) has no access.
	wantStatus(t, carol.get(sp+"/access/"+hr), http.StatusForbidden)
	wantStatus(t, bob.get(sp+"/access/"+hr), http.StatusForbidden)
	wantStatus(t, alice.get(sp+"/access/"+salaries), http.StatusNotFound)
	wantStatus(t, alice.post(sp+"/access/"+hr, url.Values{"default_role": {""}}), http.StatusSeeOther)
	wantStatus(t, alice.get(sp+"/access/"+hr), http.StatusOK)

	for _, p := range []string{"/d/" + hr, "/d/" + salaries, "/history/" + salaries, "/raw/" + salaries, "/edit/" + salaries} {
		wantStatus(t, carol.get(sp+p), http.StatusNotFound)
	}
	wantStatus(t, carol.get(sp+"/d/"+public+"/public"), http.StatusOK)
	if b := readBody(t, carol.get(sp+"/")); strings.Contains(b, "HR") || strings.Contains(b, "Salaries") {
		t.Fatalf("tree of carol shows the private folder:\n%s", b)
	}
	if b := readBody(t, carol.get(sp+"/search?q=payroll")); strings.Contains(b, "Salaries") {
		t.Fatalf("space search of carol finds the private document:\n%s", b)
	}
	if b := readBody(t, carol.get("/dflt/search?q=payroll")); strings.Contains(b, "Salaries") {
		t.Fatalf("tenant search of carol finds the private document:\n%s", b)
	}
	if b := readBody(t, carol.get(sp+"/recent")); strings.Contains(b, "Salaries") {
		t.Fatalf("recent changes of carol show the private document:\n%s", b)
	}
	wantStatus(t, carol.get(sp+"/w/salaries"), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/d/"+salaries+"/salaries"), http.StatusOK)

	// Give carol the editor role for the folder HR only.
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {salaries},
	}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {"000000000000"},
	}), http.StatusNotFound)
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {hr},
		"return_to": {sp + "/access/" + hr},
	}), http.StatusSeeOther)
	if b := readBody(t, alice.get(sp+"/access/"+hr)); !strings.Contains(b, "carol") {
		t.Fatalf("access page does not show the binding:\n%s", b)
	}
	if b := readBody(t, alice.get("/dflt/admin/bindings")); !strings.Contains(b, `Folder "HR"`) {
		t.Fatalf("binding list does not show the folder scope:\n%s", b)
	}

	wantStatus(t, carol.get(sp+"/d/"+salaries+"/salaries"), http.StatusOK)
	wantStatus(t, carol.save(salaries, "Salaries", "new text", 1), http.StatusSeeOther)
	child := carol.create("document", hr, "Bonus", "x")
	// The binding does not apply outside the folder.
	wantStatus(t, carol.save(public, "Public", "changed", 1), http.StatusForbidden)
	wantStatus(t, carol.post(sp+"/new", url.Values{"kind": {"document"}, "title": {"Top"}}), http.StatusForbidden)
	wantStatus(t, carol.get(sp+"/new?kind=document"), http.StatusForbidden)

	// Carol cannot move a document out of HR to the top level, or into a
	// folder where she cannot create items.
	wantStatus(t, carol.post(sp+"/move/"+child, url.Values{"parent": {""}}), http.StatusForbidden)
	wantStatus(t, carol.post(sp+"/move/"+child, url.Values{"parent": {bobFolder}}), http.StatusForbidden)
	if b := readBody(t, carol.get(sp+"/move/"+child)); strings.Contains(b, "Top of the space") || strings.Contains(b, "Bob folder") {
		t.Fatalf("move form shows places without permission:\n%s", b)
	}

	// Editors cannot delete. A maintainer binding on the folder can.
	wantStatus(t, carol.post(sp+"/delete/"+child, nil), http.StatusForbidden)

	// Mary is a maintainer for all spaces. That binding applies also in
	// the private folder.
	mary := e.login("mary")
	inner := alice.create("folder", hr, "Inner", "")
	if b := readBody(t, mary.get(sp+"/delete/"+hr)); strings.Contains(b, "You cannot delete") {
		t.Fatalf("delete form of mary:\n%s", b)
	}

	// A folder binding with the admin role does not give tenant
	// administration.
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"admin"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {hr},
	}), http.StatusSeeOther)
	wantStatus(t, carol.get(sp+"/access/"+hr), http.StatusForbidden)
	wantStatus(t, carol.get("/dflt/admin/users"), http.StatusForbidden)

	// The delete check covers the subtree. HR gets the default role
	// maintainer, and the inner folder gets no default role. Dave has only
	// default roles. Thus dave cannot delete HR, because he cannot delete
	// the inner folder.
	wantStatus(t, alice.post(sp+"/access/"+hr, url.Values{"default_role": {"maintainer"}}), http.StatusSeeOther)
	wantStatus(t, alice.post(sp+"/access/"+inner, url.Values{"default_role": {""}}), http.StatusSeeOther)
	dave := e.login("dave")
	if b := readBody(t, dave.get(sp+"/delete/"+hr)); !strings.Contains(b, "You cannot delete this item") {
		t.Fatalf("delete form of dave:\n%s", b)
	}
	wantStatus(t, dave.post(sp+"/delete/"+hr, nil), http.StatusForbidden)
	// Dave can delete the document Bonus in HR.
	wantStatus(t, dave.post(sp+"/delete/"+child, nil), http.StatusSeeOther)
	// Alice can delete everything.
	wantStatus(t, alice.post(sp+"/delete/"+hr, nil), http.StatusSeeOther)
}
