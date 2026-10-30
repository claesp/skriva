// Command skriva runs the Skriva wiki server.
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

	"github.com/clpe/skriva/internal/auth"
	"github.com/clpe/skriva/internal/config"
	"github.com/clpe/skriva/internal/db"
	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	st := store.New(pool)
	if err := bootstrap(ctx, st, cfg, log); err != nil {
		return err
	}

	oidc := auth.New(auth.Config{
		Issuer:       cfg.OIDCIssuer,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.BaseURL.String() + "/auth/callback",
		Scopes:       cfg.OIDCScopes,
		GroupsClaim:  cfg.OIDCGroupsClaim,
	})
	// Do the discovery early if the provider is available. If it is not
	// available, the first login tries again.
	if err := oidc.Ready(ctx); err != nil {
		log.Warn("OIDC discovery failed, will try again at first login", "error", err)
	}

	handler, err := web.New(st, oidc, web.Options{
		BaseURL:       cfg.BaseURL,
		SecureCookies: cfg.SecureCookies(),
		SessionTTL:    cfg.SessionTTL,
		DefaultRole:   cfg.DefaultRole,
		MaxPageBytes:  cfg.MaxPageBytes,
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
		log.Info("server started", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL.String())
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
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

// bootstrap gives roles to the groups in SKRIVA_ADMIN_GROUP and
// SKRIVA_GROUP_ROLES. Thus a new installation has an administrator, and an
// administrator can always get access again with a restart.
func bootstrap(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger) error {
	if cfg.DefaultRole != "" {
		ok, err := st.RoleExists(ctx, cfg.DefaultRole)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("SKRIVA_DEFAULT_ROLE: role %q does not exist", cfg.DefaultRole)
		}
	}
	for _, gr := range cfg.GroupRoles {
		id, err := st.EnsureOIDCGroup(ctx, gr.Group)
		if err != nil {
			return fmt.Errorf("create group %q: %w", gr.Group, err)
		}
		err = st.BindRoleToGroup(ctx, gr.Role, id)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("SKRIVA_GROUP_ROLES: role %q does not exist", gr.Role)
		}
		if err != nil {
			return fmt.Errorf("bind role %q to group %q: %w", gr.Role, gr.Group, err)
		}
		log.Info("group has role", "group", gr.Group, "role", gr.Role)
	}
	return nil
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
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
