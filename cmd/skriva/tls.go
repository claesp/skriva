package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/claesp/skriva/internal/certs"
	"github.com/claesp/skriva/internal/config"
)

// loadServerCert loads the certificate of the web server. Without
// SKRIVA_TLS_CERT_FILE, the server uses the generated certificate in
// SKRIVA_TLS_DIR, and makes the CA and the certificate first if they do
// not exist.
func loadServerCert(cfg *config.Config, log *slog.Logger) (*certs.Server, error) {
	certFile, keyFile := cfg.TLSCertFile, cfg.TLSKeyFile
	if certFile == "" {
		dir := cfg.TLSDir
		made, err := certs.Ensure(dir, dir, []certs.Leaf{{Name: "skriva", Hosts: serverHosts(cfg.BaseURL.Hostname())}})
		if err != nil {
			return nil, fmt.Errorf("make a TLS certificate in SKRIVA_TLS_DIR (%s): %w; "+
				"set SKRIVA_TLS_CERT_FILE and SKRIVA_TLS_KEY_FILE, or make the folder writable", dir, err)
		}
		if len(made) > 0 {
			log.Warn("the server made a self-signed CA and a TLS certificate; give your own certificate in "+
				"SKRIVA_TLS_CERT_FILE and SKRIVA_TLS_KEY_FILE, or make the clients trust the CA", "ca", filepath.Join(dir, certs.CACert))
		}
		certFile, keyFile = filepath.Join(dir, "skriva.crt"), filepath.Join(dir, "skriva.key")
	}
	return certs.NewServer(certFile, keyFile)
}

// serverHosts returns the host names of a generated server certificate:
// the host of the base URL and the local addresses.
func serverHosts(extra ...string) []string {
	return hostList(append(extra, "localhost", "127.0.0.1", "::1"))
}

// hostList removes empty and repeated names.
func hostList(hosts []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// makeCerts is the command "skriva certs".
func makeCerts(args []string) error {
	fs := flag.NewFlagSet("certs", flag.ContinueOnError)
	dir := fs.String("dir", "tls", "the folder for the certificates and keys of the servers, and a copy of ca.crt")
	caDir := fs.String("ca-dir", "", "the folder for ca.crt and ca.key (default: the same as -dir)")
	skriva := fs.String("skriva-hosts", "", "more host names of Skriva, separated by commas")
	keycloak := fs.String("keycloak-hosts", "", "more host names of Keycloak, separated by commas")
	postgres := fs.String("postgres-hosts", "", "more host names of PostgreSQL, separated by commas")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `usage: skriva certs [flags]

Make a private CA and the TLS certificates of Skriva, Keycloak and
PostgreSQL, valid for 10 years. The command uses a CA that exists, and it
never replaces a file. Give your own certificates in production if the
clients must validate them without this CA.

Files: <ca-dir>/ca.crt, <ca-dir>/ca.key, and in <dir>: ca.crt,
skriva.crt, skriva.key, keycloak.crt, keycloak.key, postgres.crt,
postgres.key.

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unknown argument %q", fs.Arg(0))
	}
	if *caDir == "" {
		*caDir = *dir
	}
	split := func(s string) []string { return strings.Split(s, ",") }
	leaves := []certs.Leaf{
		{Name: "skriva", Hosts: serverHosts(append(split(*skriva), "skriva")...)},
		{Name: "keycloak", Hosts: serverHosts(append(split(*keycloak), "keycloak")...)},
		{Name: "postgres", Hosts: serverHosts(append(split(*postgres), "db", "postgres")...)},
	}
	for _, l := range leaves {
		for _, h := range l.Hosts {
			if strings.ContainsAny(h, " /:") && net.ParseIP(h) == nil {
				return fmt.Errorf("%q is not a host name", h)
			}
		}
	}
	made, err := certs.Ensure(*caDir, *dir, leaves)
	if err != nil {
		return err
	}
	for _, f := range made {
		fmt.Fprintln(os.Stdout, "made", f)
	}
	if len(made) == 0 {
		fmt.Fprintln(os.Stdout, "all files exist")
	}
	return nil
}
