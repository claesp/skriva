package store_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/clpe/skriva/internal/db"
	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/testdb"
)

func newStore(t *testing.T) *store.Store {
	return store.New(testdb.New(t))
}

func login(t *testing.T, st *store.Store, name string, groups ...string) *store.User {
	t.Helper()
	u, err := st.SyncLogin(context.Background(), store.Identity{
		Issuer: "https://idp.example", Subject: "sub-" + name, Username: name, Groups: groups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMigrateTwice(t *testing.T) {
	pool := testdb.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}

func TestSyncLoginGroups(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	u := login(t, st, "alice", "a", "b")
	local, err := st.CreateLocalGroup(ctx, "local-team", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddLocalGroupMember(ctx, local, u.ID); err != nil {
		t.Fatal(err)
	}

	// The second login removes group "a" and adds group "c". The local
	// group does not change.
	u2 := login(t, st, "alice", "b", "c")
	if u2.ID != u.ID {
		t.Fatalf("second login made a new user")
	}
	groups, err := st.UserGroups(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Source+":"+g.Name)
	}
	want := []string{"local:local-team", "oidc:b", "oidc:c"}
	if !slices.Equal(names, want) {
		t.Fatalf("groups = %v, want %v", names, want)
	}
}

func TestPermissions(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	u := login(t, st, "bob", "writers")

	perms, err := st.UserPermissions(ctx, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(perms) != 0 {
		t.Fatalf("new user has permissions: %v", perms.List())
	}

	perms, _ = st.UserPermissions(ctx, u.ID, "viewer")
	if !perms.Has(rbac.PageRead) || perms.Has(rbac.PageEdit) {
		t.Fatalf("default role viewer gives %v", perms.List())
	}

	gid, err := st.EnsureOIDCGroup(ctx, "writers")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToGroup(ctx, "editor", gid); err != nil {
		t.Fatal(err)
	}
	perms, _ = st.UserPermissions(ctx, u.ID, "")
	if !perms.Has(rbac.PageEdit) || perms.Has(rbac.PageDelete) {
		t.Fatalf("editor group gives %v", perms.List())
	}

	if err := st.CreateRole(ctx, "deleter", "", []rbac.Permission{rbac.PageDelete}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, "deleter", u.ID); err != nil {
		t.Fatal(err)
	}
	// A second binding of the same role is not an error.
	if err := st.BindRoleToUser(ctx, "deleter", u.ID); err != nil {
		t.Fatal(err)
	}
	perms, _ = st.UserPermissions(ctx, u.ID, "")
	if !perms.Has(rbac.PageDelete) || perms.Has(rbac.AdminManage) {
		t.Fatalf("custom role gives %v", perms.List())
	}

	if err := st.BindRoleToUser(ctx, "no-such-role", u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding to unknown role: %v", err)
	}
	if err := st.BindRoleToUser(ctx, "viewer", 999999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding to unknown user: %v", err)
	}

	// Remove the user from the group. The editor permissions go away.
	login(t, st, "bob")
	perms, _ = st.UserPermissions(ctx, u.ID, "")
	if perms.Has(rbac.PageEdit) {
		t.Fatalf("permissions stay after group removal: %v", perms.List())
	}
}

func TestBuiltinRolesArePermanent(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	roles, err := st.Roles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if !r.Builtin {
			continue
		}
		if err := st.DeleteRole(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("delete built-in role %s: %v", r.Name, err)
		}
	}
	if err := st.CreateRole(ctx, "admin", "", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate role name: %v", err)
	}
}

func TestOIDCGroupsAreNotEditable(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	u := login(t, st, "carol")
	gid, err := st.EnsureOIDCGroup(ctx, "idp-group")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddLocalGroupMember(ctx, gid, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("add member to OIDC group: %v", err)
	}
	if err := st.DeleteGroup(ctx, gid); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete OIDC group: %v", err)
	}
	again, err := st.EnsureOIDCGroup(ctx, "idp-group")
	if err != nil || again != gid {
		t.Fatalf("EnsureOIDCGroup second call = %d, %v", again, err)
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	u := login(t, st, "dave")

	token, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Session(ctx, token, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.ID != u.ID || sess.CSRFToken == "" || !sess.Permissions.Has(rbac.PageRead) {
		t.Fatalf("unexpected session: %+v", sess)
	}
	if _, err := st.Session(ctx, token+"x", "viewer"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong token: %v", err)
	}

	// Disabling the user removes the session.
	if err := st.SetUserDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, token, "viewer"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session of disabled user: %v", err)
	}
	if err := st.SetUserDisabled(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}

	short, err := st.CreateSession(ctx, u.ID, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, short, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLoginStateOneTime(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	in := store.LoginState{Nonce: "n", CodeVerifier: "v", ReturnTo: "/wiki/x"}
	if err := st.SaveLoginState(ctx, "state-1", in, time.Minute); err != nil {
		t.Fatal(err)
	}
	out, err := st.TakeLoginState(ctx, "state-1")
	if err != nil || *out != in {
		t.Fatalf("TakeLoginState = %+v, %v", out, err)
	}
	if _, err := st.TakeLoginState(ctx, "state-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}

	if err := st.SaveLoginState(ctx, "state-2", in, -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TakeLoginState(ctx, "state-2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired state: %v", err)
	}
}

func TestPagesAndRevisions(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	u := login(t, st, "erin")

	if err := st.CreatePage(ctx, "home", "Home", "Hello world", "first", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePage(ctx, "home", "Home", "again", "", u.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate page: %v", err)
	}
	if err := st.UpdatePage(ctx, "home", 1, "Home", "Hello again", "second", u.ID); err != nil {
		t.Fatal(err)
	}
	// An update from version 1 is now a conflict.
	if err := st.UpdatePage(ctx, "home", 1, "Home", "lost update", "", u.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err := st.UpdatePage(ctx, "nothing", 1, "X", "", "", u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of missing page: %v", err)
	}

	p, err := st.Page(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 2 || p.Body != "Hello again" || p.UpdatedBy != "erin" {
		t.Fatalf("unexpected page: %+v", p)
	}
	revs, err := st.Revisions(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 || revs[0].Version != 2 || revs[1].Comment != "first" {
		t.Fatalf("unexpected revisions: %+v", revs)
	}
	r1, err := st.Revision(ctx, "home", 1)
	if err != nil || r1.Body != "Hello world" {
		t.Fatalf("revision 1 = %+v, %v", r1, err)
	}

	res, err := st.SearchPages(ctx, "again", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Slug != "home" {
		t.Fatalf("search results: %+v", res)
	}
	// websearch_to_tsquery accepts any text. Bad syntax is not an error.
	if _, err := st.SearchPages(ctx, `"unclosed OR -( & |`, 10); err != nil {
		t.Fatalf("search with odd syntax: %v", err)
	}

	if err := st.DeletePage(ctx, "home"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Page(ctx, "home"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("page after delete: %v", err)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	u := login(t, st, "frank")
	if err := st.CreatePage(ctx, "race", "Race", "v1", "", u.ID); err != nil {
		t.Fatal(err)
	}

	// Many writers start from version 1. Only one can win.
	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = st.UpdatePage(ctx, "race", 1, "Race", "v2", "", u.ID)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, store.ErrConflict):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d updates succeeded, want 1", ok)
	}
}
