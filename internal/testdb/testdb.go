// Package testdb makes temporary databases for integration tests.
package testdb

import (
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claesp/skriva/internal/db"
	"github.com/claesp/skriva/internal/secret"
)

// New makes a new, migrated database and returns a pool for it. The test
// stops (skips) if SKRIVA_TEST_DATABASE_URL is not set. The function
// removes the database when the test is complete.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("SKRIVA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("SKRIVA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)

	name := "t_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Logf("drop database: %v", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Logf("drop database: %v", err)
		}
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// Box returns a secret.Box with a random key for tests.
func Box(t testing.TB) *secret.Box {
	t.Helper()
	key := make([]byte, secret.KeySize)
	_, _ = rand.Read(key)
	b, err := secret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
