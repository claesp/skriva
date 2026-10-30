// Package auth connects to an OpenID Connect provider. It makes the login
// URL, exchanges the authorization code for tokens, and verifies the ID
// token.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/clpe/skriva/internal/store"
)

// ErrUnavailable tells that the identity provider did not respond.
var ErrUnavailable = errors.New("identity provider is not available")

// Limits for data from the identity provider.
const (
	maxGroups       = 500
	maxGroupNameLen = 200
)

// Config contains the settings for the OIDC client.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	GroupsClaim  string
}

// OIDC is an OpenID Connect relying party. It is safe for concurrent use.
//
// The first request that needs the provider does the discovery. Thus the
// application can start before the identity provider is available.
type OIDC struct {
	cfg    Config
	client *http.Client

	mu         sync.Mutex
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	endSession string
}

// New makes an OIDC client. It does not connect to the provider.
func New(cfg Config) *OIDC {
	return &OIDC{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Ready makes sure that the provider discovery is complete.
func (a *OIDC) Ready(ctx context.Context) error {
	_, err := a.init(ctx)
	return err
}

func (a *OIDC) init(ctx context.Context) (*oidc.Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider, nil
	}

	// The provider keeps the context for the key set requests. Thus use
	// a context that does not end with the request.
	pctx := oidc.ClientContext(context.WithoutCancel(ctx), a.client)
	dctx, cancel := context.WithTimeout(pctx, 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(dctx, a.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := p.Claims(&meta); err != nil {
		return nil, fmt.Errorf("read provider metadata: %w", err)
	}

	scopes := a.cfg.Scopes
	hasOpenID := false
	for _, s := range scopes {
		hasOpenID = hasOpenID || s == oidc.ScopeOpenID
	}
	if !hasOpenID {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}

	a.provider = p
	a.verifier = p.Verifier(&oidc.Config{ClientID: a.cfg.ClientID})
	a.oauth = oauth2.Config{
		ClientID:     a.cfg.ClientID,
		ClientSecret: a.cfg.ClientSecret,
		RedirectURL:  a.cfg.RedirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       scopes,
	}
	a.endSession = meta.EndSession
	return p, nil
}

// AuthCodeURL returns the URL of the login page at the provider. The
// request uses PKCE (S256) and a nonce.
func (a *OIDC) AuthCodeURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	if _, err := a.init(ctx); err != nil {
		return "", err
	}
	return a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Exchange changes the authorization code into tokens, verifies the ID
// token and returns the identity of the user.
func (a *OIDC) Exchange(ctx context.Context, code, verifier, nonce string) (store.Identity, error) {
	if _, err := a.init(ctx); err != nil {
		return store.Identity{}, err
	}
	ctx = oidc.ClientContext(ctx, a.client)
	tok, err := a.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return store.Identity{}, fmt.Errorf("exchange code: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return store.Identity{}, errors.New("token response has no id_token")
	}
	idt, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return store.Identity{}, fmt.Errorf("verify id_token: %w", err)
	}
	if idt.Nonce != nonce {
		return store.Identity{}, errors.New("id_token nonce does not match")
	}

	var claims map[string]json.RawMessage
	if err := idt.Claims(&claims); err != nil {
		return store.Identity{}, fmt.Errorf("read claims: %w", err)
	}
	return identityFromClaims(idt.Issuer, idt.Subject, claims, a.cfg.GroupsClaim)
}

// LogoutURL returns the URL of the logout endpoint of the provider, or an
// empty string if the provider does not have one.
func (a *OIDC) LogoutURL(ctx context.Context, postLogoutRedirect string) string {
	if _, err := a.init(ctx); err != nil || a.endSession == "" {
		return ""
	}
	u, err := url.Parse(a.endSession)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("client_id", a.cfg.ClientID)
	q.Set("post_logout_redirect_uri", postLogoutRedirect)
	u.RawQuery = q.Encode()
	return u.String()
}

func identityFromClaims(issuer, subject string, claims map[string]json.RawMessage, groupsClaim string) (store.Identity, error) {
	str := func(key string) string {
		var v string
		if raw, ok := claims[key]; ok {
			_ = json.Unmarshal(raw, &v) // A value of a different type gives "".
		}
		return v
	}
	id := store.Identity{
		Issuer:      issuer,
		Subject:     subject,
		Username:    str("preferred_username"),
		Email:       str("email"),
		DisplayName: str("name"),
	}
	if id.Subject == "" {
		return id, errors.New("id_token has no subject")
	}
	if id.Username == "" {
		id.Username = id.Email
	}
	if id.Username == "" {
		id.Username = id.Subject
	}

	if raw, ok := claims[groupsClaim]; ok && groupsClaim != "" {
		var groups []string
		if err := json.Unmarshal(raw, &groups); err != nil {
			return id, fmt.Errorf("claim %q is not a list of strings", groupsClaim)
		}
		if len(groups) > maxGroups {
			return id, fmt.Errorf("claim %q has more than %d groups", groupsClaim, maxGroups)
		}
		for _, g := range groups {
			if g != "" && len(g) <= maxGroupNameLen {
				id.Groups = append(id.Groups, g)
			}
		}
	}
	return id, nil
}
