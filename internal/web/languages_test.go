package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The language of a page is the language of the user, else of the
// tenant, else of the system.
func TestLanguages(t *testing.T) {
	e := newEnv(t)
	bob := e.login("bob")
	page := func(c *client, path string) string {
		t.Helper()
		resp := c.get(path)
		wantStatus(t, resp, http.StatusOK)
		return readBody(t, resp)
	}
	if body := page(bob, "/dflt/"); !strings.Contains(body, `<html lang="en">`) || !strings.Contains(body, ">Spaces<") {
		t.Fatalf("default language: %s", body)
	}

	// The profile.
	body := page(bob, "/dflt/profile")
	for _, want := range []string{"<h1>Profile</h1>", `name="language"`, `<option value="sv" lang="sv">Svenska</option>`,
		"The default (English)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("profile page has no %q:\n%s", want, body)
		}
	}
	resp := bob.post("/dflt/profile", url.Values{"language": {"sv"}})
	wantStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/dflt/profile?saved=1" {
		t.Fatalf("redirect to %q", loc)
	}
	body = page(bob, "/dflt/profile?saved=1")
	for _, want := range []string{`<html lang="sv">`, "<h1>Profil</h1>", "Profilen är sparad.",
		`<option value="sv" lang="sv" selected>Svenska</option>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("Swedish profile page has no %q:\n%s", want, body)
		}
	}
	if body := page(bob, "/dflt/"); !strings.Contains(body, ">Utrymmen<") {
		t.Fatalf("the start page is not in Swedish:\n%s", body)
	}
	// Other users keep the language of the tenant.
	alice := e.login("alice")
	if body := page(alice, "/dflt/"); !strings.Contains(body, `<html lang="en">`) {
		t.Fatal("the language of bob changed the page of alice")
	}
	resp = bob.post("/dflt/profile", url.Values{"language": {"fr"}})
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Språket är inte giltigt.") {
		t.Fatalf("error for a bad language:\n%s", body)
	}

	// The language of the tenant applies to users without a language.
	ts := e.dft.TenantSettings
	ts.Language = "sv"
	if _, err := e.st.UpdateTenant(ctx, e.dft.ID, e.dft.Code, ts, nil); err != nil {
		t.Fatal(err)
	}
	if body := page(alice, "/dflt/"); !strings.Contains(body, `<html lang="sv">`) {
		t.Fatal("the page of alice does not use the language of the tenant")
	}
	if body := page(alice, "/dflt/profile"); !strings.Contains(body, "Standard (Svenska)") {
		t.Fatalf("the profile does not show the language of the tenant:\n%s", body)
	}
	resp = alice.post("/dflt/profile", url.Values{"language": {"en"}})
	wantStatus(t, resp, http.StatusSeeOther)
	if body := page(alice, "/dflt/"); !strings.Contains(body, `<html lang="en">`) {
		t.Fatal("the language of the user does not come before the language of the tenant")
	}
	// A page without a session uses the language of the tenant.
	if body := readBody(t, e.anon().get("/dflt/signed-out")); !strings.Contains(body, `<html lang="sv">`) {
		t.Fatal("the signed-out page does not use the language of the tenant")
	}

	// The system administrators have a profile too.
	root := e.loginAt("/admin", "root")
	if body := page(root, "/admin/profile"); !strings.Contains(body, `action="/admin/profile"`) {
		t.Fatalf("system profile:\n%s", body)
	}
	resp = root.post("/admin/profile", url.Values{"language": {"sv"}})
	wantStatus(t, resp, http.StatusSeeOther)
	if body := page(root, "/admin/"); !strings.Contains(body, "Systemadministration") {
		t.Fatalf("the system pages are not in Swedish:\n%s", body)
	}
}

// The system administrator sets the languages of a tenant.
func TestTenantLanguageSettings(t *testing.T) {
	e := newEnv(t)
	root := e.loginAt("/admin", "root")
	resp := root.get("/admin/tenants/dflt/edit")
	wantStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	for _, want := range []string{`name="language"`, `name="content_language"`, `<option value="sv">Svenska</option>`,
		`<option value="en" selected>English</option>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("tenant form has no %q", want)
		}
	}
	form := url.Values{
		"code": {"dflt"}, "name": {"Default"}, "oidc_issuer": {"https://idp.example"}, "oidc_client_id": {"skriva"},
		"oidc_scopes": {"openid"}, "oidc_groups_claim": {"groups"}, "admin_group": {"admins"},
		"default_role": {"viewer"}, "session_ttl": {"12h"}, "max_doc_kib": {"1024"},
		"language": {"sv"}, "content_language": {"sv"},
	}
	wantStatus(t, root.post("/admin/tenants/dflt", form), http.StatusSeeOther)
	tn, err := e.st.TenantByCode(ctx, "dflt")
	if err != nil || tn.Language != "sv" || tn.ContentLanguage != "sv" {
		t.Fatalf("tenant languages = %q, %q, %v", tn.Language, tn.ContentLanguage, err)
	}
	form.Set("content_language", "xx")
	resp = root.post("/admin/tenants/dflt", form)
	wantStatus(t, resp, http.StatusUnprocessableEntity)
}
