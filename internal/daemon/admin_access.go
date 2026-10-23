package daemon

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yumekaz/cairn/internal/config"
)

func privateRegularFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("credential/key must be a private regular file (0400 or 0600)")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, fmt.Errorf("credential/key changed while opening")
	}
	return f, nil
}

func adminTokenHash(path string) ([32]byte, error) {
	var zero [32]byte
	f, err := privateRegularFile(path)
	if err != nil {
		return zero, fmt.Errorf("read admin credential: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return zero, err
	}
	value := strings.TrimSpace(string(data))
	if len(value) < 32 || len(data) > 4096 || strings.ContainsAny(value, " \t\r\n:") {
		return zero, fmt.Errorf("admin credential must be a single token of at least 32 characters")
	}
	return sha256.Sum256([]byte(value)), nil
}

func validateTCPAccess(cfg *config.DaemonConfig) error {
	if cfg.DashboardAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.DashboardAddr)
	if err != nil {
		return fmt.Errorf("invalid dashboard address: %w", err)
	}
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	hasTLS := cfg.DashboardTLSCert != "" && cfg.DashboardTLSKey != ""
	if (cfg.DashboardTLSCert == "") != (cfg.DashboardTLSKey == "") {
		return fmt.Errorf("dashboard TLS requires both certificate and private key")
	}
	if !loopback && (!hasTLS || cfg.AdminTokenFile == "") {
		return fmt.Errorf("non-loopback dashboard requires TLS and admin_token_file; refusing unsecured listener")
	}
	for _, alias := range cfg.DashboardHosts {
		if alias == "" || strings.ContainsAny(alias, "/\\@* \t\r\n") || strings.HasSuffix(strings.ToLower(alias), ".localhost") {
			return fmt.Errorf("invalid dashboard host allowlist entry")
		}
		if net.ParseIP(alias) == nil && strings.Contains(alias, ":") {
			return fmt.Errorf("dashboard_hosts entries must not contain ports")
		}
	}
	return nil
}

func adminHTTPHandler(next http.Handler, cfg *config.DaemonConfig) (http.Handler, error) {
	if err := validateTCPAccess(cfg); err != nil {
		return nil, err
	}
	if cfg.AdminTokenFile == "" {
		return dashboardHTTPGuardHosts(next, cfg.DashboardAddr, cfg.DashboardHosts), nil
	}
	expected, err := adminTokenHash(cfg.AdminTokenFile)
	if err != nil {
		return nil, err
	}
	authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user, password, ok := r.BasicAuth()
		actual := sha256.Sum256([]byte(password))
		valid := subtle.ConstantTimeCompare(expected[:], actual[:])
		if !ok || user != "cairn" || valid != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Cairn admin", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
	// Host/origin rejection happens before authentication and mutation dispatch.
	return dashboardHTTPGuardHosts(authenticated, cfg.DashboardAddr, cfg.DashboardHosts), nil
}

// Prepare before starting any workers or the Unix listener. Misconfiguration
// and bind failures must not leave a half-started control plane behind.
func (s *Server) prepareTCPServer() (net.Listener, *http.Server, error) {
	cfg := *s.config
	if cfg.DashboardAddr == "" {
		return nil, nil, nil
	}
	if err := validateTCPAccess(&cfg); err != nil {
		return nil, nil, err
	}
	if cfg.AdminTokenFile != "" {
		if _, err := adminTokenHash(cfg.AdminTokenFile); err != nil {
			return nil, nil, err
		}
	}
	var transport *tls.Config
	if cfg.DashboardTLSCert != "" {
		key, err := privateRegularFile(cfg.DashboardTLSKey)
		if err != nil {
			return nil, nil, fmt.Errorf("dashboard TLS key: %w", err)
		}
		keyData, err := io.ReadAll(io.LimitReader(key, 1<<20))
		key.Close()
		if err != nil {
			return nil, nil, err
		}
		certData, err := os.ReadFile(cfg.DashboardTLSCert)
		if err != nil {
			return nil, nil, err
		}
		certificate, err := tls.X509KeyPair(certData, keyData)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid dashboard TLS certificate/key pair: %w", err)
		}
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			return nil, nil, fmt.Errorf("invalid dashboard certificate: %w", err)
		}
		if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
			return nil, nil, fmt.Errorf("dashboard certificate is not currently valid")
		}
		transport = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	}
	listener, err := net.Listen("tcp", cfg.DashboardAddr)
	if err != nil {
		return nil, nil, err
	}
	host, port, _ := net.SplitHostPort(cfg.DashboardAddr)
	if port == "0" {
		_, port, _ = net.SplitHostPort(listener.Addr().String())
		cfg.DashboardAddr = net.JoinHostPort(host, port)
	}
	handler, err := s.tcpHTTPHandlerWithConfig(&cfg)
	if err != nil {
		listener.Close()
		return nil, nil, err
	}
	if transport != nil {
		listener = tls.NewListener(listener, transport)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10, TLSConfig: transport}
	return listener, server, nil
}
