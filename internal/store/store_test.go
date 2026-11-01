package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/clpe/skriva/internal/db"
	"github.com/clpe/skriva/internal/doc"
	"github.com/clpe/skriva/internal/rbac"
	"github.com/clpe/skriva/internal/store"
	"github.com/clpe/skriva/internal/testdb"
)

var ctx = context.Background()

// content returns the content of a document with one paragraph.
func content(text string) store.Content {
	t, _ := json.Marshal(text)
	n, err := doc.Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":`+string(t)+`}]}]}`), nil)
	if err != nil {
		panic(err)
	}
	return store.Content{Body: string(n.JSON()), PlainText: n.PlainText(), Files: n.Files()}
}

// bodyText returns the plain text of a body.
func bodyText(t *testing.T, body string) string {
	t.Helper()
	n, err := doc.Parse([]byte(body), nil)
	if err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return n.PlainText()
}

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
	if err := st.BindRoleToUser(ctx, b.ID, "admin", ua.ID, store.Scope{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bind role to user of other tenant: %v", err)
	}
	if err := st.BindRoleToGroup(ctx, b.ID, "admin", ga, store.Scope{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bind role to group of other tenant: %v", err)
	}
	if err := st.DeleteGroup(ctx, b.ID, ga); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete group of other tenant: %v", err)
	}

	// Custom roles are visible only in their tenant.
	if err := st.CreateRole(ctx, a.ID, "deleter", "", []rbac.Permission{rbac.DocDelete}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, b.ID, "deleter", ub.ID, store.Scope{}); !errors.Is(err, store.ErrNotFound) {
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
	id, err := st.CreateNode(ctx, spA.ID, store.NewNode{Kind: store.KindDocument, Title: "Secret plans", Content: content("zebra"), AuthorID: ua.ID})
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
	if res, _ := st.Search(ctx, b.ID, []int64{spB.ID}, "zebra", 10); len(res) != 0 {
		t.Fatalf("search in tenant b finds tenant a data: %+v", res)
	}
	if res, _ := st.Search(ctx, a.ID, []int64{spA.ID}, "zebra", 10); len(res) != 1 || res[0].SpaceSlug != "default0" {
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
	if err := st.BindRoleToGroup(ctx, tn.ID, "editor", gid, store.Scope{}); err != nil {
		t.Fatal(err)
	}
	perms, _ = st.UserPermissions(ctx, tn.ID, u.ID)
	if !perms.Has(rbac.DocEdit) || perms.Has(rbac.DocDelete) {
		t.Fatalf("editor group gives %v", perms.List())
	}

	if err := st.CreateRole(ctx, tn.ID, "deleter", "", []rbac.Permission{rbac.DocDelete}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "deleter", u.ID, store.Scope{}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "deleter", u.ID, store.Scope{}); err != nil {
		t.Fatalf("second binding: %v", err)
	}
	perms, _ = st.UserPermissions(ctx, tn.ID, u.ID)
	if !perms.Has(rbac.DocDelete) || perms.Has(rbac.TenantManage) {
		t.Fatalf("custom role gives %v", perms.List())
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "no-such-role", u.ID, store.Scope{}); !errors.Is(err, store.ErrNotFound) {
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
			ParentPublicID: parent, Kind: kind, Title: title, Content: content("text of " + title), AuthorID: u.ID,
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
	if err != nil || bodyText(t, n.Body) != "" || !n.IsFolder() {
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

	// Delete moves the subtree into the trash.
	f, _ := st.Node(ctx, sp.ID, folder)
	if cnt, _ := st.CountDescendants(ctx, f.ID); cnt != 2 {
		t.Fatalf("descendants = %d", cnt)
	}
	if err := st.TrashNode(ctx, sp.ID, folder, 0); err != nil {
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
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Home", Slug: "home", Content: content("Hello world"), Comment: "first", AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 1, Title: "Home", Slug: "home", Content: content("Hello again"), Comment: "second", AuthorID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 1, Title: "Home", Slug: "home", Content: content("lost"), AuthorID: u.ID}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err := st.UpdateNode(ctx, sp.ID, "000000000000", store.NodeUpdate{BaseVersion: 1, Title: "X", AuthorID: u.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of missing node: %v", err)
	}
	n, _ := st.Node(ctx, sp.ID, id)
	if n.Version != 2 || bodyText(t, n.Body) != "Hello again" || n.UpdatedBy != "erin" {
		t.Fatalf("node = %+v", n)
	}
	revs, _ := st.Revisions(ctx, n.ID)
	if len(revs) != 2 || revs[1].Comment != "first" {
		t.Fatalf("revisions = %+v", revs)
	}
	r1, err := st.Revision(ctx, n.ID, 1)
	if err != nil || bodyText(t, r1.Body) != "Hello world" {
		t.Fatalf("revision 1 = %+v, %v", r1, err)
	}
	if res, _ := st.NodesBySlug(ctx, sp.ID, "home"); len(res) != 1 || res[0].PublicID != id {
		t.Fatalf("NodesBySlug = %+v", res)
	}
	if _, err := st.Search(ctx, tn.ID, []int64{sp.ID}, `"unclosed OR -( & |`, 10); err != nil {
		t.Fatalf("search with odd syntax: %v", err)
	}

	// Concurrent updates: only one can win.
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 2, Title: "Home", Slug: "home", Content: content("v3"), AuthorID: u.ID})
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

// TestAllSpacePermissionsPerSpace makes sure that a space binding applies
// to its space in a query for all spaces. An earlier query gave the
// result of the first space for all spaces.
func TestAllSpacePermissionsPerSpace(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	def := defaultSpace(t, st, tn)
	if err := st.CreateSpace(ctx, tn.ID, "secret00", "Secret", ""); err != nil {
		t.Fatal(err)
	}
	none := ""
	if err := st.SetSpaceDefaultRole(ctx, tn.ID, "secret00", &none); err != nil {
		t.Fatal(err)
	}
	secret, _ := st.Space(ctx, tn.ID, "secret00")
	u := login(t, st, tn, "carol")
	if err := st.BindRoleToUser(ctx, tn.ID, "editor", u.ID, store.Scope{SpaceID: secret.ID}); err != nil {
		t.Fatal(err)
	}
	all, err := st.AllSpacePermissions(ctx, tn.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p := all[secret.ID]; !p.Has(rbac.DocEdit) {
		t.Fatalf("secret space: %v", p.List())
	}
	if p := all[def.ID]; p.Has(rbac.DocEdit) || !p.Has(rbac.DocRead) {
		t.Fatalf("default space: %v", p.List())
	}
	ids, err := st.ReadableSpaces(ctx, tn.ID, u.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("ReadableSpaces = %v, %v", ids, err)
	}
}

func TestSpaceTopLevelPermissions(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	pub := defaultSpace(t, st, tn)
	if err := st.CreateSpace(ctx, tn.ID, "private0", "Private", ""); err != nil {
		t.Fatal(err)
	}
	priv, _ := st.Space(ctx, tn.ID, "private0")
	carol := login(t, st, tn, "carol", "team")
	dave := login(t, st, tn, "dave")

	perms := func(u *store.User, sp *store.Space) rbac.Set {
		t.Helper()
		a, err := st.Access(ctx, tn.ID, sp.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a.Root()
	}

	// Without a space default role, the tenant default role applies.
	if !perms(carol, priv).Has(rbac.DocRead) {
		t.Fatal("tenant default role does not apply in the space")
	}

	// "No default role" makes the space private.
	none := ""
	if err := st.SetSpaceDefaultRole(ctx, tn.ID, "private0", &none); err != nil {
		t.Fatal(err)
	}
	if p := perms(carol, priv); len(p) != 0 {
		t.Fatalf("private space gives %v", p.List())
	}
	if !perms(carol, pub).Has(rbac.DocRead) {
		t.Fatal("the other space changed")
	}
	bad := "no-such-role"
	if err := st.SetSpaceDefaultRole(ctx, tn.ID, "private0", &bad); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("unknown space default role: %v", err)
	}

	// A space binding to a group gives document permissions only in that
	// space.
	gid, _ := st.EnsureOIDCGroup(ctx, tn.ID, "team")
	if err := st.BindRoleToGroup(ctx, tn.ID, "editor", gid, store.Scope{SpaceID: priv.ID}); err != nil {
		t.Fatal(err)
	}
	if p := perms(carol, priv); !p.Has(rbac.DocEdit) || p.Has(rbac.DocDelete) {
		t.Fatalf("space binding gives %v", p.List())
	}
	if perms(carol, pub).Has(rbac.DocEdit) {
		t.Fatal("space binding applies in a different space")
	}
	if tp, _ := st.UserPermissions(ctx, tn.ID, carol.ID); tp.Has(rbac.DocEdit) {
		t.Fatal("space binding gives tenant-level permissions")
	}

	// The admin role for one space gives only the document permissions.
	if err := st.BindRoleToUser(ctx, tn.ID, "admin", dave.ID, store.Scope{SpaceID: priv.ID}); err != nil {
		t.Fatal(err)
	}
	if p := perms(dave, priv); !p.Has(rbac.DocDelete) || p.Has(rbac.TenantManage) || p.Has(rbac.SpaceManage) {
		t.Fatalf("space admin binding gives %v", p.List())
	}

	// A binding without a space applies in all spaces, also in a private
	// space. Tenant permissions apply in each space.
	if err := st.BindRoleToUser(ctx, tn.ID, "maintainer", dave.ID, store.Scope{}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToUser(ctx, tn.ID, "admin", dave.ID, store.Scope{}); err != nil {
		t.Fatal(err)
	}
	if p := perms(dave, pub); !p.Has(rbac.DocDelete) || !p.Has(rbac.TenantManage) {
		t.Fatalf("tenant binding in space gives %v", p.List())
	}

	// A space default role gives only document permissions.
	adminRole := "admin"
	if err := st.SetSpaceDefaultRole(ctx, tn.ID, "default0", &adminRole); err != nil {
		t.Fatal(err)
	}
	if p := perms(carol, pub); !p.Has(rbac.DocDelete) || p.Has(rbac.TenantManage) {
		t.Fatalf("space default role admin gives %v", p.List())
	}

	readable, err := st.ReadableSpaces(ctx, tn.ID, carol.ID)
	if err != nil || len(readable) != 2 {
		t.Fatalf("readable spaces = %v, %v", readable, err)
	}
	stranger := login(t, st, tn, "eve")
	if readable, _ := st.ReadableSpaces(ctx, tn.ID, stranger.ID); len(readable) != 1 || readable[0] != pub.ID {
		t.Fatalf("readable spaces of eve = %v", readable)
	}

	// The binding list shows the space. The deletion of the space removes
	// its bindings.
	bindings, _ := st.Bindings(ctx, tn.ID)
	n := 0
	for _, b := range bindings {
		if b.SpaceSlug == "private0" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d bindings for the private space", n)
	}
	if err := st.DeleteSpace(ctx, tn.ID, "private0"); err != nil {
		t.Fatal(err)
	}
	bindings, _ = st.Bindings(ctx, tn.ID)
	for _, b := range bindings {
		if b.SpaceID != 0 {
			t.Fatalf("binding stays after space deletion: %+v", b)
		}
	}
}

func TestSpaceBindingIsolation(t *testing.T) {
	st := newStore(t)
	a := defaultTenant(t, st)
	b := newTenant(t, st, "bbbb")
	if err := st.CreateSpace(ctx, b.ID, "bspace00", "B", ""); err != nil {
		t.Fatal(err)
	}
	spB, _ := st.Space(ctx, b.ID, "bspace00")
	ua := login(t, st, a, "ann")
	// A binding in tenant a cannot use a space of tenant b.
	if err := st.BindRoleToUser(ctx, a.ID, "editor", ua.ID, store.Scope{SpaceID: spB.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("binding to space of other tenant: %v", err)
	}
	// Space permissions for a space of a different tenant do not exist.
	if _, err := st.Access(ctx, a.ID, spB.ID, ua.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("permissions in space of other tenant: %v", err)
	}
	if err := st.SetSpaceDefaultRole(ctx, a.ID, "bspace00", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("default role for space of other tenant: %v", err)
	}
}

func TestNodePermissions(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	carol := login(t, st, tn, "carol", "hr")
	dave := login(t, st, tn, "dave")

	mk := func(parent, kind, title string) string {
		t.Helper()
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: kind, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	node := func(id string) *store.Node {
		t.Helper()
		n, err := st.Node(ctx, sp.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// perms reads the node again, because the access path can change.
	perms := func(u *store.User, id string) rbac.Set {
		t.Helper()
		a, err := st.Access(ctx, tn.ID, sp.ID, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a.For(node(id).AccessPathID)
	}
	public := mk("", store.KindDocument, "Public")
	hr := mk("", store.KindFolder, "HR")
	salaries := mk(hr, store.KindDocument, "Salaries")
	sub := mk(salaries, store.KindDocument, "Details")
	inner := mk(hr, store.KindFolder, "Open inside")
	innerDoc := mk(inner, store.KindDocument, "Open doc")

	// Without settings, all nodes have the space permissions and one
	// access path.
	if p := perms(dave, salaries); !p.Has(rbac.DocRead) || p.Has(rbac.DocEdit) {
		t.Fatalf("default permissions: %v", p.List())
	}
	if node(public).AccessPathID != node(innerDoc).AccessPathID {
		t.Fatal("nodes without settings have different access paths")
	}

	// Make the folder HR private. Its subtree inherits the setting.
	none := ""
	if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, hr, &none); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hr, salaries, sub, inner, innerDoc} {
		if p := perms(dave, id); len(p) != 0 {
			t.Fatalf("%s in private folder gives %v", node(id).Title, p.List())
		}
	}
	if !perms(dave, public).Has(rbac.DocRead) {
		t.Fatal("the private folder changed the rest of the space")
	}

	// A folder inside the private folder can open again.
	viewer := "viewer"
	if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, inner, &viewer); err != nil {
		t.Fatal(err)
	}
	if !perms(dave, innerDoc).Has(rbac.DocRead) || perms(dave, salaries).Has(rbac.DocRead) {
		t.Fatal("nested default role does not apply")
	}

	// A document can open again inside the private folder. Its
	// sub-documents inherit the setting.
	if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, salaries, &viewer); err != nil {
		t.Fatal(err)
	}
	if !perms(dave, salaries).Has(rbac.DocRead) || !perms(dave, sub).Has(rbac.DocRead) || len(perms(dave, hr)) != 0 {
		t.Fatal("document default role does not apply")
	}
	// "Same as the parent" removes the setting.
	if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, salaries, nil); err != nil {
		t.Fatal(err)
	}
	if perms(dave, sub).Has(rbac.DocRead) {
		t.Fatal("removed document default role still applies")
	}

	// A binding for a group on the folder gives permissions in the
	// subtree. A binding on a document applies to the document and its
	// sub-documents.
	gid, _ := st.EnsureOIDCGroup(ctx, tn.ID, "hr")
	if err := st.BindRoleToGroup(ctx, tn.ID, "editor", gid, store.Scope{SpaceID: sp.ID, NodeID: node(hr).ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindRoleToGroup(ctx, tn.ID, "maintainer", gid, store.Scope{SpaceID: sp.ID, NodeID: node(salaries).ID}); err != nil {
		t.Fatal(err)
	}
	if p := perms(carol, sub); !p.Has(rbac.DocDelete) {
		t.Fatalf("document binding in sub-document: %v", p.List())
	}
	if p := perms(carol, innerDoc); !p.Has(rbac.DocEdit) || p.Has(rbac.DocDelete) {
		t.Fatalf("folder binding: %v", p.List())
	}
	if perms(carol, public).Has(rbac.DocEdit) {
		t.Fatal("folder binding applies outside the folder")
	}

	// The admin role for a node gives only document permissions.
	if err := st.BindRoleToUser(ctx, tn.ID, "admin", dave.ID, store.Scope{SpaceID: sp.ID, NodeID: node(inner).ID}); err != nil {
		t.Fatal(err)
	}
	if p := perms(dave, innerDoc); !p.Has(rbac.DocDelete) || p.Has(rbac.TenantManage) {
		t.Fatalf("node admin binding gives %v", p.List())
	}

	// The deletion of a binding refreshes the cache.
	bindings, _ := st.Bindings(ctx, tn.ID)
	for _, b := range bindings {
		if b.NodeID == node(salaries).ID {
			if err := st.DeleteBinding(ctx, tn.ID, b.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if perms(carol, sub).Has(rbac.DocDelete) {
		t.Fatal("deleted document binding still applies")
	}

	// A move takes the subtree out of the private folder.
	if err := st.MoveNode(ctx, sp.ID, salaries, ""); err != nil {
		t.Fatal(err)
	}
	if !perms(dave, sub).Has(rbac.DocRead) {
		t.Fatal("moved document keeps the permissions of the old folder")
	}

	// The two nodes below HR (the inner folder and its document) have the
	// same access path.
	paths, err := st.SubtreePaths(ctx, node(hr).ID)
	if err != nil || len(paths) != 1 {
		t.Fatalf("subtree paths of HR = %v, %v", paths, err)
	}

	// Readable spaces.
	none2 := ""
	if err := st.SetSpaceDefaultRole(ctx, tn.ID, "default0", &none2); err != nil {
		t.Fatal(err)
	}
	if ids, _ := st.ReadableSpaces(ctx, tn.ID, carol.ID); len(ids) != 1 {
		t.Fatalf("space with a node binding is not readable: %v", ids)
	}
	eve := login(t, st, tn, "eve")
	if ids, _ := st.ReadableSpaces(ctx, tn.ID, eve.ID); len(ids) != 1 {
		t.Fatalf("space with a readable node default role is not readable: %v", ids)
	}
	if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, inner, nil); err != nil {
		t.Fatal(err)
	}
	if ids, _ := st.ReadableSpaces(ctx, tn.ID, eve.ID); len(ids) != 0 {
		t.Fatalf("private space is readable: %v", ids)
	}

	// The binding list shows the node. The deletion removes the binding.
	bindings, _ = st.Bindings(ctx, tn.ID)
	found := false
	for _, b := range bindings {
		found = found || (b.NodePublicID == hr && b.NodeTitle == "HR" && b.SpaceSlug == "default0")
	}
	if !found {
		t.Fatalf("no node binding in the list: %+v", bindings)
	}
	purge(t, st, sp.ID, hr)
	bindings, _ = st.Bindings(ctx, tn.ID)
	for _, b := range bindings {
		if b.NodeID != 0 {
			t.Fatalf("node binding stays after deletion: %+v", b)
		}
	}
	checkAccessCache(t, st, sp.ID)
}

// checkAccessCache compares the cached access path of each node with a
// path that the test calculates from the tree.
func checkAccessCache(t *testing.T, st *store.Store, spaceID int64) {
	t.Helper()
	got, want, err := st.AccessCacheReport(ctx, spaceID)
	if err != nil {
		t.Fatal(err)
	}
	for id, w := range want {
		if !slices.Equal(got[id], w) {
			t.Fatalf("node %d: cached path %v, want %v", id, got[id], w)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d cached paths, %d nodes", len(got), len(want))
	}
}

func TestAccessCacheRandomOperations(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "ann")
	var ids []string
	rnd := uint64(42)
	next := func(n int) int {
		rnd = rnd*6364136223846793005 + 1442695040888963407
		return int((rnd >> 33) % uint64(n))
	}
	viewer, none := "viewer", ""
	for i := range 300 {
		switch op := next(6); {
		case op <= 1 || len(ids) < 5:
			parent := ""
			if len(ids) > 0 && next(4) > 0 {
				parent = ids[next(len(ids))]
			}
			id, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: store.KindDocument, Title: fmt.Sprint("N", i)})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		case op == 2:
			roles := []*string{nil, &viewer, &none}
			if err := st.SetNodeDefaultRole(ctx, tn.ID, sp.ID, ids[next(len(ids))], roles[next(3)]); err != nil {
				t.Fatal(err)
			}
		case op == 3:
			n, _ := st.Node(ctx, sp.ID, ids[next(len(ids))])
			if err := st.BindRoleToUser(ctx, tn.ID, "editor", u.ID, store.Scope{SpaceID: sp.ID, NodeID: n.ID}); err != nil {
				t.Fatal(err)
			}
		case op == 4:
			bindings, _ := st.Bindings(ctx, tn.ID)
			if len(bindings) > 0 {
				if err := st.DeleteBinding(ctx, tn.ID, bindings[next(len(bindings))].ID); err != nil {
					t.Fatal(err)
				}
			}
		case op == 5:
			parent := ""
			if next(3) > 0 {
				parent = ids[next(len(ids))]
			}
			err := st.MoveNode(ctx, sp.ID, ids[next(len(ids))], parent)
			if err != nil && !errors.Is(err, store.ErrInvalid) {
				t.Fatal(err)
			}
		}
	}
	checkAccessCache(t, st, sp.ID)
	// A full rebuild gives the same result.
	if err := st.RebuildAccessPaths(ctx, sp.ID); err != nil {
		t.Fatal(err)
	}
	checkAccessCache(t, st, sp.ID)
}

func TestFolderBindingIsolation(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	if err := st.CreateSpace(ctx, tn.ID, "other000", "Other", ""); err != nil {
		t.Fatal(err)
	}
	other, _ := st.Space(ctx, tn.ID, "other000")
	id, _ := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindFolder, Title: "F"})
	f, _ := st.Node(ctx, sp.ID, id)
	u := login(t, st, tn, "ann")
	// The node must be in the space of the binding.
	if err := st.BindRoleToUser(ctx, tn.ID, "editor", u.ID, store.Scope{SpaceID: other.ID, NodeID: f.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("folder of a different space: %v", err)
	}
	if err := st.SetNodeDefaultRole(ctx, tn.ID, other.ID, f.PublicID, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("default role through a different space: %v", err)
	}
	b := newTenant(t, st, "bbbb")
	if err := st.SetNodeDefaultRole(ctx, b.ID, sp.ID, f.PublicID, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("default role through a different tenant: %v", err)
	}
}

func TestFiles(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	u := login(t, st, tn, "erin")
	used, err := st.CreateFile(ctx, sp.ID, "image/png", []byte("png"), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	unused, _ := st.CreateFile(ctx, sp.ID, "image/png", []byte("png"), u.ID)
	if _, err := st.CreateFile(ctx, sp.ID, "text/html", []byte("<p>"), u.ID); err == nil {
		t.Fatal("the database accepts an HTML file")
	}

	f, err := st.File(ctx, sp.ID, used)
	if err != nil || f.CreatedBy != u.ID || string(f.Data) != "png" || len(f.AccessPaths) != 0 {
		t.Fatalf("new file = %+v, %v", f, err)
	}
	c := content("picture")
	c.Files = []string{used, "zzzzzzzzzzzzzz"}
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Pictures", Content: c, AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := st.Node(ctx, sp.ID, id)
	f, _ = st.File(ctx, sp.ID, used)
	if len(f.AccessPaths) != 1 || f.AccessPaths[0] != n.AccessPathID {
		t.Fatalf("access paths = %v, want [%d]", f.AccessPaths, n.AccessPathID)
	}
	// A new version without the image keeps the reference. Thus the old
	// version continues to show the image.
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 1, Title: "Pictures", Content: content("none"), AuthorID: u.ID}); err != nil {
		t.Fatal(err)
	}
	// Another space cannot use the file.
	if err := st.CreateSpace(ctx, tn.ID, "other000", "Other", ""); err != nil {
		t.Fatal(err)
	}
	other, _ := st.Space(ctx, tn.ID, "other000")
	if _, err := st.File(ctx, other.ID, used); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("file from other space: %v", err)
	}

	// After one day, DeleteExpired removes the unused file only.
	st.SetNow(func() time.Time { return time.Now().Add(25 * time.Hour) })
	if err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.File(ctx, sp.ID, unused); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unused file: %v", err)
	}
	if _, err := st.File(ctx, sp.ID, used); err != nil {
		t.Fatalf("used file: %v", err)
	}
	// A node in the trash does not give access to the file.
	if err := st.TrashNode(ctx, sp.ID, id, 0); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.File(ctx, sp.ID, used); len(f.AccessPaths) != 0 {
		t.Fatalf("access paths after delete = %v", f.AccessPaths)
	}
}

// purge moves a node into the trash and deletes it permanently.
func purge(t *testing.T, st *store.Store, spaceID int64, publicID string) {
	t.Helper()
	if err := st.TrashNode(ctx, spaceID, publicID, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeNode(ctx, spaceID, publicID); err != nil {
		t.Fatal(err)
	}
}

func TestTrash(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)
	mk := func(parent, kind, title string) string {
		t.Helper()
		id, err := st.CreateNode(ctx, sp.ID, store.NewNode{ParentPublicID: parent, Kind: kind, Title: title, Slug: title})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	folder := mk("", store.KindFolder, "guides")
	doc := mk(folder, store.KindDocument, "install")
	sub := mk(doc, store.KindDocument, "linux")
	other := mk("", store.KindDocument, "home")

	// The sub-document goes into the trash first. It stays a separate
	// item when its folder goes into the trash.
	if err := st.TrashNode(ctx, sp.ID, sub, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.TrashNode(ctx, sp.ID, folder, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.TrashNode(ctx, sp.ID, folder, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	items, err := st.Trash(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].PublicID != folder || items[0].Descendants != 1 ||
		items[1].PublicID != sub || !items[1].ParentInTrash || items[1].ParentTitle != "install" {
		t.Fatalf("trash = %+v", items)
	}
	// The live queries do not show items in the trash.
	if nodes, _ := st.SpaceNodes(ctx, sp.ID); len(nodes) != 1 || nodes[0].PublicID != other {
		t.Fatalf("space nodes = %+v", nodes)
	}
	if nodes, _ := st.NodesBySlug(ctx, sp.ID, "install"); len(nodes) != 0 {
		t.Fatalf("slug finds a node in the trash: %+v", nodes)
	}
	if err := st.UpdateNode(ctx, sp.ID, doc, store.NodeUpdate{BaseVersion: 1, Title: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update in the trash: %v", err)
	}
	if err := st.MoveNode(ctx, sp.ID, other, folder); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("move into the trash: %v", err)
	}

	// The sub-document cannot go back before its parent.
	if err := st.RestoreNode(ctx, sp.ID, sub); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("restore with parent in the trash: %v", err)
	}
	if err := st.RestoreNode(ctx, sp.ID, doc); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("restore of an item in a deleted folder: %v", err)
	}
	if err := st.RestoreNode(ctx, sp.ID, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Node(ctx, sp.ID, doc); err != nil {
		t.Fatalf("document after restore: %v", err)
	}
	if _, err := st.Node(ctx, sp.ID, sub); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("separate item came back: %v", err)
	}
	if err := st.RestoreNode(ctx, sp.ID, sub); err != nil {
		t.Fatal(err)
	}

	// A permanent deletion removes the subtree, also the separate items
	// in the trash.
	if err := st.TrashNode(ctx, sp.ID, sub, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeNode(ctx, sp.ID, doc); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("purge of a live node: %v", err)
	}
	purge(t, st, sp.ID, folder)
	if items, _ := st.Trash(ctx, sp.ID); len(items) != 0 {
		t.Fatalf("trash after purge = %+v", items)
	}

	// DeleteExpired removes old items only.
	if err := st.TrashNode(ctx, sp.ID, other, 0); err != nil {
		t.Fatal(err)
	}
	st.SetNow(func() time.Time { return time.Now().AddDate(0, 0, store.TrashDays-1) })
	if err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if items, _ := st.Trash(ctx, sp.ID); len(items) != 1 {
		t.Fatalf("trash after %d days = %+v", store.TrashDays-1, items)
	}
	st.SetNow(func() time.Time { return time.Now().AddDate(0, 0, store.TrashDays+1) })
	if err := st.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if items, _ := st.Trash(ctx, sp.ID); len(items) != 0 {
		t.Fatalf("trash after %d days = %+v", store.TrashDays+1, items)
	}
	// The cleanup task writes the permanent deletion into the audit log.
	events, err := st.AuditEvents(ctx, &tn.ID, 0, 1)
	if err != nil || len(events) != 1 || events[0].Action != store.AuditNodePurge ||
		events[0].Actor != "System" || events[0].Target != "home" || events[0].Space != "default0" {
		t.Fatalf("audit events = %+v, %v", events, err)
	}
	checkAccessCache(t, st, sp.ID)
}

func TestClassifications(t *testing.T) {
	st := newStore(t)
	tn := defaultTenant(t, st)
	sp := defaultSpace(t, st, tn)

	levels, err := st.Classifications(ctx, tn.ID)
	if err != nil || len(levels) != 5 || levels[0].Name != "Unclassified" || !levels[0].IsDefault ||
		levels[4].Name != "Top Secret" {
		t.Fatalf("default levels = %+v, %v", levels, err)
	}
	byName := map[string]int64{}
	for _, c := range levels {
		byName[c.Name] = c.ID
	}

	// A new document gets the default level. A folder has none.
	id, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := st.Node(ctx, sp.ID, id)
	if n.Classification != "Unclassified" {
		t.Fatalf("classification = %q", n.Classification)
	}
	folder, _ := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindFolder, Title: "Box"})
	if f, _ := st.Node(ctx, sp.ID, folder); f.ClassificationID != 0 {
		t.Fatalf("folder classification = %+v", f)
	}

	// A change of the level is in the revision and in the audit log.
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 1, Title: "Plan", ClassificationID: byName["Secret"]}); err != nil {
		t.Fatal(err)
	}
	if rev, _ := st.Revision(ctx, n.ID, 2); rev.Classification != "Secret" {
		t.Fatalf("revision classification = %q", rev.Classification)
	}
	if rev, _ := st.Revision(ctx, n.ID, 1); rev.Classification != "Unclassified" {
		t.Fatalf("old revision classification = %q", rev.Classification)
	}
	events, _ := st.AuditEvents(ctx, &tn.ID, 0, 1)
	if len(events) != 1 || events[0].Action != store.AuditNodeClassify || events[0].Details != "From Unclassified to Secret." {
		t.Fatalf("audit events = %+v", events)
	}
	// 0 keeps the level.
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 2, Title: "Plan"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Node(ctx, sp.ID, id); n.Classification != "Secret" {
		t.Fatalf("classification after a save = %q", n.Classification)
	}

	// A level of a different tenant is not permitted.
	other := newTenant(t, st, "othr")
	otherLevels, _ := st.Classifications(ctx, other.ID)
	if len(otherLevels) != 5 {
		t.Fatalf("levels of a new tenant = %+v", otherLevels)
	}
	if err := st.UpdateNode(ctx, sp.ID, id, store.NodeUpdate{BaseVersion: 3, Title: "Plan", ClassificationID: otherLevels[3].ID}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("level of a different tenant: %v", err)
	}
	if _, err := st.CreateNode(ctx, sp.ID, store.NewNode{Kind: store.KindDocument, Title: "X", ClassificationID: otherLevels[0].ID}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("new document with a level of a different tenant: %v", err)
	}

	// A new default replaces the old default.
	newID, err := st.SaveClassification(ctx, tn.ID, store.Classification{Name: "Internal", Position: 2, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveClassification(ctx, tn.ID, store.Classification{Name: "internal"}); err != nil {
		t.Fatalf("names are case sensitive: %v", err)
	}
	if _, err := st.SaveClassification(ctx, tn.ID, store.Classification{Name: "Internal"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	levels, _ = st.Classifications(ctx, tn.ID)
	defaults := 0
	for _, c := range levels {
		if c.IsDefault {
			defaults++
			if c.ID != newID {
				t.Fatalf("default = %+v", c)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("%d defaults", defaults)
	}
	// The default and a level in use cannot be deleted.
	if err := st.DeleteClassification(ctx, tn.ID, newID); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("delete the default: %v", err)
	}
	if err := st.DeleteClassification(ctx, tn.ID, byName["Secret"]); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete a level in use: %v", err)
	}
	if err := st.DeleteClassification(ctx, tn.ID, byName["Public"]); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteClassification(ctx, other.ID, byName["Restricted"]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete a level of a different tenant: %v", err)
	}
	// A tenant with documents can be deleted with its levels.
	if err := st.CreateSpace(ctx, other.ID, "othsp000", "Other", ""); err != nil {
		t.Fatal(err)
	}
	osp, _ := st.Space(ctx, other.ID, "othsp000")
	if _, err := st.CreateNode(ctx, osp.ID, store.NewNode{Kind: store.KindDocument, Title: "Doc"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTenant(ctx, other.ID); err != nil {
		t.Fatalf("delete a tenant with documents: %v", err)
	}
}
