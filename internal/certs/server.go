package certs

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// checkInterval is the time between two checks of the files.
const checkInterval = time.Minute

// Server gives the certificate of the web server. It reads the files
// again when they change. Thus a new certificate (for example from an
// ACME client) needs no restart. It is safe for concurrent use.
type Server struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	modTime time.Time
	checked time.Time
}

// NewServer reads the certificate (with its chain) and the key. The
// files are PEM.
func NewServer(certFile, keyFile string) (*Server, error) {
	s := &Server{certFile: certFile, keyFile: keyFile}
	if err := s.load(time.Now()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) newest() (time.Time, error) {
	var t time.Time
	for _, f := range []string{s.certFile, s.keyFile} {
		info, err := os.Stat(f)
		if err != nil {
			return t, err
		}
		if info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	return t, nil
}

func (s *Server) load(now time.Time) error {
	mod, err := s.newest()
	if err != nil {
		return fmt.Errorf("TLS certificate: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return fmt.Errorf("TLS certificate: %w", err)
	}
	s.cert, s.modTime, s.checked = &cert, mod, now
	return nil
}

// Certificate is the GetCertificate function of tls.Config. If the new
// files are not valid, it keeps the old certificate.
func (s *Server) Certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.checked) >= checkInterval {
		s.checked = now
		if mod, err := s.newest(); err == nil && mod.After(s.modTime) {
			if err := s.load(now); err != nil {
				slog.Error("the new TLS certificate is not valid, the server keeps the old certificate", "error", err)
			} else {
				slog.Info("TLS certificate loaded again", "file", s.certFile)
			}
		}
	}
	return s.cert, nil
}

// Config returns the TLS configuration of the web server.
func (s *Server) Config() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: s.Certificate}
}
