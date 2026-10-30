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
	"strings"
	"time"

	"github.com/clpe/skriva/internal/secret"
)

// Config contains the settings of the installation.
type Config struct {
	ListenAddr  string
	BaseURL     *url.URL
	DatabaseURL string
	// EncryptionKey encrypts the OIDC client secrets of the tenants.
	EncryptionKey []byte

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
}

// GroupRole is one item of SKRIVA_GROUP_ROLES.
type GroupRole struct {
	Group string
	Role  string
}

// SecureCookies reports whether cookies must have the Secure attribute.
func (c *Config) SecureCookies() bool { return c.BaseURL.Scheme == "https" }

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
		DatabaseURL:      get("SKRIVA_DATABASE_URL", ""),
		OIDCIssuer:       get("SKRIVA_OIDC_ISSUER", ""),
		OIDCClientID:     get("SKRIVA_OIDC_CLIENT_ID", ""),
		OIDCGroupsClaim:  get("SKRIVA_OIDC_GROUPS_CLAIM", "groups"),
		SystemAdminGroup: get("SKRIVA_SYSTEM_ADMIN_GROUP", ""),
		AdminGroup:       get("SKRIVA_ADMIN_GROUP", ""),
	}

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

	base, err := url.Parse(get("SKRIVA_BASE_URL", "http://localhost:8080"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("SKRIVA_BASE_URL: %w", err))
	case base.Scheme != "http" && base.Scheme != "https", base.Host == "":
		errs = append(errs, errors.New("SKRIVA_BASE_URL: must be an absolute http or https URL"))
	case base.Path != "" && base.Path != "/":
		errs = append(errs, errors.New("SKRIVA_BASE_URL: must not have a path"))
	default:
		base.Path = ""
		c.BaseURL = base
	}

	ttl, err := time.ParseDuration(get("SKRIVA_SESSION_TTL", "4h"))
	if err != nil || ttl < time.Minute {
		errs = append(errs, errors.New("SKRIVA_SESSION_TTL: must be a duration of one minute or more"))
	}
	c.SessionTTL = ttl

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
