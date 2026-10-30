package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"SKRIVA_DATABASE_URL":       "postgres://localhost/skriva",
		"SKRIVA_OIDC_ISSUER":        "http://localhost:8180/realms/skriva",
		"SKRIVA_OIDC_CLIENT_ID":     "skriva",
		"SKRIVA_OIDC_CLIENT_SECRET": "secret",
		"SKRIVA_ENCRYPTION_KEY":     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.OIDCGroupsClaim != "groups" || len(c.EncryptionKey) != 32 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.SecureCookies() {
		t.Fatal("http base URL must not give secure cookies")
	}
}

func TestLoadMissingValues(t *testing.T) {
	_, err := load(env(map[string]string{}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"SKRIVA_DATABASE_URL", "SKRIVA_OIDC_ISSUER", "SKRIVA_OIDC_CLIENT_SECRET", "SKRIVA_ENCRYPTION_KEY"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not name %s: %v", key, err)
		}
	}
}

func TestLoadSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := validEnv()
	delete(m, "SKRIVA_OIDC_CLIENT_SECRET")
	m["SKRIVA_OIDC_CLIENT_SECRET_FILE"] = path
	c, err := load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.OIDCClientSecret != "from-file" {
		t.Fatalf("got secret %q", c.OIDCClientSecret)
	}
}

func TestLoadBadBaseURL(t *testing.T) {
	m := validEnv()
	m["SKRIVA_BASE_URL"] = "localhost:8080"
	if _, err := load(env(m)); err == nil {
		t.Fatal("expected an error for a relative base URL")
	}
}

func TestBadEncryptionKey(t *testing.T) {
	m := validEnv()
	m["SKRIVA_ENCRYPTION_KEY"] = "c2hvcnQ="
	if _, err := load(env(m)); err == nil {
		t.Fatal("expected an error for a short key")
	}
}

func TestBaseURLWithPath(t *testing.T) {
	m := validEnv()
	m["SKRIVA_BASE_URL"] = "https://example.com/wiki"
	if _, err := load(env(m)); err == nil {
		t.Fatal("expected an error for a base URL with a path")
	}
}

func TestGroupRoles(t *testing.T) {
	m := validEnv()
	m["SKRIVA_GROUP_ROLES"] = "editors=editor, readers = viewer"
	c, err := load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	want := []GroupRole{{"editors", "editor"}, {"readers", "viewer"}}
	if len(c.GroupRoles) != len(want) {
		t.Fatalf("got %v", c.GroupRoles)
	}
	for i := range want {
		if c.GroupRoles[i] != want[i] {
			t.Fatalf("got %v, want %v", c.GroupRoles, want)
		}
	}

	m["SKRIVA_GROUP_ROLES"] = "editors"
	if _, err := load(env(m)); err == nil {
		t.Fatal("expected an error for an item without a role")
	}
}
