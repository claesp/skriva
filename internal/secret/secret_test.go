package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{7}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := newBox(t)
	ct := b.Seal([]byte("secret"), "tenant/acme")
	if bytes.Contains(ct, []byte("secret")) {
		t.Fatal("ciphertext contains the plain text")
	}
	got, err := b.Open(ct, "tenant/acme")
	if err != nil || string(got) != "secret" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if bytes.Equal(ct, b.Seal([]byte("secret"), "tenant/acme")) {
		t.Fatal("two Seal calls gave the same result")
	}
}

func TestOpenFails(t *testing.T) {
	b := newBox(t)
	ct := b.Seal([]byte("secret"), "tenant/acme")
	if _, err := b.Open(ct, "tenant/evil"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong context: %v", err)
	}
	ct[len(ct)-1] ^= 1
	if _, err := b.Open(ct, "tenant/acme"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("changed data: %v", err)
	}
	if _, err := b.Open([]byte("short"), "tenant/acme"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("short data: %v", err)
	}
	other, _ := New(bytes.Repeat([]byte{8}, KeySize))
	if _, err := other.Open(b.Seal([]byte("x"), "c"), "c"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestParseKey(t *testing.T) {
	key := make([]byte, KeySize)
	_, _ = rand.Read(key)
	got, err := ParseKey(base64.StdEncoding.EncodeToString(key) + "\n")
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("ParseKey = %x, %v", got, err)
	}
	if _, err := ParseKey(base64.StdEncoding.EncodeToString(key[:16])); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := ParseKey("not base64!"); err == nil {
		t.Fatal("bad base64 accepted")
	}
}
