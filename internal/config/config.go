// Package config reads the application settings from environment
// variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config contains all settings of the application.
type Config struct {
	ListenAddr  string
	BaseURL     *url.URL
	DatabaseURL string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       []string
	OIDCGroupsClaim  string

	// AdminGroup is the name of an OIDC group. At start, the application
	// gives the "admin" role to this group. Empty means no group.
	AdminGroup string
	// GroupRoles maps OIDC group names to role names. At start, the
	// application gives each role to its group.
	GroupRoles []GroupRole
	// DefaultRole is the role that all signed-in users get.
	// Empty means no role.
	DefaultRole string

	SessionTTL time.Duration
	// MaxPageBytes is the largest permitted page body.
	MaxPageBytes int64
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
		ListenAddr:      get("SKRIVA_LISTEN_ADDR", ":8080"),
		DatabaseURL:     get("SKRIVA_DATABASE_URL", ""),
		OIDCIssuer:      get("SKRIVA_OIDC_ISSUER", ""),
		OIDCClientID:    get("SKRIVA_OIDC_CLIENT_ID", ""),
		OIDCGroupsClaim: get("SKRIVA_OIDC_GROUPS_CLAIM", "groups"),
		AdminGroup:      get("SKRIVA_ADMIN_GROUP", ""),
		DefaultRole:     get("SKRIVA_DEFAULT_ROLE", "viewer"),
		MaxPageBytes:    1 << 20,
	}
	if strings.EqualFold(c.DefaultRole, "none") {
		c.DefaultRole = ""
	}

	secret, err := secretValue(getenv, "SKRIVA_OIDC_CLIENT_SECRET")
	if err != nil {
		errs = append(errs, err)
	}
	c.OIDCClientSecret = secret

	if c.AdminGroup != "" {
		c.GroupRoles = append(c.GroupRoles, GroupRole{Group: c.AdminGroup, Role: "admin"})
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
	default:
		base.Path = strings.TrimSuffix(base.Path, "/")
		c.BaseURL = base
	}

	ttl, err := time.ParseDuration(get("SKRIVA_SESSION_TTL", "12h"))
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

// secretValue reads KEY, or the file that KEY_FILE names. A file is
// better for secrets because environment variables can leak to logs.
func secretValue(getenv func(string) string, key string) (string, error) {
	if path := getenv(key + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", key, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return getenv(key), nil
}
