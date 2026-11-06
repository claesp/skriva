// Package config reads the application settings from environment
// variables.
//
// Most settings are per tenant and are in the database. The environment
// contains the settings for the installation: the database, the
// encryption key and the identity provider for system administrators.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/claesp/skriva/internal/i18n"
	"github.com/claesp/skriva/internal/secret"
)

// Config contains the settings of the installation.
type Config struct {
	ListenAddr  string
	BaseURL     *url.URL
	DatabaseURL string
	// AutoMigrate permits the server to apply the database migrations at
	// start. If it is false and the database needs migrations, the
	// server stops. Then run "skriva migrate".
	AutoMigrate bool
	// EncryptionKey is the key encryption key (KEK). It encrypts the OIDC
	// client secrets of the tenants and the keys of the tenants.
	EncryptionKey []byte
	// PreviousEncryptionKey is the old KEK after a change of the KEK, or
	// nil. At start, the server encrypts all keys and secrets of the old
	// KEK again with the new KEK.
	PreviousEncryptionKey []byte

	// The OIDC provider for system administrators. At the first start,
	// the default tenant also gets these settings.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       []string
	OIDCGroupsClaim  string
	// SystemAdminGroup is the OIDC group of the system administrators.
	// Empty means that nobody can sign in as system administrator.
	SystemAdminGroup string

	// AdminGroup is an OIDC group of the default tenant. The application
	// gives it the "admin" role. Empty means no group.
	AdminGroup string
	// GroupRoles maps OIDC group names of the default tenant to role
	// names. At start, the application gives each role to its group.
	GroupRoles []GroupRole

	// SessionTTL is the maximum life of a system administrator session.
	SessionTTL time.Duration

	// Typst is the path of the Typst program for the PDF export, or ""
	// to find "typst" in the PATH. See TypstPath.
	Typst string
	// Mmdr is the path of the mmdr program for Mermaid diagrams, or "" to
	// find "mmdr" in the PATH. See MmdrPath.
	Mmdr string
	// TypstMemory and MmdrMemory are the largest data memory of one
	// process of the program, in bytes.
	TypstMemory uint64
	MmdrMemory  uint64
	// Language is the default language of the user interface. A tenant
	// can set a different language, and a user can select a language.
	// It is also the content language of a new tenant, if it has a
	// stemmer.
	Language string
}

// GroupRole is one item of SKRIVA_GROUP_ROLES.
type GroupRole struct {
	Group string
	Role  string
}

// TypstPath returns the path of the Typst program. If SKRIVA_TYPST is
// set, the program must exist. If it is not set, the function finds
// "typst" in the PATH. The error tells why the PDF export is not
// available.
func (c *Config) TypstPath() (string, error) { return programPath("SKRIVA_TYPST", c.Typst, "typst") }

// MmdrPath returns the path of the mmdr program, the same as TypstPath.
// The error tells why Skriva cannot show Mermaid diagrams.
func (c *Config) MmdrPath() (string, error) { return programPath("SKRIVA_MMDR", c.Mmdr, "mmdr") }

// programPath returns the path of a program. value is the value of the
// variable env. If value is "", the function finds name in the PATH.
func programPath(env, value, name string) (string, error) {
	if value == "" {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%s is not set and %w", env, err)
		}
		return path, nil
	}
	info, err := os.Stat(value)
	switch {
	case err != nil:
		return "", fmt.Errorf("%s: %w", env, err)
	case info.IsDir() || info.Mode().Perm()&0o111 == 0:
		return "", fmt.Errorf("%s: %s is not a program", env, value)
	}
	return value, nil
}

// SecureCookies reports whether cookies must have the Secure attribute.
func (c *Config) SecureCookies() bool { return c.BaseURL.Scheme == "https" }

// DatabaseURL reads only SKRIVA_DATABASE_URL (or SKRIVA_DATABASE_URL_FILE),
// for the commands that need only the database.
func DatabaseURL() (string, error) {
	v, err := fromEnvOrFile(os.Getenv, "SKRIVA_DATABASE_URL")
	if err == nil && v == "" {
		err = errors.New("SKRIVA_DATABASE_URL: value is necessary")
	}
	return v, err
}

// Load reads the configuration from the environment.
func Load() (*Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (*Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	var errs []error
	c := &Config{
		ListenAddr:       get("SKRIVA_LISTEN_ADDR", ":8080"),
		OIDCIssuer:       get("SKRIVA_OIDC_ISSUER", ""),
		OIDCClientID:     get("SKRIVA_OIDC_CLIENT_ID", ""),
		OIDCGroupsClaim:  get("SKRIVA_OIDC_GROUPS_CLAIM", "groups"),
		SystemAdminGroup: get("SKRIVA_SYSTEM_ADMIN_GROUP", ""),
		AdminGroup:       get("SKRIVA_ADMIN_GROUP", ""),
		Typst:            get("SKRIVA_TYPST", ""),
		Mmdr:             get("SKRIVA_MMDR", ""),
	}

	// The URL can contain the password of the database.
	dbURL, err := fromEnvOrFile(getenv, "SKRIVA_DATABASE_URL")
	if err != nil {
		errs = append(errs, err)
	}
	c.DatabaseURL = dbURL

	secretValue, err := fromEnvOrFile(getenv, "SKRIVA_OIDC_CLIENT_SECRET")
	if err != nil {
		errs = append(errs, err)
	}
	c.OIDCClientSecret = secretValue

	keyText, err := fromEnvOrFile(getenv, "SKRIVA_ENCRYPTION_KEY")
	switch {
	case err != nil:
		errs = append(errs, err)
	case keyText == "":
		errs = append(errs, errors.New("SKRIVA_ENCRYPTION_KEY: value is necessary (32 random bytes in base64)"))
	default:
		if c.EncryptionKey, err = secret.ParseKey(keyText); err != nil {
			errs = append(errs, fmt.Errorf("SKRIVA_ENCRYPTION_KEY: %w", err))
		}
	}
	switch prevText, err := fromEnvOrFile(getenv, "SKRIVA_ENCRYPTION_KEY_PREVIOUS"); {
	case err != nil:
		errs = append(errs, err)
	case prevText != "":
		if c.PreviousEncryptionKey, err = secret.ParseKey(prevText); err != nil {
			errs = append(errs, fmt.Errorf("SKRIVA_ENCRYPTION_KEY_PREVIOUS: %w", err))
		}
	}

	if v := get("SKRIVA_GROUP_ROLES", ""); v != "" {
		for _, item := range strings.Split(v, ",") {
			group, role, ok := strings.Cut(strings.TrimSpace(item), "=")
			group, role = strings.TrimSpace(group), strings.TrimSpace(role)
			if !ok || group == "" || role == "" {
				errs = append(errs, fmt.Errorf("SKRIVA_GROUP_ROLES: item %q must have the form group=role", item))
				continue
			}
			c.GroupRoles = append(c.GroupRoles, GroupRole{Group: group, Role: role})
		}
	}

	c.OIDCScopes = strings.Fields(get("SKRIVA_OIDC_SCOPES", "openid profile email"))

	if c.AutoMigrate, err = strconv.ParseBool(get("SKRIVA_AUTO_MIGRATE", "true")); err != nil {
		errs = append(errs, errors.New("SKRIVA_AUTO_MIGRATE: must be true or false"))
	}

	base, err := url.Parse(get("SKRIVA_BASE_URL", "http://localhost:8080"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("SKRIVA_BASE_URL: %w", err))
	case base.Scheme != "http" && base.Scheme != "https", base.Host == "":
		errs = append(errs, errors.New("SKRIVA_BASE_URL: must be an absolute http or https URL"))
	case base.Path != "" && base.Path != "/":
		errs = append(errs, errors.New("SKRIVA_BASE_URL: must not have a path"))
	case base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "":
		errs = append(errs, errors.New("SKRIVA_BASE_URL: must not have a user, a query or a fragment"))
	default:
		base.Path = ""
		c.BaseURL = base
	}

	ttl, err := time.ParseDuration(get("SKRIVA_SESSION_TTL", "4h"))
	if err != nil || ttl < time.Minute {
		errs = append(errs, errors.New("SKRIVA_SESSION_TTL: must be a duration of one minute or more"))
	}
	c.SessionTTL = ttl

	c.TypstMemory = megabytes(get, "SKRIVA_TYPST_MEMORY", 1024, &errs)
	c.MmdrMemory = megabytes(get, "SKRIVA_MMDR_MEMORY", 256, &errs)

	c.Language = get("SKRIVA_LANGUAGE", i18n.Default)
	if !i18n.IsUI(c.Language) {
		var codes []string
		for _, l := range i18n.UILanguages() {
			codes = append(codes, l.Code)
		}
		errs = append(errs, fmt.Errorf("SKRIVA_LANGUAGE: must be one of %s", strings.Join(codes, ", ")))
	}

	for key, val := range map[string]string{
		"SKRIVA_DATABASE_URL":       c.DatabaseURL,
		"SKRIVA_OIDC_ISSUER":        c.OIDCIssuer,
		"SKRIVA_OIDC_CLIENT_ID":     c.OIDCClientID,
		"SKRIVA_OIDC_CLIENT_SECRET": c.OIDCClientSecret,
	} {
		if val == "" {
			errs = append(errs, fmt.Errorf("%s: value is necessary", key))
		}
	}
	return c, errors.Join(errs...)
}

// megabytes reads a memory size in MiB from the variable key, and
// returns it in bytes. The value must be a whole number from 16 to
// 65536. An empty variable gives def.
func megabytes(get func(key, def string) string, key string, def uint64, errs *[]error) uint64 {
	v := get(key, strconv.FormatUint(def, 10))
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n < 16 || n > 65536 {
		*errs = append(*errs, fmt.Errorf("%s: must be a number of MiB from 16 to 65536", key))
		return def << 20
	}
	return n << 20
}

// fromEnvOrFile reads KEY, or the file that KEY_FILE names. A file is
// better for secrets because environment variables can leak to logs.
func fromEnvOrFile(getenv func(string) string, key string) (string, error) {
	if path := getenv(key + "_FILE"); path != "" {
		b, err := os.ReadFile(path) // #nosec G304 -- the operator sets the path.
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", key, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(getenv(key)), nil
}
