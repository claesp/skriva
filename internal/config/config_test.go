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
	if !c.AutoMigrate {
		t.Fatal("the server must apply the migrations by default")
	}
}

func TestLoadDatabaseURLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(path, []byte("postgres://u:p@db/skriva\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := validEnv()
	delete(e, "SKRIVA_DATABASE_URL")
	e["SKRIVA_DATABASE_URL_FILE"] = path
	if c, err := load(env(e)); err != nil || c.DatabaseURL != "postgres://u:p@db/skriva" {
		t.Fatalf("DatabaseURL = %q, %v", c.DatabaseURL, err)
	}
}

func TestLoadAutoMigrate(t *testing.T) {
	e := validEnv()
	e["SKRIVA_AUTO_MIGRATE"] = "false"
	if c, err := load(env(e)); err != nil || c.AutoMigrate {
		t.Fatalf("SKRIVA_AUTO_MIGRATE=false: %v, %v", c.AutoMigrate, err)
	}
	e["SKRIVA_AUTO_MIGRATE"] = "maybe"
	if _, err := load(env(e)); err == nil || !strings.Contains(err.Error(), "SKRIVA_AUTO_MIGRATE") {
		t.Fatalf("bad SKRIVA_AUTO_MIGRATE: %v", err)
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

func TestPreviousEncryptionKey(t *testing.T) {
	m := validEnv()
	c, err := load(env(m))
	if err != nil || c.PreviousEncryptionKey != nil {
		t.Fatalf("no previous key: %v, %v", c.PreviousEncryptionKey, err)
	}
	m["SKRIVA_ENCRYPTION_KEY_PREVIOUS"] = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	if c, err = load(env(m)); err != nil || len(c.PreviousEncryptionKey) != 32 {
		t.Fatalf("previous key: %v, %v", c.PreviousEncryptionKey, err)
	}
	m["SKRIVA_ENCRYPTION_KEY_PREVIOUS"] = "c2hvcnQ="
	if _, err := load(env(m)); err == nil {
		t.Fatal("expected an error for a short previous key")
	}
}

func TestBadBaseURL(t *testing.T) {
	for _, u := range []string{
		"https://example.com/wiki", "https://example.com/?x=1", "https://example.com/?",
		"https://example.com/#top", "https://user@example.com", "ftp://example.com", "/wiki",
	} {
		m := validEnv()
		m["SKRIVA_BASE_URL"] = u
		if _, err := load(env(m)); err == nil {
			t.Errorf("no error for the base URL %q", u)
		}
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

func TestTypstPath(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "typst")
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(text, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if path, err := (&Config{Typst: prog}).TypstPath(); err != nil || path != prog {
		t.Fatalf("TypstPath = %q, %v", path, err)
	}
	for _, bad := range []string{text, dir, filepath.Join(dir, "missing")} {
		if _, err := (&Config{Typst: bad}).TypstPath(); err == nil {
			t.Errorf("TypstPath(%q) did not fail", bad)
		}
	}
	// Without SKRIVA_TYPST, the function finds the program in the PATH.
	t.Setenv("PATH", dir)
	if path, err := (&Config{}).TypstPath(); err != nil || path != prog {
		t.Fatalf("TypstPath from PATH = %q, %v", path, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := (&Config{}).TypstPath(); err == nil {
		t.Fatal("TypstPath found a program in an empty PATH")
	}
}

func TestMmdrPath(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "mmdr")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if path, err := (&Config{Mmdr: prog}).MmdrPath(); err != nil || path != prog {
		t.Fatalf("MmdrPath = %q, %v", path, err)
	}
	if _, err := (&Config{Mmdr: dir}).MmdrPath(); err == nil || !strings.Contains(err.Error(), "SKRIVA_MMDR") {
		t.Fatalf("MmdrPath(dir) = %v", err)
	}
	t.Setenv("PATH", dir)
	if path, err := (&Config{}).MmdrPath(); err != nil || path != prog {
		t.Fatalf("MmdrPath from PATH = %q, %v", path, err)
	}
}

func TestLanguage(t *testing.T) {
	c, err := load(env(validEnv()))
	if err != nil || c.Language != "en" {
		t.Fatalf("default language = %q, %v", c.Language, err)
	}
	m := validEnv()
	m["SKRIVA_LANGUAGE"] = "sv"
	if c, err = load(env(m)); err != nil || c.Language != "sv" {
		t.Fatalf("SKRIVA_LANGUAGE=sv gives %q, %v", c.Language, err)
	}
	m["SKRIVA_LANGUAGE"] = "fr"
	if _, err := load(env(m)); err == nil {
		t.Fatal("SKRIVA_LANGUAGE=fr gives no error")
	}
}

func TestProgramMemory(t *testing.T) {
	c, err := load(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.TypstMemory != 1<<30 || c.MmdrMemory != 256<<20 {
		t.Fatalf("memory = %d, %d", c.TypstMemory, c.MmdrMemory)
	}
	m := validEnv()
	m["SKRIVA_TYPST_MEMORY"] = "2048"
	if c, err = load(env(m)); err != nil || c.TypstMemory != 2<<30 {
		t.Fatalf("SKRIVA_TYPST_MEMORY=2048 gives %d, %v", c.TypstMemory, err)
	}
	for _, bad := range []string{"0", "8", "1G", "-1", "100000"} {
		m["SKRIVA_MMDR_MEMORY"] = bad
		if _, err := load(env(m)); err == nil || !strings.Contains(err.Error(), "SKRIVA_MMDR_MEMORY") {
			t.Errorf("SKRIVA_MMDR_MEMORY=%s gives %v", bad, err)
		}
	}
}
