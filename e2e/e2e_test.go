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
)

type browser struct {
	t    *testing.T
	base string
	c    *http.Client
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
	return resp, string(body)
}

func (b *browser) get(path string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodGet, b.base+path, nil)
	return b.do(req)
}

func (b *browser) post(target string, form url.Values) (*http.Response, string) {
	if !strings.HasPrefix(target, "http") {
		target = b.base + target
	}
	req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.do(req)
}

// login signs in through the Keycloak login form.
func (b *browser) login(user, password string) string {
	b.t.Helper()
	_, body := b.get("/wiki/")
	m := formActionRE.FindStringSubmatch(body)
	if m == nil {
		b.t.Fatalf("no Keycloak login form:\n%s", body)
	}
	resp, body := b.post(html.UnescapeString(m[1]), url.Values{
		"username": {user}, "password": {password}, "credentialId": {""},
	})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/wiki/" {
		b.t.Fatalf("login ended at %s with status %d:\n%s", resp.Request.URL, resp.StatusCode, body)
	}
	c := csrfRE.FindStringSubmatch(body)
	if c == nil {
		b.t.Fatalf("no CSRF token after login:\n%s", body)
	}
	return c[1]
}

func TestLoginEditAndRBAC(t *testing.T) {
	slug := "e2e-" + time.Now().UTC().Format("20060102150405")

	bob := newBrowser(t)
	csrf := bob.login("bob", "bob")
	resp, body := bob.post("/wiki/"+slug+"/edit", url.Values{
		"csrf_token": {csrf}, "title": {"E2E " + slug}, "body": {"# From the e2e test"},
		"version": {"0"}, "action": {"save"},
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "From the e2e test") {
		t.Fatalf("save failed with status %d:\n%s", resp.StatusCode, body)
	}

	// Carol has only the default role "viewer".
	carol := newBrowser(t)
	csrf = carol.login("carol", "carol")
	if resp, _ := carol.get("/wiki/" + slug); resp.StatusCode != http.StatusOK {
		t.Fatalf("carol cannot read the page: %d", resp.StatusCode)
	}
	resp, _ = carol.post("/wiki/"+slug+"/edit", url.Values{
		"csrf_token": {csrf}, "title": {"x"}, "body": {"x"}, "version": {"1"}, "action": {"save"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("carol can edit: %d", resp.StatusCode)
	}
	if resp, _ := carol.get("/admin/users"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("carol can open administration: %d", resp.StatusCode)
	}

	// Alice is in the group skriva-admins.
	alice := newBrowser(t)
	csrf = alice.login("alice", "alice")
	resp, body = alice.get("/admin/users")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "carol") {
		t.Fatalf("alice cannot see users: %d", resp.StatusCode)
	}
	resp, _ = alice.post("/wiki/"+slug+"/delete", url.Values{"csrf_token": {csrf}})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/wiki/" {
		t.Fatalf("delete failed: %d at %s", resp.StatusCode, resp.Request.URL)
	}

	// Logout goes through Keycloak and back to the signed-out page.
	resp, _ = alice.post("/logout", url.Values{"csrf_token": {csrf}})
	if resp.Request.URL.Path != "/signed-out" {
		t.Fatalf("logout ended at %s", resp.Request.URL)
	}
}
