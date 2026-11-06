package db

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newDB makes an empty database. The test stops (skips) if
// SKRIVA_TEST_DATABASE_URL is not set. (The package testdb uses this
// package, thus the test cannot use it.)
func newDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("SKRIVA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("SKRIVA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := "t_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	pool, err := Open(ctx, u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if conn, err := pgx.Connect(ctx, base); err == nil {
			_, _ = conn.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
			_ = conn.Close(ctx)
		}
	})
	return pool
}

func migrations(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, sql := range files {
		fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	}
	return fsys
}

// The boot step applies new migrations, and stops for a newer or a
// changed schema.
func TestUpgrade(t *testing.T) {
	pool := newDB(t)
	ctx := context.Background()
	one := map[string]string{"0001_a.sql": "CREATE TABLE a (id int)"}
	two := map[string]string{"0001_a.sql": one["0001_a.sql"], "0002_b.sql": "CREATE TABLE b (id int)"}

	// Without Apply, the program does not change the database.
	_, err := upgrade(ctx, pool, migrations(two), Options{AppVersion: "1.0.abc"})
	var pending *PendingError
	if !errors.As(err, &pending) || pending.From != 0 || pending.To != 2 {
		t.Fatalf("upgrade without Apply = %v", err)
	}

	res, err := upgrade(ctx, pool, migrations(one), Options{AppVersion: "0.1.abc", Apply: true})
	if err != nil || res.From != 0 || res.To != 1 || len(res.Applied) != 1 {
		t.Fatalf("first upgrade = %+v, %v", res, err)
	}
	res, err = upgrade(ctx, pool, migrations(two), Options{AppVersion: "1.0.def", Apply: true})
	if err != nil || res.From != 1 || res.To != 2 || len(res.Applied) != 1 || res.Applied[0] != "0002_b.sql" {
		t.Fatalf("second upgrade = %+v, %v", res, err)
	}
	var appVersion string
	if err := pool.QueryRow(ctx, "SELECT app_version FROM schema_migrations WHERE version = 2").Scan(&appVersion); err != nil || appVersion != "1.0.def" {
		t.Fatalf("app_version = %q, %v", appVersion, err)
	}
	res, err = upgrade(ctx, pool, migrations(two), Options{AppVersion: "1.0.def"})
	if err != nil || res.From != 2 || res.To != 2 || len(res.Applied) != 0 {
		t.Fatalf("upgrade of a current database = %+v, %v", res, err)
	}

	// An older program stops.
	_, err = upgrade(ctx, pool, migrations(one), Options{AppVersion: "0.1.abc", Apply: true})
	var newer *NewerError
	if !errors.As(err, &newer) || newer.Database != 2 || newer.Program != 1 || newer.AppVersion != "1.0.def" {
		t.Fatalf("upgrade with an older program = %v", err)
	}

	// A changed migration stops the program.
	changed := map[string]string{"0001_a.sql": "CREATE TABLE a (id bigint)", "0002_b.sql": two["0002_b.sql"]}
	if _, err := upgrade(ctx, pool, migrations(changed), Options{Apply: true}); err == nil || !strings.Contains(err.Error(), "0001_a.sql changed") {
		t.Fatalf("upgrade with a changed migration = %v", err)
	}

	// A database without checksums gets them.
	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET checksum = NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := upgrade(ctx, pool, migrations(two), Options{}); err != nil {
		t.Fatal(err)
	}
	var missing int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE checksum IS NULL").Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("migrations without a checksum: %d, %v", missing, err)
	}
}

func TestLoadMigrations(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"bad name":    {"init.sql": ""},
		"zero":        {"0000_a.sql": ""},
		"same number": {"0001_a.sql": "", "1_b.sql": ""},
	} {
		if _, err := loadMigrations(migrations(files)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if SchemaVersion() < 1 {
		t.Fatalf("SchemaVersion = %d", SchemaVersion())
	}
}

func TestVerifiedTLS(t *testing.T) {
	for url, want := range map[string]bool{
		"postgres://u@db/skriva":                                    false, // prefer
		"postgres://u@db/skriva?sslmode=disable":                    false,
		"postgres://u@db/skriva?sslmode=require":                    false,
		"postgres://u@db/skriva?sslmode=verify-ca":                  true,
		"postgres://u@db/skriva?sslmode=verify-full":                true,
		"postgres://u@db1,db2/skriva?sslmode=verify-full":           true,
		"postgres://u@/skriva?host=/run/postgresql&sslmode=disable": true,
	} {
		cfg, err := pgx.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		if got := verifiedTLS(cfg); got != want {
			t.Errorf("%s: verifiedTLS = %v, want %v", url, got, want)
		}
	}
}
