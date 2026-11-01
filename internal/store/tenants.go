package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Tenant is the main organizational unit. Each tenant has its own
// identity provider, users, groups, custom roles and spaces.
type Tenant struct {
	ID        int64
	Code      string
	IsDefault bool
	TenantSettings
	// HasClientSecret is true if the tenant has an OIDC client secret.
	// The secret is available only from ClientSecret.
	HasClientSecret bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// OIDCConfigured reports whether the tenant has the necessary OIDC
// settings for a sign-in.
func (t *Tenant) OIDCConfigured() bool {
	return t.OIDCIssuer != "" && t.OIDCClientID != ""
}

// TenantSettings are the settings of a tenant that a system administrator
// can change.
type TenantSettings struct {
	Name            string
	Disabled        bool
	OIDCIssuer      string
	OIDCClientID    string
	OIDCScopes      []string
	OIDCGroupsClaim string
	// AdminGroup is an OIDC group that always has the admin role.
	AdminGroup string
	// DefaultRole is the role of all signed-in users. Empty means none.
	DefaultRole string
	SessionTTL  time.Duration
	MaxDocBytes int
}

const tenantColumns = `id, code, name, is_default, disabled, oidc_issuer, oidc_client_id,
	oidc_client_secret IS NOT NULL, oidc_scopes, oidc_groups_claim, admin_group, default_role,
	session_ttl_seconds, max_doc_bytes, created_at, updated_at`

func scanTenant(row pgx.Row) (*Tenant, error) {
	var t Tenant
	var scopes string
	var ttl int
	err := row.Scan(&t.ID, &t.Code, &t.Name, &t.IsDefault, &t.Disabled, &t.OIDCIssuer, &t.OIDCClientID,
		&t.HasClientSecret, &scopes, &t.OIDCGroupsClaim, &t.AdminGroup, &t.DefaultRole,
		&ttl, &t.MaxDocBytes, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	t.OIDCScopes = strings.Fields(scopes)
	t.SessionTTL = time.Duration(ttl) * time.Second
	return &t, nil
}

func secretContext(code string) string {
	return "skriva/tenant/" + code + "/oidc_client_secret"
}

// TenantByCode returns the tenant with the given code.
func (s *Store) TenantByCode(ctx context.Context, code string) (*Tenant, error) {
	return scanTenant(s.pool.QueryRow(ctx, "SELECT "+tenantColumns+" FROM tenants WHERE code = $1", code))
}

// DefaultTenant returns the default tenant.
func (s *Store) DefaultTenant(ctx context.Context) (*Tenant, error) {
	return scanTenant(s.pool.QueryRow(ctx, "SELECT "+tenantColumns+" FROM tenants WHERE is_default"))
}

// Tenants returns all tenants, sorted by code.
func (s *Store) Tenants(ctx context.Context) ([]*Tenant, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+tenantColumns+" FROM tenants ORDER BY code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTenant makes a tenant. clientSecret can be empty. It returns
// ErrConflict if the code is in use, and ErrInvalid if the default role
// does not exist.
func (s *Store) CreateTenant(ctx context.Context, code string, ts TenantSettings, clientSecret string) (*Tenant, error) {
	var t *Tenant
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var secretData []byte
		if clientSecret != "" {
			secretData = s.box.Seal([]byte(clientSecret), secretContext(code))
		}
		var err error
		t, err = scanTenant(tx.QueryRow(ctx, `
			INSERT INTO tenants (code, name, disabled, oidc_issuer, oidc_client_id, oidc_client_secret,
			                     oidc_scopes, oidc_groups_claim, admin_group, default_role,
			                     session_ttl_seconds, max_doc_bytes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING `+tenantColumns,
			code, ts.Name, ts.Disabled, ts.OIDCIssuer, ts.OIDCClientID, secretData,
			strings.Join(ts.OIDCScopes, " "), ts.OIDCGroupsClaim, ts.AdminGroup, ts.DefaultRole,
			int(ts.SessionTTL/time.Second), ts.MaxDocBytes))
		if err != nil {
			return err
		}
		if err := checkRole(ctx, tx, t.ID, ts.DefaultRole); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, auditEntry{Action: AuditTenantCreate, Target: t.Name, TargetID: code}); err != nil {
			return err
		}
		return s.ensureAdminGroup(ctx, tx, t)
	})
	if sqlState(err) == codeUniqueViolation {
		return nil, ErrConflict
	}
	return t, err
}

// UpdateTenant changes the settings of a tenant. If newSecret is nil, the
// client secret does not change. A pointer to "" removes the secret.
func (s *Store) UpdateTenant(ctx context.Context, id int64, ts TenantSettings, newSecret *string) (*Tenant, error) {
	var t *Tenant
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var code string
		if err := tx.QueryRow(ctx, "SELECT code FROM tenants WHERE id = $1 FOR UPDATE", id).Scan(&code); err != nil {
			return notFound(err)
		}
		if err := checkRole(ctx, tx, id, ts.DefaultRole); err != nil {
			return err
		}
		var err error
		t, err = scanTenant(tx.QueryRow(ctx, `
			UPDATE tenants SET name = $2, disabled = $3, oidc_issuer = $4, oidc_client_id = $5,
			       oidc_scopes = $6, oidc_groups_claim = $7, admin_group = $8, default_role = $9,
			       session_ttl_seconds = $10, max_doc_bytes = $11, updated_at = $12
			WHERE id = $1
			RETURNING `+tenantColumns,
			id, ts.Name, ts.Disabled, ts.OIDCIssuer, ts.OIDCClientID,
			strings.Join(ts.OIDCScopes, " "), ts.OIDCGroupsClaim, ts.AdminGroup, ts.DefaultRole,
			int(ts.SessionTTL/time.Second), ts.MaxDocBytes, s.now()))
		if err != nil {
			return err
		}
		if newSecret != nil {
			var data []byte
			if *newSecret != "" {
				data = s.box.Seal([]byte(*newSecret), secretContext(code))
			}
			if _, err := tx.Exec(ctx, "UPDATE tenants SET oidc_client_secret = $2 WHERE id = $1", id, data); err != nil {
				return err
			}
			t.HasClientSecret = data != nil
		}
		if ts.Disabled {
			// A disabled tenant has no sessions.
			if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE tenant_id = $1", id); err != nil {
				return err
			}
		}
		details := "Enabled."
		if ts.Disabled {
			details = "Disabled."
		}
		if newSecret != nil {
			details += " The client secret changed."
		}
		if err := s.audit(ctx, tx, auditEntry{Action: AuditTenantUpdate, Target: t.Name, TargetID: code, Details: details}); err != nil {
			return err
		}
		return s.ensureAdminGroup(ctx, tx, t)
	})
	return t, err
}

// DeleteTenant removes a tenant and all its data. The default tenant is
// permanent: the function returns ErrInvalid for it.
func (s *Store) DeleteTenant(ctx context.Context, id int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var code, name string
		err := tx.QueryRow(ctx, "DELETE FROM tenants WHERE id = $1 AND NOT is_default RETURNING code, name",
			id).Scan(&code, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			var isDefault bool
			if err := tx.QueryRow(ctx, "SELECT is_default FROM tenants WHERE id = $1", id).Scan(&isDefault); err != nil {
				return notFound(err)
			}
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{Action: AuditTenantDelete, Target: name, TargetID: code})
	})
}

// ClientSecret returns the decrypted OIDC client secret of the tenant.
func (s *Store) ClientSecret(ctx context.Context, t *Tenant) (string, error) {
	var data []byte
	if err := s.pool.QueryRow(ctx, "SELECT oidc_client_secret FROM tenants WHERE id = $1", t.ID).Scan(&data); err != nil {
		return "", notFound(err)
	}
	if data == nil {
		return "", nil
	}
	plain, err := s.box.Open(data, secretContext(t.Code))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// EnsureAdminGroups gives the admin role to the admin group of each
// tenant. The application calls it at start. Thus an administrator can
// always get access again.
func (s *Store) EnsureAdminGroups(ctx context.Context) error {
	tenants, err := s.Tenants(ctx)
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return s.ensureAdminGroup(ctx, tx, t)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureAdminGroup(ctx context.Context, tx pgx.Tx, t *Tenant) error {
	if t.AdminGroup == "" {
		return nil
	}
	gid, err := ensureOIDCGroup(ctx, tx, t.ID, t.AdminGroup)
	if err != nil {
		return err
	}
	id, err := bind(ctx, tx, t.ID, "admin", nil, &gid, Scope{})
	if err != nil || id == 0 {
		return err
	}
	return s.auditBinding(ctx, tx, t.ID, id, AuditBindingCreate)
}

// checkRole returns ErrInvalid if the role name is not empty and no
// built-in role or custom role of the tenant has the name.
func checkRole(ctx context.Context, q querier, tenantID int64, name string) error {
	if name == "" {
		return nil
	}
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles
		WHERE name = $2 AND (tenant_id IS NULL OR tenant_id = $1))`, tenantID, name).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrInvalid
	}
	return nil
}
