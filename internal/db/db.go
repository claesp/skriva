// Package db opens the PostgreSQL connection pool and applies schema
// migrations.
package db

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claesp/skriva/internal/version"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLockID is the key for the PostgreSQL advisory lock. The lock
// makes sure that only one process applies migrations at a time.
const migrationLockID = 0x736b72697661 // "skriva"

// Open makes a connection pool and makes sure that the database responds.
// roots are the trusted CA certificates for TLS, or nil for the system
// certificates. The parameter sslrootcert of the URL replaces roots.
func Open(ctx context.Context, url string, roots *x509.CertPool) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if roots != nil {
		setRoots(cfg.ConnConfig.TLSConfig, roots)
		for _, fb := range cfg.ConnConfig.Fallbacks {
			setRoots(fb.TLSConfig, roots)
		}
	}
	if !verifiedTLS(cfg.ConnConfig) {
		slog.Warn("the connection to the database does not use verified TLS: " +
			"set sslmode=verify-full in SKRIVA_DATABASE_URL")
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 10 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// The database can start more slowly than the application.
	// Try again for a short time before we stop.
	deadline := time.Now().Add(30 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		slog.Warn("database is not available, trying again", "error", err)
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

// setRoots sets the trusted certificates, if the URL did not set them.
func setRoots(c *tls.Config, roots *x509.CertPool) {
	if c != nil && c.RootCAs == nil {
		c.RootCAs = roots
	}
}

// verifiedTLS reports whether each connection attempt uses TLS and
// verifies the certificate of the server (sslmode verify-ca or
// verify-full). A Unix socket needs no TLS.
func verifiedTLS(c *pgx.ConnConfig) bool {
	ok := func(host string, t *tls.Config) bool {
		if strings.HasPrefix(host, "/") {
			return true
		}
		return t != nil && (!t.InsecureSkipVerify || t.VerifyPeerCertificate != nil)
	}
	if !ok(c.Host, c.TLSConfig) {
		return false
	}
	for _, fb := range c.Fallbacks {
		if !ok(fb.Host, fb.TLSConfig) {
			return false
		}
	}
	return true
}

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil || v < 1 {
			return nil, fmt.Errorf("migration %q: the number must be 1 or more", name)
		}
		body, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, migration{version: v, name: name, sql: string(body), checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("two migrations have version %d", out[i].version)
		}
	}
	return out, nil
}

// SchemaVersion returns the schema version of the program: the number of
// its last migration.
func SchemaVersion() int {
	m, err := loadMigrations(migrationFS)
	if err != nil || len(m) == 0 {
		return 0
	}
	return m[len(m)-1].version
}

// Options of Upgrade.
type Options struct {
	// AppVersion is the version of the program. The database records it
	// with each migration.
	AppVersion string
	// Apply permits the migrations. If it is false and the database
	// needs migrations, Upgrade returns a *PendingError.
	Apply bool
}

// Result tells what Upgrade did.
type Result struct {
	// From is the schema version of the database before Upgrade, and To
	// the schema version after it. 0 is an empty database.
	From, To int
	// Applied are the names of the migrations that Upgrade applied.
	Applied []string
}

// NewerError tells that the database has a newer schema than the
// program. An older program must not change the data of a newer schema.
type NewerError struct {
	Database   int
	Program    int
	AppVersion string
}

func (e *NewerError) Error() string {
	from := ""
	if e.AppVersion != "" {
		from = fmt.Sprintf(" from Skriva %s", e.AppVersion)
	}
	return fmt.Sprintf("the database has schema version %d%s, but this program supports only "+
		"schema version %d: install the newer version of Skriva", e.Database, from, e.Program)
}

// PendingError tells that the database needs migrations, and that the
// options do not permit them.
type PendingError struct {
	From, To int
}

func (e *PendingError) Error() string {
	return fmt.Sprintf("the database has schema version %d and this program needs schema version %d: "+
		"run \"skriva migrate\" or set SKRIVA_AUTO_MIGRATE=true", e.From, e.To)
}

// Migrate applies all migrations that the database does not have. It is
// Upgrade with the version of the program.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := Upgrade(ctx, pool, Options{AppVersion: version.String(), Apply: true})
	return err
}

// Upgrade is the boot step of the program. It compares the schema
// version of the database with the schema version of the program:
//
//   - The same version: there is nothing to do.
//   - The database is older: Upgrade applies the missing migrations in
//     order, each in its own transaction, if opts.Apply is true.
//   - The database is newer: Upgrade returns a *NewerError. The program
//     must stop.
//
// Upgrade also makes sure that the migrations in the database are the
// same as the migrations of the program (a SHA-256 checksum). An
// advisory lock makes sure that only one process does this at a time.
func Upgrade(ctx context.Context, pool *pgxpool.Pool, opts Options) (Result, error) {
	return upgrade(ctx, pool, migrationFS, opts)
}

// appliedMigration is a row of schema_migrations.
type appliedMigration struct {
	name       string
	checksum   *string
	appVersion *string
}

func upgrade(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, opts Options) (Result, error) {
	var res Result
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return res, err
	}
	known := map[int]migration{}
	for _, m := range migrations {
		known[m.version] = m
		res.To = m.version
	}
	program := res.To

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return res, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return res, fmt.Errorf("get migration lock: %w", err)
	}
	defer func() {
		// Use a new context. The lock must be released also if ctx is cancelled.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
			slog.Error("release migration lock", "error", err)
		}
	}()

	// The program makes this table, not a migration. Do not change it:
	// all versions of the program must be able to read it.
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version     integer PRIMARY KEY,
		name        text NOT NULL,
		checksum    text,
		app_version text,
		applied_at  timestamptz NOT NULL DEFAULT now())`); err != nil {
		return res, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]appliedMigration{}
	rows, err := conn.Query(ctx, "SELECT version, name, checksum, app_version FROM schema_migrations")
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var v int
		var a appliedMigration
		if err := rows.Scan(&v, &a.name, &a.checksum, &a.appVersion); err != nil {
			rows.Close()
			return res, err
		}
		applied[v] = a
		res.From = max(res.From, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	if res.From > program {
		e := &NewerError{Database: res.From, Program: program}
		if a := applied[res.From]; a.appVersion != nil {
			e.AppVersion = *a.appVersion
		}
		return res, e
	}
	for v, a := range applied {
		m, ok := known[v]
		switch {
		case !ok:
			return res, fmt.Errorf("the database has migration %d (%s), but this program does not have it", v, a.name)
		case a.checksum == nil:
			// A database from before the checksums. Record the checksum.
			if _, err := conn.Exec(ctx, "UPDATE schema_migrations SET checksum = $2 WHERE version = $1",
				v, m.checksum); err != nil {
				return res, err
			}
		case *a.checksum != m.checksum:
			return res, fmt.Errorf("migration %s changed after the database applied it: "+
				"never change a migration, add a new one", m.name)
		}
	}

	var pending []migration
	for _, m := range migrations {
		if _, ok := applied[m.version]; !ok {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		res.To = res.From
		return res, nil
	}
	if !opts.Apply {
		res.To = res.From
		return res, &PendingError{From: res.From, To: program}
	}
	for _, m := range pending {
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				"INSERT INTO schema_migrations (version, name, checksum, app_version) VALUES ($1, $2, $3, $4)",
				m.version, m.name, m.checksum, opts.AppVersion)
			return err
		})
		if err != nil {
			return res, fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		res.Applied = append(res.Applied, m.name)
		slog.Info("applied migration", "name", m.name, "app_version", opts.AppVersion)
	}
	return res, nil
}
