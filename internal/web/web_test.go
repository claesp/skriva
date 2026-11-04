package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claesp/skriva/internal/diagram"
	"github.com/claesp/skriva/internal/export"
	"github.com/claesp/skriva/internal/store"
	"github.com/claesp/skriva/internal/testdb"
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
	if dft, err = st.UpdateTenant(ctx, dft.ID, dft.Code, ts, nil); err != nil {
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
	// SKRIVA_TEST_TYPST is the path of the Typst program for the tests of
	// the PDF export. Without it, the PDF export is not available.
	var pdf *export.PDFOptions
	if path := os.Getenv("SKRIVA_TEST_TYPST"); path != "" {
		pdf = &export.PDFOptions{Typst: path, TempDir: os.Getenv("SKRIVA_TEST_TYPST_TMP")}
	}
	s, err := New(st, Options{
		Formats:          export.NewFormats(pdf),
		Diagrams:         diagram.New(diagram.Options{Program: mmdrProgram(t), Timeout: 30 * time.Second}),
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

// mmdrProgram returns the path of the mmdr program for the tests. It is
// SKRIVA_TEST_MMDR, or a fake program. The fake program fails if the
// text ends with "-->". Else it writes an SVG image with the text and a
// javascript: link, the same as a "click" line of Mermaid.
func mmdrProgram(t *testing.T) string {
	if path := os.Getenv("SKRIVA_TEST_MMDR"); path != "" {
		return path
	}
	prog := filepath.Join(t.TempDir(), "mmdr")
	script := `#!/bin/sh
src=$(cat)
case "$src" in *'-->') echo 'error: invalid flowchart edge syntax: A-->' >&2; exit 1;; esac
printf '<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><text>%s</text></a></svg>' "$src"
`
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return prog
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

// docBody returns the JSON of a document. A text that does not start
// with "{" becomes one paragraph. Other text is the JSON.
func docBody(text string) string {
	if strings.HasPrefix(text, "{") {
		return text
	}
	t, _ := json.Marshal(text)
	return `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":` + string(t) + `}]}]}`
}

// create makes a node and returns its public ID.
func (c *client) create(kind, parent, title, body string) string {
	c.e.t.Helper()
	resp := c.post(sp+"/new", url.Values{
		"kind": {kind}, "parent": {parent}, "title": {title}, "body": {docBody(body)}, "action": {"save"},
	})
	wantStatus(c.e.t, resp, http.StatusSeeOther)
	loc := resp.Header.Get("Location")
	m := regexp.MustCompile(`^` + sp + `/d/([0-9a-z]{14})(/|$)`).FindStringSubmatch(loc)
	if m == nil {
		c.e.t.Fatalf("create redirects to %q", loc)
	}
	return m[1]
}

func (c *client) save(id, title, body string, version int) *http.Response {
	return c.post(sp+"/edit/"+id, url.Values{
		"title": {title}, "body": {docBody(body)}, "version": {strconv.Itoa(version)}, "action": {"save"},
	})
}

// selectList returns the first select element of a page. The page also
// contains the tree of the space, thus a test of the select must not use
// the full page.
func selectList(page string) string {
	i := strings.Index(page, "<select")
	if i < 0 {
		return ""
	}
	j := strings.Index(page[i:], "</select>")
	if j < 0 {
		return page[i:]
	}
	return page[i : i+j]
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

// TestFonts checks that each font in the style sheet is available, and that the browser can keep the font files.
func TestFonts(t *testing.T) {
	e := newEnv(t)
	c := e.anon()
	css, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	urls := regexp.MustCompile(`url\("(fonts/[^"]+)"\)`).FindAllStringSubmatch(string(css), -1)
	if len(urls) != 12 {
		t.Fatalf("style sheet has %d font URLs, want 12", len(urls))
	}
	for _, u := range urls {
		resp := c.get("/static/" + u[1])
		wantStatus(t, resp, http.StatusOK)
		if ct := resp.Header.Get("Content-Type"); ct != "font/woff2" {
			t.Errorf("%s: content type %q", u[1], ct)
		}
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("%s: cache control %q", u[1], cc)
		}
	}
	// The page does not preload fonts. See docs/fonts.md.
	page := c.get("/dflt/signed-out")
	if body := readBody(t, page); strings.Contains(body, `rel="preload"`) {
		t.Errorf("page preloads files")
	}
	if csp := page.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "font-src 'self'") {
		t.Errorf("CSP does not permit the fonts: %s", csp)
	}
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
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "skriva_login_t" + strconv.FormatInt(e.dft.ID, 10), Value: state, Path: "/"}})
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
	install := bob.create("document", setup, "Install", `{"type":"doc","content":[
		{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Install"}]},
		{"type":"paragraph","content":[{"type":"text","text":"See "},
			{"type":"text","text":"Configure","marks":[{"type":"link","attrs":{"href":"wiki:Configure"}}]}]}]}`)
	linux := bob.create("document", install, "Install on Linux", `{"type":"doc","content":[{"type":"paragraph","content":[
		{"type":"text","text":"Use "},{"type":"text","text":"Podman","marks":[{"type":"bold"}]},{"type":"text","text":"."}]}]}`)

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
	// The case of the letters in a slug has no effect.
	resp = bob.get(sp + "/w/CONFIGURE")
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != sp+"/d/"+cfg+"/configure" {
		t.Fatalf("wiki link in upper case redirects to %q", loc)
	}
	resp = bob.get(sp + "/d/" + cfg + "/Configure")
	wantStatus(t, resp, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != sp+"/d/"+cfg+"/configure" {
		t.Fatalf("slug in mixed case redirects to %q", loc)
	}

	// Rename changes the slug. The old link redirects.
	wantStatus(t, bob.save(cfg, "Configuration", "x", 1), http.StatusSeeOther)
	resp = bob.get(sp + "/d/" + cfg + "/configure")
	wantStatus(t, resp, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != sp+"/d/"+cfg+"/configuration" {
		t.Fatalf("old slug redirects to %q", loc)
	}

	// Conflict, history and restore.
	wantStatus(t, bob.save(cfg, "Configuration", "lost", 1), http.StatusConflict)
	if b := readBody(t, bob.get(sp+"/history/"+cfg)); !strings.Contains(b, "/history/"+cfg+"/1") {
		t.Fatalf("history:\n%s", b)
	}
	wantStatus(t, bob.get(sp+"/history/"+cfg+"/1"), http.StatusOK)
	wantStatus(t, bob.get(sp+"/history/"+cfg+"/9"), http.StatusNotFound)
	if b := readBody(t, bob.get(sp+"/edit/"+cfg+"?from=1")); !strings.Contains(b, `value="Configure"`) {
		t.Fatalf("restore form:\n%s", b)
	}
	if md := readBody(t, bob.get(sp+"/export/"+linux+"/markdown/text")); !strings.HasSuffix(md, "\n\nUse **Podman**.\n") {
		t.Fatalf("export = %q", md)
	}

	// Move.
	wantStatus(t, bob.post(sp+"/move/"+guides, url.Values{"parent": {setup}}), http.StatusBadRequest)
	wantStatus(t, bob.post(sp+"/move/"+setup, url.Values{"parent": {install}}), http.StatusBadRequest)
	form := selectList(readBody(t, bob.get(sp+"/move/"+install)))
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
	// The link to the search in all spaces keeps the search text.
	if b := readBody(t, bob.get(sp+"/search?q=a%23b%26c")); !strings.Contains(b, "/dflt/search?q=a%23b%26c") {
		t.Fatalf("link to the search in all spaces:\n%s", b)
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

	// The folder is in the trash. Editors cannot see the trash. A
	// maintainer can restore the folder, but only an administrator can
	// delete it permanently.
	wantStatus(t, bob.get(sp+"/trash"), http.StatusForbidden)
	if b := readBody(t, bob.get(sp+"/")); strings.Contains(b, sp+"/trash") {
		t.Fatalf("editor sees the trash button:\n%s", b)
	}
	if b := readBody(t, mary.get(sp+"/")); !strings.Contains(b, sp+"/trash") {
		t.Fatalf("no trash button for the maintainer:\n%s", b)
	}
	b := readBody(t, mary.get(sp+"/trash"))
	if !strings.Contains(b, "Guides") || !strings.Contains(b, "and 2 items in it") ||
		!strings.Contains(b, sp+"/trash/"+guides+"/restore") || strings.Contains(b, sp+"/trash/"+guides+"/delete") {
		t.Fatalf("trash of the maintainer:\n%s", b)
	}
	wantStatus(t, bob.post(sp+"/trash/"+guides+"/restore", nil), http.StatusNotFound)
	wantStatus(t, mary.get(sp+"/trash/"+guides+"/delete"), http.StatusForbidden)
	wantStatus(t, mary.post(sp+"/trash/"+guides+"/delete", nil), http.StatusForbidden)
	wantStatus(t, mary.post(sp+"/trash/"+guides+"/restore", nil), http.StatusSeeOther)
	wantStatus(t, mary.get(sp+"/d/"+install), http.StatusFound)
	wantStatus(t, mary.post(sp+"/trash/"+guides+"/restore", nil), http.StatusNotFound)
	wantStatus(t, mary.post(sp+"/delete/"+guides, nil), http.StatusSeeOther)
	admin := e.login("alice")
	if b := readBody(t, admin.get(sp+"/trash/"+guides+"/delete")); !strings.Contains(b, "Delete permanently") {
		t.Fatalf("confirm page:\n%s", b)
	}
	wantStatus(t, admin.post(sp+"/trash/"+guides+"/delete", nil), http.StatusSeeOther)
	if b := readBody(t, admin.get(sp+"/trash")); !strings.Contains(b, "The trash is empty.") {
		t.Fatalf("trash after the permanent deletion:\n%s", b)
	}

	// Recent changes are in the administration. They show only documents
	// that the user can read.
	wantStatus(t, bob.get("/dflt/admin/recent"), http.StatusForbidden)
	alice := e.login("alice")
	if b := readBody(t, alice.get("/dflt/admin/recent")); !strings.Contains(b, "Install on Linux") {
		t.Fatalf("recent changes:\n%s", b)
	}

	// Public IDs are in lower case. An ID in upper case goes to the URL
	// in lower case.
	resp = bob.get(sp + "/d/" + strings.ToUpper(linux) + "/Install-On-Linux")
	wantStatus(t, resp, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != sp+"/d/"+linux+"/install-on-linux" {
		t.Fatalf("redirect to %q", loc)
	}

	// The page header has a drop-down for new items. A document can
	// contain only documents.
	b = readBody(t, bob.get(sp+"/d/"+linux+"/install-on-linux"))
	if !strings.Contains(b, "new?kind=document&amp;parent="+linux) || strings.Contains(b, "kind=folder") {
		t.Fatalf("header buttons on a document page:\n%s", b)
	}
	b = readBody(t, bob.get(sp+"/"))
	if !strings.Contains(b, `<details class="newmenu">`) || !strings.Contains(b, `new?kind=document"`) || !strings.Contains(b, `new?kind=folder"`) {
		t.Fatalf("header buttons on the space page:\n%s", b)
	}

	// The button of the current mode is marked. A second click goes back
	// to the document.
	doc := sp + "/d/" + linux + "/install-on-linux"
	for _, mode := range []string{"edit", "history", "move"} {
		b := readBody(t, bob.get(sp+"/"+mode+"/"+linux))
		if !strings.Contains(b, `class="tool`) || !strings.Contains(b, `href="`+doc+`" title=`) || !strings.Contains(b, `aria-current="page"`) {
			t.Fatalf("%s page has no current button that goes back:\n%s", mode, b)
		}
	}

	// Only the form of a document permits the script of the editor. The
	// form contains the JSON of the text and the toolbar.
	resp = bob.get(sp + "/edit/" + linux)
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP of the editor: %s", csp)
	}
	b = readBody(t, resp)
	for _, want := range []string{`<script src="/static/editor.js?v=`, `data-editor-toolbar`, `name="body" value="{&#34;type&#34;:&#34;doc&#34;`, `data-upload="` + sp + `/files?node=` + linux + `"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("editor page does not contain %q:\n%s", want, b)
		}
	}
	if csp := bob.get(doc).Header.Get("Content-Security-Policy"); strings.Contains(csp, "script-src") {
		t.Fatalf("CSP of a document permits scripts: %s", csp)
	}
	if csp := bob.get(sp + "/edit/" + guides).Header.Get("Content-Security-Policy"); strings.Contains(csp, "script-src") {
		t.Fatalf("CSP of the folder form permits scripts: %s", csp)
	}
	if b := readBody(t, bob.get(sp+"/new?kind=document&parent="+linux)); !strings.Contains(b, `data-upload="`+sp+`/files?parent=`+linux+`"`) {
		t.Fatalf("form of a new document:\n%s", b)
	}
	// A text that is not JSON is not valid.
	wantStatus(t, bob.save(linux, "Linux", "{", 1), http.StatusUnprocessableEntity)
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
	id := bob.create("document", "", `<script>alert("t")</script>`, `{"type":"doc","content":[
		{"type":"paragraph","content":[{"type":"text","text":"<script>alert(1)</script>"},
			{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(2)"}}]}]},
		{"type":"script","content":[{"type":"text","text":"alert(3)"}]},
		{"type":"image","attrs":{"src":"javascript:alert(4)","onerror":"alert(5)"}}]}`)
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
	for _, bad := range []string{"a", "-team", "team-", "Team", "seventeen-letters", "admin", "login", "search"} {
		wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Team"}, "slug": {bad}}), http.StatusBadRequest)
	}
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Team"}, "slug": {"teamspce"}}), http.StatusSeeOther)
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"Again"}, "slug": {"teamspce"}}), http.StatusConflict)
	wantStatus(t, alice.get("/dflt/teamspce/"), http.StatusOK)
	wantStatus(t, alice.post("/dflt/admin/spaces/teamspce", url.Values{"name": {"Team space"}, "slug": {"teamspce"}}), http.StatusSeeOther)
	wantStatus(t, alice.post("/dflt/admin/spaces/teamspce/delete", url.Values{"confirm": {"wrong"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/teamspce/delete", url.Values{"confirm": {"teamspce"}}), http.StatusSeeOther)
	wantStatus(t, alice.get("/dflt/teamspce/"), http.StatusNotFound)

	wantStatus(t, alice.post("/dflt/admin/users/"+strconv.FormatInt(aliceID, 10)+"/disable", nil), http.StatusBadRequest)
	wantStatus(t, alice.post(carolPath+"/disable", nil), http.StatusSeeOther)
	wantStatus(t, carol.get("/dflt/"), http.StatusSeeOther)
}

// TestAdminPages opens each page of the tenant administration. Each form
// has its own page.
func TestAdminPages(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	carol := e.login("carol")
	users, _ := e.st.Users(ctx, e.dft.ID)
	var carolID int64
	for _, u := range users {
		if u.Username == "carol" {
			carolID = u.ID
		}
	}
	carolPath := "/dflt/admin/users/" + strconv.FormatInt(carolID, 10)
	resp := alice.post("/dflt/admin/groups", url.Values{"name": {"Team"}})
	wantStatus(t, resp, http.StatusSeeOther)
	groupPath := resp.Header.Get("Location")
	groupID := groupPath[strings.LastIndex(groupPath, "/")+1:]
	wantStatus(t, alice.post("/dflt/admin/roles", url.Values{"name": {"reader"}, "permission": {"doc.read"}}), http.StatusSeeOther)
	roles, _ := e.st.Roles(ctx, e.dft.ID)
	var readerID, viewerID int64
	for _, r := range roles {
		switch r.Name {
		case "reader":
			readerID = r.ID
		case "viewer":
			viewerID = r.ID
		}
	}

	pages := []string{
		"/dflt/admin/users", carolPath, carolPath + "/bindings", carolPath + "/permissions",
		"/dflt/admin/groups", "/dflt/admin/groups/new", groupPath, groupPath + "/bindings",
		groupPath + "/members/new", groupPath + "/delete",
		"/dflt/admin/roles", "/dflt/admin/roles/new", "/dflt/admin/roles/" + strconv.FormatInt(readerID, 10) + "/delete",
		"/dflt/admin/bindings", "/dflt/admin/bindings/new",
		"/dflt/admin/bindings/new?subject=group:" + groupID + "&space=default0",
		"/dflt/admin/spaces", "/dflt/admin/spaces/new", "/dflt/admin/spaces/default0", "/dflt/admin/spaces/default0/edit",
		"/dflt/admin/spaces/default0/access", "/dflt/admin/spaces/default0/default-role",
		"/dflt/admin/spaces/default0/delete",
	}
	for _, p := range pages {
		wantStatus(t, alice.get(p), http.StatusOK)
		wantStatus(t, carol.get(p), http.StatusForbidden)
	}

	// The form for a binding of a group shows the group and the space as
	// fixed values.
	b := readBody(t, alice.get("/dflt/admin/bindings/new?subject=group:"+groupID+"&space=default0"))
	if !strings.Contains(b, "Group Team") || !strings.Contains(b, `name="space" value="default0"`) || strings.Contains(b, `<select name="subject"`) {
		t.Fatalf("binding form with fixed values:\n%s", b)
	}
	wantStatus(t, alice.get("/dflt/admin/bindings/new?subject=group:999999"), http.StatusNotFound)
	wantStatus(t, alice.get("/dflt/admin/bindings/new?space=nothere0"), http.StatusNotFound)
	// A built-in role has no delete page.
	wantStatus(t, alice.get("/dflt/admin/roles/"+strconv.FormatInt(viewerID, 10)+"/delete"), http.StatusNotFound)

	// The list of new members does not show members.
	wantStatus(t, alice.post(groupPath+"/members", url.Values{"user_id": {strconv.FormatInt(carolID, 10)}}), http.StatusSeeOther)
	if b := readBody(t, alice.get(groupPath+"/members/new")); strings.Contains(b, ">carol") {
		t.Fatalf("member form shows a member:\n%s", b)
	}
	// The members page has a remove button for each member.
	if b := readBody(t, alice.get(groupPath)); !strings.Contains(b, "/members/"+strconv.FormatInt(carolID, 10)+"/remove") {
		t.Fatalf("members page has no remove button:\n%s", b)
	}
	// The list of groups shows the source as text.
	if b := readBody(t, alice.get("/dflt/admin/groups")); !strings.Contains(b, "<td>OpenID Connect</td>") || !strings.Contains(b, "<td>Local</td>") {
		t.Fatalf("groups page does not show the source as text:\n%s", b)
	}
	// An OIDC group has no member form and no delete page.
	groups, _ := e.st.Groups(ctx, e.dft.ID)
	for _, g := range groups {
		if g.Source == "oidc" {
			p := "/dflt/admin/groups/" + strconv.FormatInt(g.ID, 10)
			wantStatus(t, alice.get(p+"/members/new"), http.StatusBadRequest)
			wantStatus(t, alice.get(p+"/delete"), http.StatusBadRequest)
			break
		}
	}
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
	if b := readBody(t, root.get("/admin/tenants/acme/spaces")); !strings.Contains(b, "acmedocs") {
		t.Fatalf("spaces page of the tenant:\n%s", b)
	}
	for _, p := range []string{"/admin/tenants/new", "/admin/tenants/acme/edit", "/admin/tenants/acme/spaces/new", "/admin/tenants/acme/delete"} {
		wantStatus(t, root.get(p), http.StatusOK)
	}
	// The default tenant has no delete page.
	wantStatus(t, root.get("/admin/tenants/dflt/delete"), http.StatusBadRequest)

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
		if ck.Name == "skriva_t"+strconv.FormatInt(acme.ID, 10) {
			acmeToken = ck.Value
		}
	}
	if acmeToken == "" {
		t.Fatal("no session cookie for tenant acme")
	}
	other := e.anon()
	other.http.Jar.SetCookies(u, []*http.Cookie{{Name: "skriva_t" + strconv.FormatInt(e.dft.ID, 10), Value: acmeToken, Path: "/"}})
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
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/default-role", url.Values{"default_role": {"bad role"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/default-role", url.Values{"default_role": {"nothere"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/default-role", url.Values{"default_role": {""}}), http.StatusSeeOther)
	resp := alice.post("/dflt/secret00/new", url.Values{"kind": {"document"}, "title": {"Plans"}, "body": {docBody("walrus")}, "action": {"save"}})
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
		"role": {"editor"}, "subject": {subject}, "space": {"Bad!"},
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
	if b := readBody(t, alice.get("/dflt/admin/users/"+strconv.FormatInt(carolID, 10)+"/permissions")); !strings.Contains(b, "<code>doc.edit</code>") {
		t.Fatalf("user page does not show the space permissions:\n%s", b)
	}

	// The admin role for one space does not give tenant administration.
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"admin"}, "subject": {subject}, "space": {"secret00"},
	}), http.StatusSeeOther)
	wantStatus(t, carol.post("/dflt/secret00/delete/"+doc[strings.Index(doc, "/d/")+3:][:store.PublicIDLength], nil), http.StatusSeeOther)
	wantStatus(t, carol.get("/dflt/admin/users"), http.StatusForbidden)
	wantStatus(t, carol.get("/dflt/admin/spaces"), http.StatusForbidden)
	if b := readBody(t, carol.get("/dflt/secret00/")); strings.Contains(b, "Administration") {
		t.Fatalf("space admin sees the administration link:\n%s", b)
	}

	// "Same as the tenant" gives the tenant default role back.
	wantStatus(t, alice.post("/dflt/admin/spaces/secret00/default-role", url.Values{"default_role": {"-"}}), http.StatusSeeOther)
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
	wantStatus(t, alice.get(sp+"/access/"+salaries), http.StatusOK)
	wantStatus(t, alice.post(sp+"/access/"+hr+"/default-role", url.Values{"default_role": {""}}), http.StatusSeeOther)
	wantStatus(t, alice.get(sp+"/access/"+hr), http.StatusOK)

	for _, p := range []string{"/d/" + hr, "/d/" + salaries, "/history/" + salaries, "/export/" + salaries, "/export/" + salaries + "/markdown", "/edit/" + salaries} {
		wantStatus(t, carol.get(sp+p), http.StatusNotFound)
	}
	wantStatus(t, carol.get(sp+"/d/"+public+"/public"), http.StatusOK)
	// The page contains a random CSRF token. Thus the test looks for the
	// title as the text of an element, not for the letters "HR".
	if b := readBody(t, carol.get(sp+"/")); strings.Contains(b, ">HR<") || strings.Contains(b, "Salaries") {
		t.Fatalf("tree of carol shows the private folder:\n%s", b)
	}
	if b := readBody(t, carol.get(sp+"/search?q=payroll")); strings.Contains(b, "Salaries") {
		t.Fatalf("space search of carol finds the private document:\n%s", b)
	}
	if b := readBody(t, carol.get("/dflt/search?q=payroll")); strings.Contains(b, "Salaries") {
		t.Fatalf("tenant search of carol finds the private document:\n%s", b)
	}
	wantStatus(t, carol.get(sp+"/w/salaries"), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/d/"+salaries+"/salaries"), http.StatusOK)

	// Give carol the editor role for the folder HR only.
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {"00000000000000"},
	}), http.StatusNotFound)
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {hr},
		"return_to": {sp + "/access/" + hr},
	}), http.StatusSeeOther)
	if b := readBody(t, alice.get(sp+"/access/"+hr)); !strings.Contains(b, "carol") {
		t.Fatalf("access page does not show the binding:\n%s", b)
	}
	if b := readBody(t, alice.get("/dflt/admin/bindings")); !strings.Contains(b, `Item "HR"`) {
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
	if b := selectList(readBody(t, carol.get(sp+"/move/"+child))); strings.Contains(b, "Top of the space") || strings.Contains(b, "Bob folder") {
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
	wantStatus(t, alice.post(sp+"/access/"+hr+"/default-role", url.Values{"default_role": {"maintainer"}}), http.StatusSeeOther)
	wantStatus(t, alice.post(sp+"/access/"+inner+"/default-role", url.Values{"default_role": {""}}), http.StatusSeeOther)
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

func TestDocumentPermissions(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	carol := e.login("carol")
	dave := e.login("dave")
	users, _ := e.st.Users(ctx, e.dft.ID)
	ids := map[string]string{}
	for _, u := range users {
		ids[u.Username] = "user:" + strconv.FormatInt(u.ID, 10)
	}

	guide := alice.create("folder", "", "Guide", "")
	plan := alice.create("document", guide, "Plan", "secret plan")
	detail := alice.create("document", plan, "Detail", "secret detail")
	other := alice.create("document", guide, "Other", "x")

	// The default is "Same as the parent": carol can read the document.
	wantStatus(t, carol.get(sp+"/d/"+plan+"/plan"), http.StatusOK)
	if b := readBody(t, alice.get(sp+"/access/"+plan+"/default-role")); !strings.Contains(b, `value="-" selected`) {
		t.Fatalf("access page of a document does not show the default:\n%s", b)
	}

	// A private document. Its sub-documents inherit the setting.
	wantStatus(t, alice.post(sp+"/access/"+plan+"/default-role", url.Values{"default_role": {""}}), http.StatusSeeOther)
	wantStatus(t, carol.get(sp+"/d/"+plan+"/plan"), http.StatusNotFound)
	wantStatus(t, carol.get(sp+"/d/"+detail+"/detail"), http.StatusNotFound)
	wantStatus(t, carol.get(sp+"/d/"+other+"/other"), http.StatusOK)
	if b := readBody(t, carol.get(sp+"/d/"+guide+"/guide")); strings.Contains(b, "Plan") || strings.Contains(b, "Detail") {
		t.Fatalf("folder page of carol shows the private document:\n%s", b)
	}

	// A binding for carol on the document gives access to the document and
	// its sub-documents only.
	wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
		"role": {"editor"}, "subject": {ids["carol"]}, "space": {"default0"}, "node": {plan},
		"return_to": {sp + "/access/" + plan},
	}), http.StatusSeeOther)
	wantStatus(t, carol.get(sp+"/d/"+detail+"/detail"), http.StatusOK)
	wantStatus(t, carol.save(plan, "Plan", "changed", 1), http.StatusSeeOther)
	wantStatus(t, carol.save(other, "Other", "changed", 1), http.StatusForbidden)
	wantStatus(t, dave.get(sp+"/d/"+plan+"/plan"), http.StatusNotFound)

	// "Same as the parent" gives the access of the folder back.
	wantStatus(t, alice.post(sp+"/access/"+plan+"/default-role", url.Values{"default_role": {"-"}}), http.StatusSeeOther)
	wantStatus(t, dave.get(sp+"/d/"+detail+"/detail"), http.StatusOK)
}

func TestLargeSpaceTree(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	// Make a space with more nodes than the full tree limit. The store is
	// faster than the HTTP interface for this.
	a := alice.create("folder", "", "Branch A", "")
	b := alice.create("folder", "", "Branch B", "")
	deep := alice.create("folder", a, "Deep A", "")
	leaf := alice.create("document", deep, "Leaf", "x")
	alice.create("document", b, "Only in B", "x")
	for i := range store.FullTreeLimit {
		if _, err := e.st.CreateNode(ctx, e.sp.ID, store.NewNode{
			ParentPublicID: b, Kind: store.KindDocument, Title: "Filler " + strconv.Itoa(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	large, err := e.st.IsLargeSpace(ctx, e.sp.ID)
	if err != nil || !large {
		t.Fatalf("IsLargeSpace = %v, %v", large, err)
	}

	// The page of the leaf shows its path, but not the content of branch B.
	body := readBody(t, alice.get(sp+"/d/"+leaf+"/leaf"))
	for _, want := range []string{"Branch A", "Branch B", "Deep A", "Leaf"} {
		if !strings.Contains(body, want) {
			t.Fatalf("tree does not show %q", want)
		}
	}
	if strings.Contains(body, "Only in B") || strings.Contains(body, "Filler") {
		t.Fatal("tree of a large space shows a different branch")
	}
	// The page of branch B shows its children.
	if page := readBody(t, alice.get(sp+"/d/"+b+"/branch-b")); !strings.Contains(page, "Only in B") {
		t.Fatal("folder page does not show its children")
	}
	wantStatus(t, alice.get(sp+"/"), http.StatusOK)
}

// upload sends a file to the upload URL and returns the response.
func (c *client) upload(path, name string, data []byte) *http.Response {
	c.e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf_token", c.csrf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	return resp
}

// png is the start of a PNG file. The server finds the type from it.
var png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func TestUploads(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	carol := e.login("carol")
	id := bob.create("document", "", "Pictures", "x")

	// A reader cannot upload. An editor can.
	wantStatus(t, carol.upload(sp+"/files?node="+id, "a.png", png), http.StatusForbidden)
	wantStatus(t, bob.upload(sp+"/files?node=zzzzzzzzzzzzzz", "a.png", png), http.StatusNotFound)
	wantStatus(t, bob.upload(sp+"/files?node="+id, "a.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)), http.StatusUnsupportedMediaType)
	wantStatus(t, bob.upload(sp+"/files?node="+id, "a.png", append(png, make([]byte, maxUploadBytes)...)), http.StatusRequestEntityTooLarge)
	resp := bob.upload(sp+"/files?node="+id, "a.png", png)
	wantStatus(t, resp, http.StatusOK)
	var out struct{ URL string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !regexp.MustCompile(`^` + sp + `/files/[0-9a-z]{14}$`).MatchString(out.URL) {
		t.Fatalf("upload URL = %q", out.URL)
	}

	// Before a document uses the file, only the user that uploaded it
	// can read it.
	resp = bob.get(out.URL)
	wantStatus(t, resp, http.StatusOK)
	if ct, csp := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Security-Policy"); ct != "image/png" || !strings.Contains(csp, "sandbox") {
		t.Fatalf("file headers: %q, %q", ct, csp)
	}
	resp.Body.Close()
	wantStatus(t, carol.get(out.URL), http.StatusNotFound)

	// The document saves the file as a "file:" reference. The page
	// shows the URL. Then all readers of the document can read the file.
	body := `{"type":"doc","content":[{"type":"image","attrs":{"src":"` + out.URL + `","alt":"A"}}]}`
	wantStatus(t, bob.save(id, "Pictures", body, 1), http.StatusSeeOther)
	page := readBody(t, carol.get(sp+"/d/"+id+"/pictures"))
	if !strings.Contains(page, `<img src="`+out.URL+`" alt="A"`) {
		t.Fatalf("document does not show the image:\n%s", page)
	}
	wantStatus(t, carol.get(out.URL), http.StatusOK)
	if md := readBody(t, carol.get(sp+"/export/"+id+"/markdown/text")); !strings.HasSuffix(md, "\n\n![A](http://example.test"+out.URL+")\n") {
		t.Fatalf("export = %q", md)
	}
	// The HTML export contains the image. A user that cannot read the
	// document cannot export it.
	resp = carol.get(sp + "/export/" + id + "/html")
	wantStatus(t, resp, http.StatusOK)
	if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); ct != "text/html; charset=utf-8" ||
		cd != "attachment; filename=pictures.html" {
		t.Fatalf("headers: %q, %q", ct, cd)
	}
	if h := readBody(t, resp); !strings.Contains(h, `<img src="data:image/png;base64,`+base64.StdEncoding.EncodeToString(png)+`" alt="A"`) {
		t.Fatalf("HTML export:\n%s", h)
	}
	resp = carol.get(sp + "/export/" + id + "/html/text")
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("text view has the type %q", ct)
	}

	// An upload for a new document checks the parent.
	wantStatus(t, carol.upload(sp+"/files?parent="+id, "a.png", png), http.StatusForbidden)
	wantStatus(t, bob.upload(sp+"/files?parent="+id, "a.png", png), http.StatusOK)
	wantStatus(t, bob.upload(sp+"/files", "a.png", png), http.StatusOK)
}

// linkBody returns a document with one paragraph that links to href.
func linkBody(text, href string) string {
	return `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + text +
		`","marks":[{"type":"link","attrs":{"href":"` + href + `"}}]}]}]}`
}

func TestLinksBetweenDocuments(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	carol := e.login("carol")

	target := alice.create("document", "", "Install guide", "text")
	byID := alice.create("document", "", "Setup", linkBody("Install", "doc:"+target+"#Step 2"))
	byTitle := alice.create("document", "", "Notes", linkBody("guide", "wiki:Install guide"))
	private := alice.create("document", "", "Private plan", linkBody("x", "doc:"+target))
	wantStatus(t, alice.post(sp+"/access/"+private+"/default-role", url.Values{"default_role": {""}}), http.StatusSeeOther)

	// A "doc:" link goes to the short link of the document.
	if b := readBody(t, alice.get(sp+"/d/"+byID+"/setup")); !strings.Contains(b, `href="`+sp+`/d/`+target+`#step-2"`) {
		t.Fatalf("document link not rendered:\n%s", b)
	}

	// The page of the target shows the pages that link to it. Carol cannot
	// see the private page.
	// backlinks returns the section with the links to the page. The tree
	// of the page also contains links to all pages.
	backlinks := func(page string) string {
		i := strings.Index(page, `aria-label="Links to this page"`)
		if i < 0 {
			return ""
		}
		return page[i : i+strings.Index(page[i:], "</section>")]
	}
	b := backlinks(readBody(t, carol.get(sp+"/d/"+target+"/install-guide")))
	if !strings.Contains(b, "Links to this page") || !strings.Contains(b, byID) || !strings.Contains(b, byTitle) || strings.Contains(b, private) {
		t.Fatalf("backlinks of carol:\n%s", b)
	}

	// After a rename, the "doc:" link stays. The wiki link uses the old
	// title, thus it is not a backlink now.
	wantStatus(t, alice.save(target, "Installation", "text", 1), http.StatusSeeOther)
	b = backlinks(readBody(t, carol.get(sp+"/d/"+target+"/installation")))
	if !strings.Contains(b, byID) || strings.Contains(b, byTitle) {
		t.Fatalf("backlinks after rename:\n%s", b)
	}
	// A source in the trash is not a backlink.
	wantStatus(t, alice.post(sp+"/delete/"+byID, nil), http.StatusSeeOther)
	if b := readBody(t, carol.get(sp+"/d/"+target+"/installation")); strings.Contains(b, "Links to this page") {
		t.Fatalf("backlink from the trash:\n%s", b)
	}

	// The search for the link bar finds readable pages only.
	var got []linkTarget
	resp := carol.get(sp + "/link-targets?q=pla")
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil || len(got) != 0 {
		t.Fatalf("carol finds %v (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(readBody(t, alice.get(sp+"/link-targets?q=PLA"))), &got); err != nil ||
		len(got) != 1 || got[0].ID != private || got[0].Title != "Private plan" {
		t.Fatalf("alice finds %v (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(readBody(t, alice.get(sp+"/link-targets?q=%25"))), &got); err != nil || len(got) != 0 {
		t.Fatalf("a percent sign matches %v (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(readBody(t, carol.get(sp+"/link-targets?id="+target))), &got); err != nil ||
		len(got) != 1 || got[0].Title != "Installation" {
		t.Fatalf("lookup by ID: %v (%v)", got, err)
	}
	if err := json.Unmarshal([]byte(readBody(t, carol.get(sp+"/link-targets?id="+private))), &got); err != nil || len(got) != 0 {
		t.Fatalf("carol finds the private page by ID: %v (%v)", got, err)
	}
	wantStatus(t, e.anon().get(sp+"/link-targets?q=a"), http.StatusSeeOther)
}

func TestCompareVersions(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	id := bob.create("document", "", "Plan", "first text")
	wantStatus(t, bob.save(id, "Plan B", "second text", 1), http.StatusSeeOther)

	b := readBody(t, bob.get(sp+"/history/"+id))
	if !strings.Contains(b, sp+"/compare/"+id+"?from=1&amp;to=2") || !strings.Contains(b, `class="compareform"`) {
		t.Fatalf("history page:\n%s", b)
	}
	// The default is the change in the current version.
	b = readBody(t, bob.get(sp+"/compare/"+id))
	for _, want := range []string{
		"Compare versions 1 and 2", `<mark>first</mark> text`, `<mark>second</mark> text`,
		`Title: Plan<mark> B</mark>`,
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("compare page does not contain %q:\n%s", want, b)
		}
	}
	wantStatus(t, bob.get(sp+"/compare/"+id+"?from=2&to=1"), http.StatusOK)
	wantStatus(t, bob.get(sp+"/compare/"+id+"?from=1&to=1"), http.StatusBadRequest)
	wantStatus(t, bob.get(sp+"/compare/"+id+"?from=1&to=9"), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/compare/"+id+"?from=x"), http.StatusBadRequest)
	// Version 1 has no earlier version.
	wantStatus(t, bob.get(sp+"/compare/"+id+"?to=1"), http.StatusBadRequest)
}

func TestAuditLog(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	bob := e.login("bob")
	mary := e.login("mary")

	folder := alice.create("folder", "", "Secret folder", "")
	id := bob.create("document", "", "Plan", "one")
	wantStatus(t, bob.save(id, "Plan", "two", 1), http.StatusSeeOther)
	// A restore of version 1 is an event. A normal save is not.
	wantStatus(t, bob.post(sp+"/edit/"+id, url.Values{
		"title": {"Plan"}, "body": {docBody("one")}, "version": {"2"}, "restored_from": {"1"},
	}), http.StatusSeeOther)
	wantStatus(t, bob.post(sp+"/edit/"+id, url.Values{
		"title": {"Plan"}, "body": {docBody("x")}, "version": {"3"}, "restored_from": {"7"},
	}), http.StatusBadRequest)
	wantStatus(t, bob.post(sp+"/move/"+id, url.Values{"parent": {folder}}), http.StatusSeeOther)
	wantStatus(t, alice.post(sp+"/access/"+folder+"/default-role", url.Values{"default_role": {""}}), http.StatusSeeOther)
	wantStatus(t, mary.post(sp+"/delete/"+folder, nil), http.StatusSeeOther)
	wantStatus(t, mary.post(sp+"/trash/"+folder+"/restore", nil), http.StatusSeeOther)

	wantStatus(t, bob.get("/dflt/admin/audit"), http.StatusForbidden)
	b := readBody(t, alice.get("/dflt/admin/audit"))
	for _, want := range []string{
		"Signed in", "Restored an old version", "Version 1 restored as version 3.",
		"Moved an item", `Into &#34;Secret folder&#34; (` + folder + `).`,
		"Changed the default role of an item", "No default role",
		"Deleted an item (to the trash)", "With 1 item in it.", "Restored an item from the trash",
		"<td>bob</td>", "Space <code>default0</code>",
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("audit log does not contain %q:\n%s", want, b)
		}
	}
	if strings.Count(b, "Restored an old version") != 1 {
		t.Fatalf("a normal save is in the audit log:\n%s", b)
	}

	// The system log has the events of the system administration only.
	root := e.loginAt("/admin", "root")
	wantStatus(t, root.post("/admin/tenants", url.Values{
		"code": {"acme"}, "name": {"Acme"}, "oidc_issuer": {"https://idp.example"},
		"oidc_client_id": {"acme"}, "oidc_scopes": {"openid"}, "oidc_groups_claim": {"groups"},
		"default_role": {"viewer"}, "session_ttl": {"8h"}, "max_doc_kib": {"512"},
	}), http.StatusSeeOther)
	b = readBody(t, root.get("/admin/audit"))
	if !strings.Contains(b, "Made a tenant") || !strings.Contains(b, "acme") || strings.Contains(b, "Moved an item") {
		t.Fatalf("system audit log:\n%s", b)
	}
	if b := readBody(t, alice.get("/dflt/admin/audit")); strings.Contains(b, "Made a tenant") {
		t.Fatalf("tenant log shows a system event:\n%s", b)
	}

	// Older events are on the next page.
	for i := range auditPageSize {
		if _, err := e.st.CreateLocalGroup(ctx, e.dft.ID, fmt.Sprint("group ", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	b = readBody(t, alice.get("/dflt/admin/audit"))
	if strings.Contains(b, "Moved an item") {
		t.Fatalf("first page shows old events:\n%s", b)
	}
	m := regexp.MustCompile(`href="(/dflt/admin/audit\?before=\d+)"`).FindStringSubmatch(b)
	if m == nil {
		t.Fatalf("no link to older events:\n%s", b)
	}
	if b := readBody(t, alice.get(m[1])); !strings.Contains(b, "Moved an item") {
		t.Fatalf("older events:\n%s", b)
	}
}

func TestToolbarModes(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	id := bob.create("document", "", "Plan", "text")
	// toolbar returns the toolbar of the bar at the top.
	toolbar := func(page string) string {
		i := strings.Index(page, `<nav class="toolbar"`)
		if i < 0 {
			return ""
		}
		return page[i : i+strings.Index(page[i:], "</nav>")]
	}
	// The form of a new document shows the same buttons, but disabled.
	// The Save button comes first.
	b := toolbar(readBody(t, bob.get(sp+"/new?kind=document")))
	save, edit := strings.Index(b, `class="save"`), strings.Index(b, `aria-label="New document (close)"`)
	if save < 0 || edit < 0 || save > edit || !strings.Contains(b, `class="tool disabled" role="link" aria-disabled="true" title="History (not available)"`) {
		t.Fatalf("toolbar of a new document:\n%s", b)
	}
	// The edit form has the same buttons, with links. Bob is an editor:
	// Delete and Access are disabled.
	b = toolbar(readBody(t, bob.get(sp+"/edit/"+id)))
	if save, edit := strings.Index(b, `class="save"`), strings.Index(b, `aria-label="Edit (close)"`); save < 0 || save > edit ||
		!strings.Contains(b, sp+"/history/"+id) || strings.Contains(b, sp+"/delete/") ||
		!strings.Contains(b, `title="Delete (not available)"`) || !strings.Contains(b, `title="Access (not available)"`) {
		t.Fatalf("toolbar of the edit form:\n%s", b)
	}
	// A reader sees the same buttons. Edit and Move are disabled.
	carol := e.login("carol")
	b = toolbar(readBody(t, carol.get(sp+"/d/"+id+"/plan")))
	if !strings.Contains(b, `title="Edit (not available)"`) || !strings.Contains(b, `title="Move (not available)"`) ||
		!strings.Contains(b, sp+"/history/"+id) || strings.Contains(b, sp+"/edit/") {
		t.Fatalf("toolbar of a reader:\n%s", b)
	}
	// A folder has the same buttons. History and Export are disabled.
	folder := bob.create("folder", "", "Box", "")
	b = toolbar(readBody(t, bob.get(sp+"/d/"+folder+"/box")))
	if !strings.Contains(b, `title="History (not available)"`) || !strings.Contains(b, `title="Export (not available)"`) ||
		!strings.Contains(b, `aria-label="Rename"`) {
		t.Fatalf("toolbar of a folder:\n%s", b)
	}
	// The space page shows the Trash button disabled for an editor.
	if b := toolbar(readBody(t, bob.get(sp+"/"))); !strings.Contains(b, `title="Trash (not available)"`) ||
		!strings.Contains(b, `title="Access (not available)"`) {
		t.Fatalf("toolbar of the space page:\n%s", b)
	}
	// The page has no Save button.
	if b = toolbar(readBody(t, bob.get(sp+"/d/"+id+"/plan"))); strings.Contains(b, `class="save"`) {
		t.Fatalf("toolbar of the page:\n%s", b)
	}
}

func TestAdminMenuShowsAllItems(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	wantStatus(t, alice.post("/dflt/admin/roles", url.Values{"name": {"spaces"}, "permission": {"doc.read", "space.manage"}}), http.StatusSeeOther)
	dave := e.login("dave")
	users, _ := e.st.Users(ctx, e.dft.ID)
	for _, u := range users {
		if u.Username == "dave" {
			wantStatus(t, alice.post("/dflt/admin/bindings", url.Values{
				"role": {"spaces"}, "subject": {"user:" + strconv.FormatInt(u.ID, 10)},
			}), http.StatusSeeOther)
		}
	}
	// Dave can manage spaces only. The menu shows all items, and the
	// items of the tenant administration are disabled.
	b := readBody(t, dave.get("/dflt/admin/spaces"))
	i := strings.Index(b, `<nav class="menu">`)
	menu := b[i : i+strings.Index(b[i:], "</nav>")]
	for _, want := range []string{
		`title="Users (not available)"`, `title="Audit log (not available)"`,
		`href="/dflt/admin/spaces" aria-current="page"`,
	} {
		if !strings.Contains(menu, want) {
			t.Fatalf("menu does not contain %q:\n%s", want, menu)
		}
	}
	if strings.Contains(menu, `href="/dflt/admin/users"`) {
		t.Fatalf("menu has a link to the users:\n%s", menu)
	}
}

// TestExport checks the export page and the Markdown export.
func TestExport(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	folder := bob.create("folder", "", "Guides", "")
	target := bob.create("document", folder, "Start", "")
	body := `{"type":"doc","content":[` +
		`{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Install"}]},` +
		`{"type":"paragraph","content":[` +
		`{"type":"text","text":"See ","marks":[]},` +
		`{"type":"text","text":"Start","marks":[{"type":"link","attrs":{"href":"wiki:Start"}}]},` +
		`{"type":"text","text":" and "},` +
		`{"type":"text","text":"this","marks":[{"type":"link","attrs":{"href":"doc:` + target + `"}}]}]}]}`
	id := bob.create("document", folder, `Plan "A" # 1`, body)

	// The page shows the formats. The Export button is the current tab.
	b := readBody(t, bob.get(sp+"/export/"+id))
	for _, want := range []string{
		`<h1>Export "Plan &#34;A&#34; # 1"</h1>`,
		sp + "/export/" + id + "/markdown/text",
		sp + "/export/" + id + "/markdown\"",
		"Export (close)",
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("export page does not contain %q:\n%s", want, b)
		}
	}

	resp := bob.get(sp + "/export/" + id + "/markdown")
	wantStatus(t, resp, http.StatusOK)
	if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); ct != "text/markdown; charset=utf-8" ||
		cd != `attachment; filename=plan-a-1.md` {
		t.Fatalf("headers: %q, %q", ct, cd)
	}
	got := readBody(t, resp)
	site := "http://example.test"
	wantPrefix := "---\ntitle: \"Plan \\\"A\\\" # 1\"\nclassification: \"Unclassified\"\nversion: 1\nupdated: \""
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("front matter:\n%s", got)
	}
	for _, want := range []string{
		"\nsource: \"" + site + sp + "/d/" + id + "/plan-a-1\"\n---\n\n# Plan \"A\" \\# 1\n\n",
		"\n---\n\n# Plan \"A\" \\# 1\n\n## Install\n\n",
		"See [Start](" + site + sp + "/w/start) and [this](" + site + sp + "/d/" + target + ")\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("export does not contain %q:\n%s", want, got)
		}
	}
	if text := readBody(t, bob.get(sp+"/export/"+id+"/markdown/text")); text != got {
		t.Fatalf("text = %q, want %q", text, got)
	}

	// The PDF export needs the Typst program.
	resp = bob.get(sp + "/export/" + id + "/pdf")
	if os.Getenv("SKRIVA_TEST_TYPST") == "" {
		wantStatus(t, resp, http.StatusNotFound)
		if b := readBody(t, bob.get(sp+"/export/"+id)); !strings.Contains(b, "cannot make PDF files") ||
			strings.Contains(b, sp+"/export/"+id+"/pdf") {
			t.Fatalf("export page without Typst:\n%s", b)
		}
	} else {
		wantStatus(t, resp, http.StatusOK)
		if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); ct != "application/pdf" ||
			cd != "attachment; filename=plan-a-1.pdf" {
			t.Fatalf("headers: %q, %q", ct, cd)
		}
		if b := readBody(t, resp); !strings.HasPrefix(b, "%PDF-") {
			t.Fatalf("PDF export: %.100q", b)
		}
		// A PDF file is not text.
		wantStatus(t, bob.get(sp+"/export/"+id+"/pdf/text"), http.StatusNotFound)
	}

	// A folder and an unknown format have no export.
	wantStatus(t, bob.get(sp+"/export/"+folder), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/export/"+folder+"/markdown"), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/export/"+id+"/docx"), http.StatusNotFound)
	// The Export button of a folder is disabled.
	if b := readBody(t, bob.get(sp+"/d/"+folder+"/guides")); strings.Contains(b, sp+"/export/"+folder) {
		t.Fatalf("folder page links to the export:\n%s", b)
	}
}

func TestClassification(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	bob := e.login("bob")
	levels, _ := e.st.Classifications(ctx, e.dft.ID)
	byName := map[string]string{}
	for _, c := range levels {
		byName[c.Name] = strconv.FormatInt(c.ID, 10)
	}

	// The form of a new document selects the default level, and the bar
	// shows it.
	b := readBody(t, bob.get(sp+"/new?kind=document"))
	if !strings.Contains(b, `<option value="`+byName["Unclassified"]+`" selected>Unclassified</option>`) ||
		!strings.Contains(b, `This document is classified as <strong data-classbar-level>UNCLASSIFIED</strong>`) {
		t.Fatalf("form of a new document:\n%s", b)
	}
	// A folder has no level.
	if b := readBody(t, bob.get(sp+"/new?kind=folder")); strings.Contains(b, "classbar") || strings.Contains(b, `name="classification"`) {
		t.Fatalf("form of a new folder:\n%s", b)
	}

	resp := bob.post(sp+"/new", url.Values{
		"kind": {"document"}, "title": {"Plan"}, "body": {docBody("x")}, "classification": {byName["Top Secret"]},
	})
	wantStatus(t, resp, http.StatusSeeOther)
	b = readBody(t, bob.get(resp.Header.Get("Location")))
	if !strings.Contains(b, `This document is classified as <strong data-classbar-level>TOP SECRET</strong>`) {
		t.Fatalf("page of the document:\n%s", b)
	}
	id := strings.Split(resp.Header.Get("Location"), "/")[4]
	// The tables of items show the classification and the last author.
	// A folder has no classification.
	folder := bob.create("folder", "", "Box", "")
	bob.create("document", folder, "Note", "x")
	row := func(page, title string) string {
		i := strings.Index(page, ">"+title+"</a></td>")
		j := strings.Index(page[max(i, 0):], "</tr>")
		if i < 0 || j < 0 {
			t.Fatalf("no row for %q:\n%s", title, page)
		}
		return page[i : i+j]
	}
	cells := regexp.MustCompile(`<td[^>]*>([^<]*)</td>`)
	check := func(r string, want ...string) {
		t.Helper()
		var got []string
		for _, m := range cells.FindAllStringSubmatch(r, -1) {
			got = append(got, m[1])
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("cells = %q, want %q in\n%s", got, want, r)
		}
	}
	home := readBody(t, bob.get(sp+"/"))
	check(row(home, "Plan"), "Document", "Top Secret", "bob")
	check(row(home, "Box"), "Folder", "", "bob")
	check(row(readBody(t, bob.get(sp+"/d/"+folder+"/box")), "Note"), "Document", "Unclassified", "bob")
	if b := readBody(t, bob.get(sp+"/history/"+id)); !strings.Contains(b, "TOP SECRET") {
		t.Fatalf("history page has no bar:\n%s", b)
	}
	// A level that does not exist is refused.
	wantStatus(t, bob.post(sp+"/edit/"+id, url.Values{
		"title": {"Plan"}, "body": {docBody("x")}, "version": {"1"}, "classification": {"999999"},
	}), http.StatusBadRequest)

	// The administration of the levels.
	wantStatus(t, bob.get("/dflt/admin/classifications"), http.StatusForbidden)
	b = readBody(t, alice.get("/dflt/admin/classifications"))
	if !strings.Contains(b, "Top Secret") || !strings.Contains(b, `title="Delete (not available)"`) {
		t.Fatalf("list of levels:\n%s", b)
	}
	wantStatus(t, alice.post("/dflt/admin/classifications", url.Values{"name": {""}, "position": {"9"}}), http.StatusUnprocessableEntity)
	wantStatus(t, alice.post("/dflt/admin/classifications", url.Values{"name": {"Public"}, "position": {"9"}}), http.StatusUnprocessableEntity)
	wantStatus(t, alice.post("/dflt/admin/classifications", url.Values{"name": {"Internal"}, "position": {"2"}, "default": {"on"}}), http.StatusSeeOther)
	if b := readBody(t, bob.get(sp+"/new?kind=document")); !strings.Contains(b, `selected>Internal</option>`) {
		t.Fatalf("new default is not selected:\n%s", b)
	}
	// A level in use and the default cannot be deleted.
	wantStatus(t, alice.post("/dflt/admin/classifications/"+byName["Top Secret"]+"/delete", nil), http.StatusConflict)
	wantStatus(t, alice.post("/dflt/admin/classifications/"+byName["Public"]+"/delete", nil), http.StatusSeeOther)
	wantStatus(t, alice.post("/dflt/admin/classifications/"+byName["Public"]+"/delete", nil), http.StatusNotFound)
	if b := readBody(t, alice.get("/dflt/admin/audit")); !strings.Contains(b, "Made a classification") || !strings.Contains(b, "Deleted a classification") {
		t.Fatalf("audit log:\n%s", b)
	}
}

func TestDrawioUpload(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	id := bob.create("document", "", "Flow", "x")
	sample, err := os.ReadFile("testdata/flow.drawio.svg")
	if err != nil {
		t.Fatal(err)
	}
	// A draw.io file without an image gets a message with the export
	// steps.
	resp := bob.upload(sp+"/files?node="+id, "flow.drawio", []byte(`<mxfile><diagram id="a">x</diagram></mxfile>`))
	if b := readBody(t, resp); resp.StatusCode != http.StatusUnsupportedMediaType || !strings.Contains(b, "Include a copy of my diagram") {
		t.Fatalf("status %d, message: %s", resp.StatusCode, b)
	}
	resp = bob.upload(sp+"/files?node="+id, "flow.drawio.svg", sample)
	wantStatus(t, resp, http.StatusOK)
	var out struct{ URL string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = bob.get(out.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" || !strings.Contains(csp, "sandbox") ||
		strings.Contains(csp, "script-src") || !strings.HasPrefix(csp, "default-src 'none'") {
		t.Fatalf("file headers: %q, %q", ct, csp)
	}
	if b := readBody(t, resp); b != string(sample) {
		t.Fatal("the server changed the file")
	}
	// The document shows the diagram as an image.
	body := `{"type":"doc","content":[{"type":"image","attrs":{"src":"` + out.URL + `","alt":"Flow"}}]}`
	wantStatus(t, bob.save(id, "Flow", body, 1), http.StatusSeeOther)
	if page := readBody(t, bob.get(sp+"/d/"+id+"/flow")); !strings.Contains(page, `<img src="`+out.URL+`" alt="Flow"`) {
		t.Fatalf("document does not show the diagram:\n%s", page)
	}
}

func TestTenantKeyRotation(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	id := bob.create("document", "", "Plan", "secret text")
	root := e.loginAt("/admin", "root")
	b := readBody(t, root.get("/admin/tenants/dflt"))
	if !strings.Contains(b, `id="encryption"`) || !strings.Contains(b, "Version 1") {
		t.Fatalf("tenant page:\n%s", b)
	}
	// Only a system administrator can make a new key.
	wantStatus(t, bob.post("/admin/tenants/dflt/rotate-key", nil), http.StatusUnauthorized)
	wantStatus(t, root.post("/admin/tenants/dflt/rotate-key", nil), http.StatusSeeOther)
	b = readBody(t, root.get("/admin/tenants/dflt"))
	if !strings.Contains(b, "Version 2") || !strings.Contains(b, "items still use an old key") {
		t.Fatalf("tenant page after rotation:\n%s", b)
	}
	// The document is still readable, and a save uses the new key.
	if b := readBody(t, bob.get(sp+"/d/"+id+"/plan")); !strings.Contains(b, "secret text") {
		t.Fatalf("document after rotation:\n%s", b)
	}
	wantStatus(t, bob.save(id, "Plan", "new text", 1), http.StatusSeeOther)
	if b := readBody(t, bob.get(sp+"/search?q=new")); !strings.Contains(b, "Plan") {
		t.Fatalf("search after rotation:\n%s", b)
	}
}

// TestChangeCodes changes the code of a tenant and the code of a space.
// The URLs change, and the sessions and the client secret stay valid.
func TestChangeCodes(t *testing.T) {
	e := newEnv(t)
	alice := e.login("alice")
	id := alice.create("document", "", "Plan", "text")

	// A space code with 2 characters and a code with "-".
	wantStatus(t, alice.post("/dflt/admin/spaces", url.Values{"name": {"HR"}, "slug": {"hr"}}), http.StatusSeeOther)
	wantStatus(t, alice.get("/dflt/hr/"), http.StatusOK)
	wantStatus(t, alice.post("/dflt/admin/spaces/hr", url.Values{"name": {"HR"}, "slug": {"default0"}}), http.StatusConflict)
	wantStatus(t, alice.post("/dflt/admin/spaces/hr", url.Values{"name": {"HR"}, "slug": {"admin"}}), http.StatusBadRequest)
	wantStatus(t, alice.post("/dflt/admin/spaces/default0", url.Values{"name": {"Main"}, "slug": {"main-docs"}}), http.StatusSeeOther)
	wantStatus(t, alice.get("/dflt/default0/"), http.StatusNotFound)
	if b := readBody(t, alice.get("/dflt/main-docs/d/"+id+"/plan")); !strings.Contains(b, "text") {
		t.Fatalf("document after the space code changed:\n%s", b)
	}
	// The tenant-level routes still work.
	wantStatus(t, alice.get("/dflt/search?q=plan"), http.StatusOK)

	// The code of the tenant.
	root := e.loginAt("/admin", "root")
	form := url.Values{
		"name": {"Default"}, "oidc_issuer": {e.dft.OIDCIssuer}, "oidc_client_id": {e.dft.OIDCClientID},
		"oidc_client_secret": {"s3cret"}, "oidc_scopes": {"openid"}, "oidc_groups_claim": {"groups"},
		"admin_group": {"admins"}, "default_role": {"viewer"}, "session_ttl": {"12h"}, "max_doc_kib": {"1024"},
	}
	for _, bad := range []string{"x", "admin", "static", "Main"} {
		form.Set("code", bad)
		wantStatus(t, root.post("/admin/tenants/dflt", form), http.StatusUnprocessableEntity)
	}
	form.Set("code", "main")
	wantStatus(t, root.post("/admin/tenants/dflt", form), http.StatusSeeOther)
	wantStatus(t, alice.get("/dflt/"), http.StatusNotFound)
	// The session cookie uses the ID of the tenant, thus alice is still
	// signed in.
	wantStatus(t, alice.get("/main/main-docs/d/"+id+"/plan"), http.StatusOK)
	tn, err := e.st.TenantByCode(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if s, err := e.st.ClientSecret(ctx, tn); err != nil || s != "s3cret" {
		t.Fatalf("client secret = %q, %v", s, err)
	}
	events, err := e.st.AuditEvents(ctx, nil, 0, 1)
	if err != nil || len(events) != 1 || !strings.Contains(events[0].Details, "from dflt to main") {
		t.Fatalf("audit = %+v, %v", events, err)
	}
}

func TestDiagrams(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	const good, bad = "flowchart LR\n  accTitle: Review\n  A[Write] --> B[Review]", "flowchart LR\n  A -->"
	code := func(src string) string {
		js, _ := json.Marshal(src)
		return `{"type":"codeBlock","attrs":{"language":"mermaid"},"content":[{"type":"text","text":` + string(js) + `}]}`
	}
	id := bob.create("document", "", "Flow", `{"type":"doc","content":[`+code(good)+`,`+code(bad)+`]}`)
	other := bob.create("document", "", "Other", "")

	// The page refers to the image of the good diagram, and shows the text
	// and the error of the bad diagram.
	img := sp + "/diagram/" + id + "/" + diagram.Key(good)
	page := readBody(t, bob.get(sp+"/d/"+id+"/flow"))
	for _, want := range []string{
		`<figure class="diagram"><img src="` + img + `" alt="Review"></figure>`,
		`<figure class="diagram-error"><pre><code class="language-mermaid">flowchart LR`,
		`<figcaption>The diagram has an error: invalid flowchart edge syntax: A`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page does not contain %q:\n%s", want, page)
		}
	}

	// The image is an SVG file in a sandbox, thus its links do not work.
	resp := bob.get(img)
	wantStatus(t, resp, http.StatusOK)
	h := resp.Header
	if h.Get("Content-Type") != "image/svg+xml" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") ||
		h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", h)
	}
	if b := readBody(t, resp); !strings.HasPrefix(b, "<svg") || !strings.Contains(b, "Write") ||
		!strings.Contains(b, `><style>@font-face { font-family: "Inter Variable"; font-weight: 100 900; src: url(data:font/woff2;base64,`) {
		t.Fatalf("image: %.300q", b)
	}
	// Only the diagrams of the document have images.
	wantStatus(t, bob.get(sp+"/diagram/"+other+"/"+diagram.Key(good)), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/diagram/"+id+"/"+diagram.Key(bad)), http.StatusNotFound)
	wantStatus(t, bob.get(sp+"/diagram/"+id+"/xyz"), http.StatusNotFound)
	if resp := e.anon().get(img); resp.StatusCode == http.StatusOK {
		t.Fatal("a user that is not signed in can read the image")
	}

	// An old version refers to the image of its own text.
	wantStatus(t, bob.save(id, "Flow", `{"type":"doc","content":[]}`, 1), http.StatusSeeOther)
	wantStatus(t, bob.get(img), http.StatusNotFound)
	old := readBody(t, bob.get(sp+"/history/"+id+"/1"))
	if !strings.Contains(old, img+"?v=1") {
		t.Fatalf("version 1 does not refer to the image:\n%s", old)
	}
	wantStatus(t, bob.get(img+"?v=1"), http.StatusOK)
	wantStatus(t, bob.get(img+"?v=2"), http.StatusNotFound)
	wantStatus(t, bob.get(img+"?v=x"), http.StatusNotFound)

	// The HTML export contains the image. The Markdown export contains
	// the text.
	wantStatus(t, bob.save(id, "Flow", `{"type":"doc","content":[`+code(good)+`]}`, 2), http.StatusSeeOther)
	if b := readBody(t, bob.get(sp+"/export/"+id+"/html")); !strings.Contains(b, `<img src="data:image/svg+xml;base64,`) {
		t.Fatalf("HTML export has no diagram:\n%s", b)
	}
	if b := readBody(t, bob.get(sp+"/export/"+id+"/markdown")); !strings.Contains(b, "```mermaid\nflowchart LR\n") {
		t.Fatalf("Markdown export has no diagram:\n%s", b)
	}
}

func TestWithFont(t *testing.T) {
	style := string(diagramFontStyle())
	for in, want := range map[string]string{
		`<svg a="1"><g/></svg>`: `<svg a="1">` + style + `<g/></svg>`,
		`<svg/>`:                `<svg/>`,
		`no image`:              `no image`,
	} {
		if got := string(withFont([]byte(in))); got != want {
			t.Errorf("withFont(%q) = %.80q", in, got)
		}
	}
}
