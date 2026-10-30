package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/clpe/skriva/internal/db"
	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/testdb"
)

var ctx = context.Background()

func newStore(t *testing.T) *store.Store {
	return store.New(testdb.New(t), testdb.Box(t))
}

func defaultTenant(t *testing.T, st *store.Store) *store.Tenant {
	t.Helper()
	tn, err := st.DefaultTenant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

func settings(name string) store.TenantSettings {
	return store.TenantSettings{
		Name: name, OIDCScopes: []string{"openid"}, OIDCGroupsClaim: "groups",
		DefaultRole: "viewer", SessionTTL: time.Hour, MaxDocBytes: 1 << 20,
	}
}

func newTenant(t *testing.T, st *store.Store, code string) *store.Tenant {
	t.Helper()
	tn, err := st.CreateTenant(ctx, code, settings("Tenant "+code), "")
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

func login(t *testing.T, st *store.Store, tn *store.Tenant, name string, groups ...string) *store.User {
	t.Helper()
	u, err := st.SyncLogin(ctx, &tn.ID, store.Identity{
		Issuer: "https://idp.example", Subject: "sub-" + name, Username: name, Groups: groups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func defaultSpace(t *testing.T, st *store.Store, tn *store.Tenant) *store.Space {
	t.Helper()
	sp, err := st.Space(ctx, tn.ID, "default0")
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestMigrateTwice(t *testing.T) {
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}

func TestDefaultTenantAndSpace(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	if tn.Code != "dflt" || tn.Name != "Default" || tn.DefaultRole != "viewer" {
		t.Fatalf("unexpected default tenant: %+v", tn)
	}
	sp := defaultSpace(t, st, tn)
	if sp.Name != "Default" {
		t.Fatalf("unexpected default space: %+v", sp)
	}
	if err := st.DeleteTenant(ctx, tn.ID); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("delete default tenant: %v", err)
	}
}

func TestTenantCRUDAndSecret(t *testing.T) {
	st := newStore(t)
	ts := settings("Acme")
	ts.AdminGroup = "acme-admins"
	tn, err := st.CreateTenant(ctx, "acme", ts, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !tn.HasClientSecret {
		t.Fatal("tenant has no secret")
	}
	if got, err := st.ClientSecret(ctx, tn); err != nil || got != "s3cret" {
		t.Fatalf("ClientSecret = %q, %v", got, err)
	}
	if _, err := st.CreateTenant(ctx, "acme", ts, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate code: %v", err)
	}
	bad := settings("Bad")
	bad.DefaultRole = "no-such-role"
	if _, err := st.CreateTenant(ctx, "badr", bad, ""); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown default role: %v", err)
	}

	// The admin group has the admin role.
	u := login(t, st, tn, "ann", "acme-admins")
	perms, _ := st.UserPermissions(ctx, tn.ID, u.ID)
	if !perms.Has(rbac.TenantManage) {
		t.Fatalf("admin group member has %v", perms.List())
	}

	// nil keeps the secret.
	ts.Name = "Acme Inc"
	tn, err = st.UpdateTenant(ctx, tn.ID, ts, nil)
	if err != nil || tn.Name != "Acme Inc" || !tn.HasClientSecret {
		t.Fatalf("UpdateTenant = %+v, %v", tn, err)
	}
	empty := ""
	tn, err = st.UpdateTenant(ctx, tn.ID, ts, &empty)
	if err != nil || tn.HasClientSecret {
		t.Fatalf("remove secret: %+v, %v", tn, err)
	}

	if err := st.DeleteTenant(ctx, tn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TenantByCode(ctx, "acme"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tenant after delete: %v", err)
	}
}

func TestDisabledTenantHasNoSessions(t *testing.T) {
	st := newStore(t)
	tn := newTenant(t, st, "acme")
	u := login(t, st, tn, "ann")
	token, err := st.CreateSession(ctx, &tn.ID, u.ID, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ts := tn.TenantSettings
	ts.Disabled = true
	if _, err := st.UpdateTenant(ctx, tn.ID, ts, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, token, &tn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session in disabled tenant: %v", err)
	}
}

func TestSyncLoginGroups(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)

	u := login(t, st, tn, "alice", "a", "b")
	local, err := st.CreateLocalGroup(ctx, tn.ID, "local-team", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddLocalGroupMember(ctx, tn.ID, local, u.ID); err != nil {
		t.Fatal(err)
	}
	// A second add is not an error.
	if err := st.AddLocalGroupMember(ctx, tn.ID, local, u.ID); err != nil {
		t.Fatal(err)
	}

	// The second login removes group "a" and adds group "c". The local
	// group does not change.
	u2 := login(t, st, tn, "alice", "b", "c")
	if u2.ID != u.ID {
		t.Fatalf("second login made a new user")
	}
	groups, err := st.UserGroups(ctx, tn.ID, u.ID)
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

func TestSameSubjectInTwoTenants(t *testing.T) {
	st := newStore(t)
	a := defaultTenant(t, st)
	b := newTenant(t, st, "bbbb")
	ua := login(t, st, a, "same")
	ub := login(t, st, b, "same")
	if ua.ID == ub.ID {
		t.Fatal("two tenants share one user")
	}
	sys, err := st.SyncLogin(ctx, nil, store.Identity{Issuer: "https://idp.example", Subject: "sub-same", Username: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if sys.ID == ua.ID || sys.TenantID != nil {
		t.Fatalf("system user = %+v", sys)
	}
	sys2, _ := st.SyncLogin(ctx, nil, store.Identity{Issuer: "https://idp.example", Subject: "sub-same", Username: "same"})
	if sys2.ID != sys.ID {
		t.Fatal("second system login made a new user")
	}
}

func TestTenantIsolation(t *testing.T) {
	st := newStore(t)
	a := defaultTenant(t, st)
	b := newTenant(t, st, "bbbb")
	ua := login(t, st, a, "ann")
	ub := login(t, st, b, "ben")
	ga, _ := st.CreateLocalGroup(ctx, a.ID, "team", "")
	gb, _ := st.CreateLocalGroup(ctx, b.ID, "team", "")

	// Tenant b cannot see or change the data of tenant a.
	if _, err := st.User(ctx, b.ID, ua.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("read user of other tenant: %v", err)
	}
	if err := st.SetUserDisabled(ctx, b.ID, ua.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disable user of other tenant: %v", err)
	}
	if _, err := st.Group(ctx, b.ID, ga); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("read group of other tenant: %v", err)
	}
	if err := st.AddLocalGroupMember(ctx, b.ID, gb, ua.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("add user of other tenant to group: %v", err)
	}
	if err := st.AddLocalGroupMember(ctx, b.ID, ga, ub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("add user to group of other tenant: %v", err)
	}
	if err := st.BindRoleToUser(ctx, b.ID, "admin", ua.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bind role to user of other tenant: %v", err)
	}
	if err := st.BindRoleToGroup(ctx, b.ID, "admin", ga); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bind role to group of other tenant: %v", err)
	}
	if err := st.DeleteGroup(ctx, b.ID, ga); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete group of other tenant: %v", err)
	}

	// Custom roles are visible only in their tenant.
	if err := st.CreateRole(ctx, a.ID, "deleter", "", []rbac.Permission{rbac.DocDelete}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, b.ID, "deleter", ub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bind custom role of other tenant: %v", err)
	}
	if err := st.CreateRole(ctx, b.ID, "deleter", "", nil); err != nil {
		t.Fatalf("same custom role name in other tenant: %v", err)
	}
	roles, _ := st.Roles(ctx, b.ID)
	n := 0
	for _, r := range roles {
		if r.Name == "deleter" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("tenant b sees %d roles with name deleter", n)
	}

	// Spaces and nodes.
	if _, err := st.Space(ctx, b.ID, "default0"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tenant b sees default space: %v", err)
	}
	spA := defaultSpace(t, st, a)
	id, err := st.CreateNode(ctx, spA.ID, store.NewNode{Kind: store.KindDocument, Title: "Secret plans", Body: "zebra", AuthorID: ua.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSpace(ctx, b.ID, "bspace00", "B", ""); err != nil {
		t.Fatal(err)
	}
	spB, _ := st.Space(ctx, b.ID, "bspace00")
	if _, err := st.Node(ctx, spB.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("node from other space: %v", err)
	}
	if res, _ := st.Search(ctx, b.ID, 0, "zebra", 10); len(res) != 0 {
		t.Fatalf("search in tenant b finds tenant a data: %+v", res)
	}
	if res, _ := st.Search(ctx, a.ID, 0, "zebra", 10); len(res) != 1 || res[0].SpaceSlug != "default0" {
		t.Fatalf("search in tenant a: %+v", res)
	}
}

func TestPermissions(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	u := login(t, st, tn, "bob", "writers")

	perms, err := st.UserPermissions(ctx, tn.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !perms.Has(rbac.DocRead) || perms.Has(rbac.DocEdit) {
		t.Fatalf("default role viewer gives %v", perms.List())
	}

	gid, err := st.EnsureOIDCGroup(ctx, tn.ID, "writers")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToGroup(ctx, tn.ID, "editor", gid); err != nil {
		t.Fatal(err)
	}
	perms, _ = st.UserPermissions(ctx, tn.ID, u.ID)
	if !perms.Has(rbac.DocEdit) || perms.Has(rbac.DocDelete) {
		t.Fatalf("editor group gives %v", perms.List())
	}

	if err := st.CreateRole(ctx, tn.ID, "deleter", "", []rbac.Permission{rbac.DocDelete}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "deleter", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "deleter", u.ID); err != nil {
		t.Fatalf("second binding: %v", err)
	}
	perms, _ = st.UserPermissions(ctx, tn.ID, u.ID)
	if !perms.Has(rbac.DocDelete) || perms.Has(rbac.TenantManage) {
		t.Fatalf("custom role gives %v", perms.List())
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "no-such-role", u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding to unknown role: %v", err)
	}

	// Remove the user from the group. The editor permissions go away.
	login(t, st, tn, "bob")
	perms, _ = st.UserPermissions(ctx, tn.ID, u.ID)
	if perms.Has(rbac.DocEdit) {
		t.Fatalf("permissions stay after group removal: %v", perms.List())
	}

	// A tenant without a default role gives no permissions.
	ts := tn.TenantSettings
	ts.DefaultRole = ""
	if _, err := st.UpdateTenant(ctx, tn.ID, ts, nil); err != nil {
		t.Fatal(err)
	}
	perms, _ = st.UserPermissions(ctx, tn.ID, u.ID)
	if perms.Has(rbac.DocRead) {
		t.Fatalf("no default role gives %v", perms.List())
	}
}

func TestBuiltinRolesArePermanent(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	roles, err := st.Roles(ctx, tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if err := st.DeleteRole(ctx, tn.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("delete built-in role %s: %v", r.Name, err)
		}
	}
	if err := st.CreateRole(ctx, tn.ID, "admin", "", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("custom role with built-in name: %v", err)
	}
}

func TestSessions(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	other := newTenant(t, st, "othr")
	u := login(t, st, tn, "dave")

	token, err := st.CreateSession(ctx, &tn.ID, u.ID, "id-token", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Session(ctx, token, &tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.ID != u.ID || sess.CSRFToken == "" || !sess.Permissions.Has(rbac.DocRead) || sess.System() {
		t.Fatalf("unexpected session: %+v", sess)
	}
	if _, err := st.Session(ctx, token, &other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session in other tenant: %v", err)
	}
	if _, err := st.Session(ctx, token, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tenant session as system session: %v", err)
	}
	if _, err := st.Session(ctx, token+"x", &tn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong token: %v", err)
	}

	if err := st.SetUserDisabled(ctx, tn.ID, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, token, &tn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session of disabled user: %v", err)
	}
	_ = st.SetUserDisabled(ctx, tn.ID, u.ID, false)

	short, _ := st.CreateSession(ctx, &tn.ID, u.ID, "", -time.Second)
	if _, err := st.Session(ctx, short, &tn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}

	token, _ = st.CreateSession(ctx, &tn.ID, u.ID, "id-token-2", time.Hour)
	if idt, err := st.DeleteSession(ctx, token); err != nil || idt != "id-token-2" {
		t.Fatalf("DeleteSession = %q, %v", idt, err)
	}
	if idt, err := st.DeleteSession(ctx, token); err != nil || idt != "" {
		t.Fatalf("second DeleteSession = %q, %v", idt, err)
	}

	// A system session.
	sys, _ := st.SyncLogin(ctx, nil, store.Identity{Issuer: "i", Subject: "root", Username: "root"})
	token, _ = st.CreateSession(ctx, nil, sys.ID, "", time.Hour)
	sess, err = st.Session(ctx, token, nil)
	if err != nil || !sess.System() || len(sess.Permissions) != 0 {
		t.Fatalf("system session = %+v, %v", sess, err)
	}
	if _, err := st.Session(ctx, token, &tn.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("system session as tenant session: %v", err)
	}
}

func TestLoginState(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	in := store.LoginState{Nonce: "n", CodeVerifier: "v", ReturnTo: "/dflt/"}
	if err := st.SaveLoginState(ctx, &tn.ID, "state-1", in, time.Minute); err != nil {
		t.Fatal(err)
	}
	out, err := st.TakeLoginState(ctx, &tn.ID, "state-1")
	if err != nil || *out != in {
		t.Fatalf("TakeLoginState = %+v, %v", out, err)
	}
	if _, err := st.TakeLoginState(ctx, &tn.ID, "state-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}
	_ = st.SaveLoginState(ctx, &tn.ID, "state-2", in, -time.Second)
	if _, err := st.TakeLoginState(ctx, &tn.ID, "state-2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired state: %v", err)
	}
	// A state from a tenant login is not valid for a system login.
	_ = st.SaveLoginState(ctx, &tn.ID, "state-3", in, time.Minute)
	if _, err := st.TakeLoginState(ctx, nil, "state-3"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("state of other scope: %v", err)
	}
}

func TestPublicID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := store.NewPublicID()
		if !store.ValidPublicID(id) {
			t.Fatalf("invalid public ID %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate public ID %q", id)
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "short", "abcdefghijk!", "abcdefghijklm", "abcdef-hijkl"} {
		if store.ValidPublicID(bad) {
			t.Errorf("ValidPublicID(%q) = true", bad)
		}
	}
}

func TestNodeTree(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "erin")

	mk := func(parent, kind, title string) string {
		t.Helper()
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{
			ParentPublicID: parent, Kind: kind, Title: title, Body: "text of " + title, AuthorID: u.ID,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return id
	}
	folder := mk("", store.KindFolder, "Guides")
	sub := mk(folder, store.KindFolder, "Setup")
	doc := mk(sub, store.KindDocument, "Install")
	child := mk(doc, store.KindDocument, "Install on Linux")

	// A folder cannot be a child of a document.
	if _, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: doc, Kind: store.KindFolder, Title: "x"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("folder in document: %v", err)
	}
	if _, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: "000000000000", Kind: store.KindDocument, Title: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown parent: %v", err)
	}

	n, err := st.Node(ctx, sp.ID, folder)
	if err != nil || n.Body != "" || !n.IsFolder() {
		t.Fatalf("folder = %+v, %v", n, err)
	}

	c, _ := st.Node(ctx, sp.ID, child)
	anc, err := st.Ancestors(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, a := range anc {
		titles = append(titles, a.Title)
	}
	if !slices.Equal(titles, []string{"Guides", "Setup", "Install"}) {
		t.Fatalf("ancestors = %v", titles)
	}

	// Moves that make a cycle fail.
	if err := st.MoveNode(ctx, sp.ID, folder, sub); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("move into own subtree: %v", err)
	}
	if err := st.MoveNode(ctx, sp.ID, doc, doc); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("move into itself: %v", err)
	}
	if err := st.MoveNode(ctx, sp.ID, doc, child); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("move into own child: %v", err)
	}
	if err := st.MoveNode(ctx, sp.ID, sub, doc); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("move folder into document: %v", err)
	}
	// A valid move to the root.
	if err := st.MoveNode(ctx, sp.ID, child, ""); err != nil {
		t.Fatal(err)
	}
	roots, _ := st.Children(ctx, sp.ID, nil)
	if len(roots) != 2 || roots[0].Title != "Guides" {
		t.Fatalf("roots = %+v", roots)
	}

	all, _ := st.SpaceNodes(ctx, sp.ID)
	tree := store.BuildTree(all, n.ID)
	if len(tree) != 2 || !tree[0].Current || len(tree[0].Children) != 1 || tree[0].Children[0].Title != "Setup" {
		t.Fatalf("tree = %+v", tree)
	}

	// Delete removes the subtree.
	f, _ := st.Node(ctx, sp.ID, folder)
	if cnt, _ := st.CountDescendants(ctx, f.ID); cnt != 2 {
		t.Fatalf("descendants = %d", cnt)
	}
	if err := st.DeleteNode(ctx, sp.ID, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Node(ctx, sp.ID, doc); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("document in deleted folder: %v", err)
	}
	if _, err := st.Node(ctx, sp.ID, child); err != nil {
		t.Fatalf("moved document was deleted: %v", err)
	}
}

func TestDeepNesting(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	parent := ""
	const depth = 300
	for i := range depth {
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: store.KindDocument, Title: fmt.Sprint("Level ", i)})
		if err != nil {
			t.Fatalf("level %d: %v", i, err)
		}
		parent = id
	}
	last, _ := st.Node(ctx, sp.ID, parent)
	anc, err := st.Ancestors(ctx, last.ID)
	if err != nil || len(anc) != depth-1 || anc[0].Title != "Level 0" {
		t.Fatalf("ancestors: %d, %v", len(anc), err)
	}
	all, _ := st.SpaceNodes(ctx, sp.ID)
	tree := store.BuildTree(all, last.ID)
	d := 0
	for n := tree; len(n) > 0; n = n[0].Children {
		d++
	}
	if d != depth {
		t.Fatalf("tree depth %d", d)
	}
}

func TestConcurrentMovesMakeNoCycle(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	a, _ := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindFolder, Title: "A"})
	b, _ := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindFolder, Title: "B"})

	// Move A into B and B into A at the same time. Only one can win.
	for range 20 {
		var wg sync.WaitGroup
		var e1, e2 error
		wg.Add(2)
		go func() { defer wg.Done(); e1 = st.MoveNode(ctx, sp.ID, a, b) }()
		go func() { defer wg.Done(); e2 = st.MoveNode(ctx, sp.ID, b, a) }()
		wg.Wait()
		if e1 == nil && e2 == nil {
			t.Fatal("both moves succeeded: the tree has a cycle")
		}
		// Reset.
		_ = st.MoveNode(ctx, sp.ID, a, "")
		_ = st.MoveNode(ctx, sp.ID, b, "")
	}
}

func TestNodeVersions(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "erin")
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Home", Slug: "home", Body: "Hello world", Comment: "first", AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNode(ctx, sp.ID, id, 1, "Home", "home", "Hello again", "second", u.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNode(ctx, sp.ID, id, 1, "Home", "home", "lost", "", u.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err := st.UpdateNode(ctx, sp.ID, "000000000000", 1, "X", "", "", "", u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of missing node: %v", err)
	}
	n, _ := st.Node(ctx, sp.ID, id)
	if n.Version != 2 || n.Body != "Hello again" || n.UpdatedBy != "erin" {
		t.Fatalf("node = %+v", n)
	}
	revs, _ := st.Revisions(ctx, n.ID)
	if len(revs) != 2 || revs[1].Comment != "first" {
		t.Fatalf("revisions = %+v", revs)
	}
	r1, err := st.Revision(ctx, n.ID, 1)
	if err != nil || r1.Body != "Hello world" {
		t.Fatalf("revision 1 = %+v, %v", r1, err)
	}
	if res, _ := st.NodesBySlug(ctx, sp.ID, "home"); len(res) != 1 || res[0].PublicID != id {
		t.Fatalf("NodesBySlug = %+v", res)
	}
	if _, err := st.Search(ctx, tn.ID, sp.ID, `"unclosed OR -( & |`, 10); err != nil {
		t.Fatalf("search with odd syntax: %v", err)
	}

	// Concurrent updates: only one can win.
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = st.UpdateNode(ctx, sp.ID, id, 2, "Home", "home", "v3", "", u.ID)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal(err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d updates succeeded", ok)
	}
}
