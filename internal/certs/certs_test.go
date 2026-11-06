package certs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(data)
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEnsure(t *testing.T) {
	root := t.TempDir()
	caDir, dir := filepath.Join(root, "ca"), filepath.Join(root, "tls")
	leaves := []Leaf{
		{Name: "skriva", Hosts: []string{"wiki.example.com", "localhost", "127.0.0.1"}},
		{Name: "postgres", Hosts: []string{"db"}},
	}
	made, err := Ensure(caDir, dir, leaves)
	if err != nil || len(made) != 7 {
		t.Fatalf("Ensure = %v, %v", made, err)
	}

	ca := readCert(t, filepath.Join(dir, CACert))
	if !ca.IsCA || ca.NotAfter.Sub(ca.NotBefore) < Validity {
		t.Fatalf("CA: IsCA %v, validity %v", ca.IsCA, ca.NotAfter.Sub(ca.NotBefore))
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for name, host := range map[string]string{"skriva": "wiki.example.com", "postgres": "db"} {
		c := readCert(t, filepath.Join(dir, name+".crt"))
		if _, err := c.Verify(x509.VerifyOptions{DNSName: host, Roots: roots}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := tls.LoadX509KeyPair(filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := readCert(t, filepath.Join(dir, "skriva.crt")).VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("IP address: %v", err)
	}
	for _, f := range []string{filepath.Join(caDir, CAKey), filepath.Join(dir, "skriva.key")} {
		if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, %v", f, info.Mode(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, CAKey)); err == nil {
		t.Error("the CA key is in the folder of the server certificates")
	}

	// A second call makes only the missing files, with the same CA.
	if err := os.Remove(filepath.Join(dir, "postgres.crt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "postgres.key")); err != nil {
		t.Fatal(err)
	}
	made, err = Ensure(caDir, dir, append(leaves, Leaf{Name: "keycloak", Hosts: []string{"keycloak"}}))
	if err != nil || len(made) != 4 {
		t.Fatalf("second Ensure = %v, %v", made, err)
	}
	if _, err := readCert(t, filepath.Join(dir, "keycloak.crt")).Verify(x509.VerifyOptions{DNSName: "keycloak", Roots: roots}); err != nil {
		t.Errorf("keycloak: %v", err)
	}

	// A certificate without its key is an error.
	if err := os.Remove(filepath.Join(dir, "keycloak.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(caDir, dir, []Leaf{{Name: "keycloak", Hosts: []string{"keycloak"}}}); err == nil {
		t.Error("no error for a certificate without a key")
	}
}

func TestEnsureSameFolder(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, dir, []Leaf{{Name: "skriva", Hosts: []string{"localhost"}}}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{CACert, CAKey, "skriva.crt", "skriva.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
}

func TestServerReload(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, dir, []Leaf{{Name: "one", Hosts: []string{"one.example"}}, {Name: "two", Hosts: []string{"two.example"}}}); err != nil {
		t.Fatal(err)
	}
	crt, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	cp := func(from, to string) {
		data, err := os.ReadFile(filepath.Join(dir, from))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cp("one.crt", crt)
	cp("one.key", key)
	s, err := NewServer(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	name := func() string {
		c, _ := s.Certificate(nil)
		leaf, _ := x509.ParseCertificate(c.Certificate[0])
		return leaf.Subject.CommonName
	}
	if name() != "one.example" {
		t.Fatal("first certificate")
	}
	cp("two.crt", crt)
	cp("two.key", key)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(crt, future, future)
	s.checked = time.Time{}
	if got := name(); got != "two.example" {
		t.Fatalf("after the change: %s", got)
	}
	// A bad new file keeps the old certificate.
	if err := os.WriteFile(key, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := future.Add(time.Hour)
	_ = os.Chtimes(key, later, later)
	s.checked = time.Time{}
	if got := name(); got != "two.example" {
		t.Fatalf("after a bad change: %s", got)
	}
	if _, err := NewServer(crt, key); err == nil {
		t.Fatal("NewServer accepted a bad key")
	}
}

func TestPool(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Pool(filepath.Join(dir, CACert)); err != nil {
		t.Fatal(err)
	}
	if _, err := Pool(filepath.Join(dir, CAKey)); err == nil {
		t.Fatal("Pool accepted a file without certificates")
	}
}
