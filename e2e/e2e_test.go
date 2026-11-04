//go:build e2e

// Package e2e tests the complete system: the application, PostgreSQL and
// Keycloak. Start the system with "scripts/dev.sh app" first.
package e2e

import (
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	formActionRE = regexp.MustCompile(`<form[^>]+id="kc-form-login"[^>]+action="([^"]+)"`)
	csrfRE       = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	docIDRE      = regexp.MustCompile(`/d/([0-9a-z]{14})(/|$)`)
	refreshRE    = regexp.MustCompile(`<meta http-equiv="refresh" content="0; url=([^"]+)">`)
)

type browser struct {
	t    *testing.T
	base string
	c    *http.Client
	csrf string
}

func newBrowser(t *testing.T) *browser {
	base := os.Getenv("SKRIVA_E2E_URL")
	if base == "" {
		t.Skip("SKRIVA_E2E_URL is not set")
	}
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, c: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
}

func (b *browser) do(req *http.Request) (*http.Response, string) {
	b.t.Helper()
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if m := csrfRE.FindStringSubmatch(string(body)); m != nil {
		b.csrf = m[1]
	}
	return resp, string(body)
}

func (b *browser) get(path string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodGet, b.base+path, nil)
	return b.do(req)
}

func (b *browser) post(target string, form url.Values) (*http.Response, string) {
	if !strings.HasPrefix(target, "http") {
		target = b.base + target
		if _, ok := form["csrf_token"]; !ok {
			form.Set("csrf_token", b.csrf)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.do(req)
}

// login opens path and signs in through the Keycloak login form.
func (b *browser) login(path, user, password string) {
	b.t.Helper()
	_, body := b.get(path)
	m := formActionRE.FindStringSubmatch(body)
	if m == nil {
		b.t.Fatalf("no Keycloak login form:\n%s", body)
	}
	resp, body := b.post(html.UnescapeString(m[1]), url.Values{
		"username": {user}, "password": {password}, "credentialId": {""},
	})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != path {
		b.t.Fatalf("login ended at %s with status %d:\n%s", resp.Request.URL, resp.StatusCode, body)
	}
}

func wantCode(t *testing.T, resp *http.Response, body string, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d\n%s", resp.Request.Method, resp.Request.URL, resp.StatusCode, want, body)
	}
}

func TestTenantDocumentsAndRBAC(t *testing.T) {
	const sp = "/dflt/default0"
	stamp := time.Now().UTC().Format("150405")

	bob := newBrowser(t)
	bob.login(sp+"/", "bob", "bob")
	resp, body := bob.post(sp+"/new", url.Values{"kind": {"folder"}, "title": {"E2E folder " + stamp}, "action": {"save"}})
	wantCode(t, resp, body, http.StatusOK)
	m := docIDRE.FindStringSubmatch(resp.Request.URL.Path)
	if m == nil {
		t.Fatalf("folder URL %s", resp.Request.URL)
	}
	folder := m[1]
	resp, body = bob.post(sp+"/new", url.Values{"kind": {"document"}, "parent": {folder},
		"title": {"E2E doc " + stamp}, "action": {"save"},
		"body": {`{"type":"doc","content":[{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"From the e2e test"}]}]}`}})
	wantCode(t, resp, body, http.StatusOK)
	if !strings.Contains(body, `<h2 id="from-the-e2e-test" class="level-1">From the e2e test</h2>`) || !strings.Contains(body, "E2E folder "+stamp) {
		t.Fatalf("document page:\n%s", body)
	}
	doc := docIDRE.FindStringSubmatch(resp.Request.URL.Path)[1]

	carol := newBrowser(t)
	carol.login(sp+"/", "carol", "carol")
	resp, body = carol.get(sp + "/d/" + doc)
	wantCode(t, resp, body, http.StatusOK)
	resp, body = carol.post(sp+"/edit/"+doc, url.Values{"title": {"x"}, "body": {"x"}, "version": {"1"}})
	wantCode(t, resp, body, http.StatusForbidden)

	alice := newBrowser(t)
	alice.login("/dflt/", "alice", "alice")
	resp, body = alice.get("/dflt/admin/users")
	wantCode(t, resp, body, http.StatusOK)
	resp, body = alice.post(sp+"/delete/"+folder, url.Values{})
	wantCode(t, resp, body, http.StatusOK)
	resp, body = alice.get(sp + "/d/" + doc)
	wantCode(t, resp, body, http.StatusNotFound)

	// The logout page goes to Keycloak with a meta refresh. Keycloak
	// then goes back to the signed-out page.
	resp, body = alice.post("/dflt/logout", url.Values{})
	m = refreshRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the logout page has no refresh: %s", body)
	}
	req, _ := http.NewRequest(http.MethodGet, html.UnescapeString(m[1]), nil)
	if resp, _ = alice.do(req); resp.Request.URL.Path != "/dflt/signed-out" {
		t.Fatalf("logout ended at %s", resp.Request.URL)
	}
}

func TestSystemAdminAndSecondTenant(t *testing.T) {
	code := "e" + time.Now().UTC().Format("150405")[3:]

	// A tenant user cannot sign in to the system administration.
	alice := newBrowser(t)
	alice.login("/dflt/", "alice", "alice")
	_, body := alice.get("/admin/")
	if m := formActionRE.FindStringSubmatch(body); m != nil {
		t.Fatal("unexpected login form: Keycloak has no session")
	}
	if !strings.Contains(body, "does not have access") {
		t.Fatalf("alice in system administration:\n%s", body)
	}

	root := newBrowser(t)
	root.login("/admin/", "root", "root")
	resp, body := root.post("/admin/tenants", url.Values{
		"code": {code}, "name": {"E2E tenant"}, "oidc_issuer": {"http://localhost:8180/realms/skriva"},
		"oidc_client_id": {"skriva"}, "oidc_client_secret": {"skriva-dev-secret"},
		"oidc_scopes": {"openid profile email"}, "oidc_groups_claim": {"groups"},
		"admin_group": {"skriva-editors"}, "default_role": {""},
		"session_ttl": {"1h"}, "max_doc_kib": {"64"},
	})
	wantCode(t, resp, body, http.StatusOK)
	resp, body = root.post("/admin/tenants/"+code+"/spaces", url.Values{"name": {"E2E space"}, "slug": {"e2espace"}})
	wantCode(t, resp, body, http.StatusOK)

	// Bob is in skriva-editors, the admin group of the new tenant.
	bob := newBrowser(t)
	bob.login("/"+code+"/e2espace/", "bob", "bob")
	resp, body = bob.get("/" + code + "/admin/users")
	wantCode(t, resp, body, http.StatusOK)

	// Carol has no role in the new tenant: no default role.
	carol := newBrowser(t)
	_, body = carol.get("/" + code + "/")
	m := formActionRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no login form:\n%s", body)
	}
	resp, body = carol.post(html.UnescapeString(m[1]), url.Values{"username": {"carol"}, "password": {"carol"}})
	wantCode(t, resp, body, http.StatusOK)
	if strings.Contains(body, "e2espace") {
		t.Fatalf("carol sees a space without a role:\n%s", body)
	}
	resp, body = carol.get("/" + code + "/e2espace/")
	wantCode(t, resp, body, http.StatusForbidden)

	// The new tenant cannot see the default space.
	resp, body = bob.get("/" + code + "/default0/")
	wantCode(t, resp, body, http.StatusNotFound)

	resp, body = root.post("/admin/tenants/"+code+"/delete", url.Values{"confirm": {code}})
	wantCode(t, resp, body, http.StatusOK)
	resp, body = bob.get("/" + code + "/")
	wantCode(t, resp, body, http.StatusNotFound)
}
