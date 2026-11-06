// Package certs makes a private certificate authority (CA) and TLS
// certificates that it signs, and loads the certificate of the web
// server.
//
// The generated certificates are for installations without
// certificates of their own. They are valid for 10 years. An
// installation must give its own certificates and chains if the clients
// must validate them without the generated CA.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Validity is the life of the generated CA and certificates.
const Validity = 10 * 365 * 24 * time.Hour

// File names in the folders.
const (
	CACert = "ca.crt"
	CAKey  = "ca.key"
)

// Leaf is a server certificate. The files are <Name>.crt and <Name>.key.
// Hosts are the DNS names and IP addresses of the server.
type Leaf struct {
	Name  string
	Hosts []string
}

// Ensure makes the files that do not exist. caDir gets ca.crt and
// ca.key. dir gets the certificate and the key of each leaf, and a copy
// of ca.crt for the clients. If the CA exists, Ensure uses it. Ensure
// never replaces a file. It returns the names of the files that it made.
func Ensure(caDir, dir string, leaves []Leaf) ([]string, error) {
	return ensure(caDir, dir, leaves, time.Now())
}

func ensure(caDir, dir string, leaves []Leaf, now time.Time) ([]string, error) {
	var made []string
	for _, d := range []string{caDir, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	caCert, caKey, err := loadPair(filepath.Join(caDir, CACert), filepath.Join(caDir, CAKey))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		caCert, caKey, err = newCA(now)
		if err != nil {
			return nil, err
		}
		if err := writePair(caDir, "ca", caCert, caKey); err != nil {
			return nil, err
		}
		made = append(made, filepath.Join(caDir, CACert), filepath.Join(caDir, CAKey))
	case err != nil:
		return nil, fmt.Errorf("CA: %w", err)
	}

	// The clients need the CA certificate, also if the CA key is in a
	// different folder.
	if dir != caDir {
		path := filepath.Join(dir, CACert)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if err := writeFile(path, pemBlock("CERTIFICATE", caCert.Raw), 0o644); err != nil {
				return nil, err
			}
			made = append(made, path)
		}
	}

	for _, l := range leaves {
		crtPath, keyPath := filepath.Join(dir, l.Name+".crt"), filepath.Join(dir, l.Name+".key")
		_, crtErr := os.Stat(crtPath)
		_, keyErr := os.Stat(keyPath)
		switch {
		case crtErr == nil && keyErr == nil:
			continue
		case crtErr == nil || keyErr == nil:
			return nil, fmt.Errorf("%s: the certificate or the key is missing", l.Name)
		}
		cert, key, err := newLeaf(l, caCert, caKey, now)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.Name, err)
		}
		if err := writePair(dir, l.Name, cert, key); err != nil {
			return nil, err
		}
		made = append(made, crtPath, keyPath)
	}
	return made, nil
}

func newKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func newCA(now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		// A part of the serial number makes the name of each CA different.
		Subject:               pkix.Name{Organization: []string{"Skriva"}, CommonName: "Skriva generated CA " + shortID(sn)},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(Validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	return sign(tmpl, tmpl, key, key)
}

// shortID returns 8 hexadecimal digits of n.
func shortID(n *big.Int) string {
	return fmt.Sprintf("%08x", new(big.Int).Rsh(n, 95).Uint64()&0xffffffff)
}

func newLeaf(l Leaf, ca *x509.Certificate, caKey *ecdsa.PrivateKey, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if len(l.Hosts) == 0 {
		return nil, nil, errors.New("no host names")
	}
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{Organization: []string{"Skriva"}, CommonName: l.Hosts[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(Validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range l.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	return sign(tmpl, ca, key, caKey)
}

func sign(tmpl, parent *x509.Certificate, key, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// writePair writes <name>.crt (0644) and <name>.key (0600).
func writePair(dir, name string, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, name+".key"), pemBlock("PRIVATE KEY", der), 0o600); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, name+".crt"), pemBlock("CERTIFICATE", cert.Raw), 0o644)
}

// writeFile writes a new file. It does not replace a file that exists.
func writeFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G304 -- the operator sets the folder.
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// loadPair reads the CA certificate and its key.
func loadPair(crtPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	crtPEM, crtErr := os.ReadFile(crtPath) // #nosec G304 -- the operator sets the folder.
	keyPEM, keyErr := os.ReadFile(keyPath) // #nosec G304 -- the operator sets the folder.
	switch {
	case errors.Is(crtErr, fs.ErrNotExist) && errors.Is(keyErr, fs.ErrNotExist):
		return nil, nil, fs.ErrNotExist
	case crtErr != nil:
		return nil, nil, crtErr
	case keyErr != nil:
		return nil, nil, keyErr
	}
	cb, _ := pem.Decode(crtPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, errors.New("the files are not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || !cert.IsCA {
		return nil, nil, errors.New("the files are not a CA of Skriva")
	}
	return cert, key, nil
}

// Pool returns the system certificate pool with the certificates in the
// PEM file path. An empty path gives the system pool.
func Pool(path string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if path == "" {
		return pool, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the operator sets the path.
	if err != nil {
		return nil, err
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%s: no PEM certificates", path)
	}
	return pool, nil
}
