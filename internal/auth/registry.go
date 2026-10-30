package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/clpe/skriva/internal/store"
)

// ErrNotConfigured tells that the tenant has no OIDC settings.
var ErrNotConfigured = errors.New("the tenant has no OIDC settings")

// SecretFunc returns the decrypted client secret of a tenant.
type SecretFunc func(ctx context.Context, t *store.Tenant) (string, error)

// Registry keeps one OIDC client for each tenant. It makes a new client
// when the settings of the tenant change. It is safe for concurrent use.
type Registry struct {
	baseURL string
	secret  SecretFunc

	mu      sync.Mutex
	clients map[int64]registryEntry
}

type registryEntry struct {
	updatedAt time.Time
	client    *OIDC
}

// NewRegistry makes a Registry. baseURL is the public URL of the
// application. The redirect URL of tenant XXXX is
// baseURL + "/XXXX/auth/callback".
func NewRegistry(baseURL string, secret SecretFunc) *Registry {
	return &Registry{baseURL: baseURL, secret: secret, clients: map[int64]registryEntry{}}
}

// ForTenant returns the OIDC client for the tenant.
func (r *Registry) ForTenant(ctx context.Context, t *store.Tenant) (*OIDC, error) {
	if !t.OIDCConfigured() {
		return nil, ErrNotConfigured
	}
	r.mu.Lock()
	e, ok := r.clients[t.ID]
	r.mu.Unlock()
	if ok && e.updatedAt.Equal(t.UpdatedAt) {
		return e.client, nil
	}

	secret, err := r.secret(ctx, t)
	if err != nil {
		return nil, err
	}
	client := New(Config{
		Issuer:       t.OIDCIssuer,
		ClientID:     t.OIDCClientID,
		ClientSecret: secret,
		RedirectURL:  r.baseURL + "/" + t.Code + "/auth/callback",
		Scopes:       t.OIDCScopes,
		GroupsClaim:  t.OIDCGroupsClaim,
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	// A different request can store a client for newer settings while
	// this request reads the secret. Keep the newest client.
	if cur, ok := r.clients[t.ID]; ok && cur.updatedAt.After(t.UpdatedAt) {
		return cur.client, nil
	}
	r.clients[t.ID] = registryEntry{updatedAt: t.UpdatedAt, client: client}
	return client, nil
}
