package auth

import (
	"encoding/json"
	"reflect"
	"testing"
)

func claims(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestIdentityFromClaims(t *testing.T) {
	c := claims(t, `{"preferred_username":"alice","email":"a@example.com","name":"Alice A","groups":["admins","","writers"]}`)
	id, err := identityFromClaims("iss", "sub-1", c, "groups")
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "alice" || id.Email != "a@example.com" || id.DisplayName != "Alice A" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if !reflect.DeepEqual(id.Groups, []string{"admins", "writers"}) {
		t.Fatalf("groups = %v", id.Groups)
	}
}

func TestIdentityUsernameFallback(t *testing.T) {
	id, err := identityFromClaims("iss", "sub-1", claims(t, `{"email":"a@example.com"}`), "groups")
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "a@example.com" {
		t.Fatalf("username = %q", id.Username)
	}
	id, err = identityFromClaims("iss", "sub-1", claims(t, `{}`), "groups")
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "sub-1" {
		t.Fatalf("username = %q", id.Username)
	}
}

func TestIdentityBadGroups(t *testing.T) {
	if _, err := identityFromClaims("iss", "sub", claims(t, `{"groups":"admins"}`), "groups"); err == nil {
		t.Fatal("expected an error for a groups claim that is not a list")
	}
}

func TestIdentityWrongTypeIgnored(t *testing.T) {
	id, err := identityFromClaims("iss", "sub", claims(t, `{"preferred_username":42}`), "groups")
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "sub" {
		t.Fatalf("username = %q", id.Username)
	}
}

func TestIdentityNoSubject(t *testing.T) {
	if _, err := identityFromClaims("iss", "", claims(t, `{}`), "groups"); err == nil {
		t.Fatal("expected an error")
	}
}
