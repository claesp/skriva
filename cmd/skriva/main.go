// Command skriva runs the Skriva wiki server.
//
// Usage:
//
//	skriva           Start the server. At start, it applies the database
//	                 migrations (see SKRIVA_AUTO_MIGRATE).
//	skriva migrate   Apply the database migrations and stop.
//	skriva certs     Make a private CA and TLS certificates for Skriva,
//	                 Keycloak and PostgreSQL. See "skriva certs -h".
//	skriva version   Show the version and the schema version.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/claesp/skriva/internal/auth"
	"github.com/claesp/skriva/internal/certs"
	"github.com/claesp/skriva/internal/config"
	"github.com/claesp/skriva/internal/db"
	"github.com/claesp/skriva/internal/diagram"
	"github.com/claesp/skriva/internal/export"
	"github.com/claesp/skriva/internal/secret"
	"github.com/claesp/skriva/internal/store"
	"github.com/claesp/skriva/internal/version"
	"github.com/claesp/skriva/internal/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "":
		if err := run(log); err != nil {
			log.Error("server stopped", "error", err)
			os.Exit(1)
		}
	case "migrate":
		if err := migrate(log); err != nil {
			log.Error("migration failed", "error", err)
			os.Exit(1)
		}
	case "certs":
		if err := makeCerts(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "skriva certs:", err)
			os.Exit(1)
		}
	case "version", "-version", "--version":
		fmt.Printf("skriva %s (schema version %d)\n", version.String(), db.SchemaVersion())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: skriva [migrate|certs|version]\n", cmd)
		os.Exit(2)
	}
}

// migrate applies the database migrations. It needs only
// SKRIVA_DATABASE_URL.
func migrate(log *slog.Logger) error {
	url, err := config.DatabaseURL()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	roots, err := certs.Pool(config.TLSCAFile())
	if err != nil {
		return fmt.Errorf("SKRIVA_TLS_CA_FILE: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, url, roots)
	if err != nil {
		return err
	}
	defer pool.Close()
	res, err := db.Upgrade(ctx, pool, db.Options{AppVersion: version.String(), Apply: true})
	if err != nil {
		return err
	}
	log.Info("database schema is current", "from", res.From, "to", res.To, "applied", len(res.Applied))
	return nil
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	log.Info("starting", "version", version.String(), "schema_version", db.SchemaVersion())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The trusted CA certificates for the database and the OIDC
	// providers: the system certificates and SKRIVA_TLS_CA_FILE.
	roots, err := certs.Pool(cfg.TLSCAFile)
	if err != nil {
		return fmt.Errorf("SKRIVA_TLS_CA_FILE: %w", err)
	}
	httpClient := auth.NewHTTPClient(roots)
	var serverCert *certs.Server
	if cfg.TLS {
		if serverCert, err = loadServerCert(cfg, log); err != nil {
			return err
		}
	} else {
		log.Warn("SKRIVA_TLS is false: the server uses HTTP; use it only behind a reverse proxy with TLS")
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL, roots)
	if err != nil {
		return err
	}
	defer pool.Close()
	// The boot step: compare the schema of the database with the schema
	// of the program, and apply the missing migrations.
	res, err := db.Upgrade(ctx, pool, db.Options{AppVersion: version.String(), Apply: cfg.AutoMigrate})
	if err != nil {
		return err
	}
	if len(res.Applied) > 0 {
		log.Info("database schema upgraded", "from", res.From, "to", res.To, "migrations", res.Applied)
	}
	box, err := secret.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	st := store.New(pool, box)
	defer st.Close()
	if cfg.PreviousEncryptionKey != nil {
		previous, err := secret.New(cfg.PreviousEncryptionKey)
		if err != nil {
			return err
		}
		n, err := st.RewrapKeys(ctx, previous)
		if err != nil {
			return fmt.Errorf("encrypt the keys with the new SKRIVA_ENCRYPTION_KEY: %w", err)
		}
		log.Info("keys encrypted with the new key encryption key; remove SKRIVA_ENCRYPTION_KEY_PREVIOUS", "keys", n)
	}
	if err := bootstrap(ctx, st, cfg, log); err != nil {
		return err
	}
	// A problem with the keys stops the server now, and not at the first
	// save of a document.
	if err := st.EnsureAllKeys(ctx); err != nil {
		return fmt.Errorf("keys of the tenants: %w", err)
	}

	systemAuth := auth.New(auth.Config{
		Issuer:       cfg.OIDCIssuer,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.BaseURL.String() + "/admin/auth/callback",
		Scopes:       cfg.OIDCScopes,
		GroupsClaim:  cfg.OIDCGroupsClaim,
		HTTPClient:   httpClient,
	})
	// Do the discovery early if the provider is available. If it is not
	// available, the first login tries again.
	if err := systemAuth.Ready(ctx); err != nil {
		log.Warn("OIDC discovery failed, will try again at first login", "error", err)
	}
	if cfg.SystemAdminGroup == "" {
		log.Warn("SKRIVA_SYSTEM_ADMIN_GROUP is not set: nobody can sign in to the system administration")
	}
	registry := auth.NewRegistry(cfg.BaseURL.String(), st.ClientSecret, httpClient)

	var pdf *export.PDFOptions
	if path, err := cfg.TypstPath(); err != nil {
		if cfg.Typst != "" {
			return err
		}
		log.Info("the PDF export is not available", "reason", err)
	} else {
		pdf = &export.PDFOptions{Typst: path, MaxMemory: cfg.TypstMemory}
	}
	var diagrams *diagram.Renderer
	if path, err := cfg.MmdrPath(); err != nil {
		if cfg.Mmdr != "" {
			return err
		}
		log.Info("Mermaid diagrams are not available", "reason", err)
	} else {
		diagrams = diagram.New(diagram.Options{Program: path, MaxMemory: cfg.MmdrMemory})
	}
	handler, err := web.New(st, web.Options{
		Formats:          export.NewFormats(pdf),
		Diagrams:         diagrams,
		Language:         cfg.Language,
		BaseURL:          cfg.BaseURL,
		SecureCookies:    cfg.SecureCookies(),
		SystemAuth:       systemAuth,
		SystemAdminGroup: cfg.SystemAdminGroup,
		SystemSessionTTL: cfg.SessionTTL,
		TenantAuth: func(ctx context.Context, t *store.Tenant) (web.Authenticator, error) {
			return registry.ForTenant(ctx, t)
		},
	}, log)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	go cleanup(ctx, st, log)

	errc := make(chan error, 1)
	go func() {
		log.Info("server started", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL.String(), "tls", cfg.TLS)
		if serverCert != nil {
			srv.TLSConfig = serverCert.Config()
			errc <- srv.ListenAndServeTLS("", "")
			return
		}
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// A second signal stops the process at once.
	stop()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// bootstrap prepares the default tenant:
//   - If the tenant has no identity provider, it gets the settings of the
//     system identity provider (SKRIVA_OIDC_*).
//   - If the tenant has no admin group, it gets SKRIVA_ADMIN_GROUP.
//   - The groups in SKRIVA_GROUP_ROLES get their roles.
//
// Then the admin group of each tenant gets the admin role. Thus an
// administrator can always get access again with a restart.
func bootstrap(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger) error {
	t, err := st.DefaultTenant(ctx)
	if err != nil {
		return fmt.Errorf("read default tenant: %w", err)
	}
	if !t.OIDCConfigured() || (t.AdminGroup == "" && cfg.AdminGroup != "") {
		ts := t.TenantSettings
		var newSecret *string
		if !t.OIDCConfigured() {
			ts.OIDCIssuer = cfg.OIDCIssuer
			ts.OIDCClientID = cfg.OIDCClientID
			ts.OIDCScopes = cfg.OIDCScopes
			ts.OIDCGroupsClaim = cfg.OIDCGroupsClaim
			newSecret = &cfg.OIDCClientSecret
			log.Info("default tenant gets the system identity provider", "tenant", t.Code)
		}
		if ts.AdminGroup == "" {
			ts.AdminGroup = cfg.AdminGroup
		}
		if t, err = st.UpdateTenant(ctx, t.ID, t.Code, ts, newSecret); err != nil {
			return fmt.Errorf("update default tenant: %w", err)
		}
	}
	for _, gr := range cfg.GroupRoles {
		id, err := st.EnsureOIDCGroup(ctx, t.ID, gr.Group)
		if err != nil {
			return fmt.Errorf("create group %q: %w", gr.Group, err)
		}
		err = st.BindRoleToGroup(ctx, t.ID, gr.Role, id, store.Scope{})
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("SKRIVA_GROUP_ROLES: role %q does not exist", gr.Role)
		}
		if err != nil {
			return fmt.Errorf("bind role %q to group %q: %w", gr.Role, gr.Group, err)
		}
		log.Info("group has role", "tenant", t.Code, "group", gr.Group, "role", gr.Role)
	}
	return st.EnsureAdminGroups(ctx)
}

// reencryptTime is the maximum time of one run of the re-encryption, and
// of one run of the search index task.
const reencryptTime = 2 * time.Minute

// reencrypt encrypts the values that use a retired data key again with
// the current key, in batches. A large tenant needs more than one run.
func reencrypt(ctx context.Context, st *store.Store, log *slog.Logger) {
	cctx, cancel := context.WithTimeout(ctx, reencryptTime)
	defer cancel()
	total := 0
	for {
		n, err := st.ReencryptBatch(cctx)
		total += n
		if err != nil {
			if ctx.Err() == nil && cctx.Err() == nil {
				log.Warn("re-encryption", "error", err)
			}
			break
		}
		if n == 0 {
			break
		}
	}
	if total > 0 {
		log.Info("values encrypted with the current key", "values", total)
	}
}

// reindex makes the search index of the nodes with an old index format
// again, in batches.
func reindex(ctx context.Context, st *store.Store, log *slog.Logger) {
	cctx, cancel := context.WithTimeout(ctx, reencryptTime)
	defer cancel()
	total := 0
	for {
		n, err := st.ReindexBatch(cctx)
		total += n
		if err != nil {
			if ctx.Err() == nil && cctx.Err() == nil {
				log.Warn("search index", "error", err)
			}
			break
		}
		if n == 0 {
			break
		}
	}
	if total > 0 {
		log.Info("search index made again", "nodes", total)
	}
}

// cleanup removes expired sessions and login states at intervals.
func cleanup(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		if err := st.DeleteExpired(cctx); err != nil && ctx.Err() == nil {
			log.Warn("remove expired sessions", "error", err)
		}
		cancel()
		reencrypt(ctx, st, log)
		reindex(ctx, st, log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
