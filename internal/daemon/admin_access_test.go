package daemon

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yumekaz/cairn/internal/config"
)

const testAdminCredential = "regression-only-not-a-real-credential-123456789"

func adminFixture(t *testing.T) *config.DaemonConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admin.token")
	if err := os.WriteFile(path, []byte(testAdminCredential+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return &config.DaemonConfig{DashboardAddr: "127.0.0.1:2476", AdminTokenFile: path}
}

func TestAdminAccessRequiresCredentialsBeforeMutation(t *testing.T) {
	cfg := adminFixture(t)
	mutations := 0
	h, err := adminHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mutations++; w.WriteHeader(204) }), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, user, password, origin string
		want                         int
	}{
		{"missing", "", "", "", 401},
		{"wrong user", "other", testAdminCredential, "", 401},
		{"wrong credential", "cairn", "incorrect", "", 401},
		{"valid", "cairn", testAdminCredential, "http://127.0.0.1:2476", 204},
		{"authenticated cross origin", "cairn", testAdminCredential, "https://hostile.invalid", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://127.0.0.1:2476/services/test/stop", nil)
			if test.user != "" {
				req.SetBasicAuth(test.user, test.password)
			}
			req.Header.Set("Origin", test.origin)
			before := mutations
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != test.want || (mutations > before) != (test.want == 204) {
				t.Fatalf("status=%d mutations=%d", res.Code, mutations-before)
			}
			if test.want == 401 && res.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing authentication challenge")
			}
			if strings.Contains(res.Body.String(), testAdminCredential) {
				t.Fatal("credential disclosed in response")
			}
		})
	}
}

func TestAdminPrivateFileValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    os.FileMode
		value   string
		symlink bool
	}{
		{"world readable", 0644, testAdminCredential, false},
		{"group readable", 0640, testAdminCredential, false},
		{"weak token", 0600, "short", false},
		{"multiline token", 0600, testAdminCredential + "\nextra", false},
		{"symlink", 0600, testAdminCredential, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := adminFixture(t)
			if err := os.WriteFile(cfg.AdminTokenFile, []byte(test.value), test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(cfg.AdminTokenFile, test.mode); err != nil {
				t.Fatal(err)
			}
			if test.symlink {
				link := cfg.AdminTokenFile + "-link"
				if err := os.Symlink(cfg.AdminTokenFile, link); err != nil {
					t.Fatal(err)
				}
				cfg.AdminTokenFile = link
			}
			if _, err := adminHTTPHandler(http.NotFoundHandler(), cfg); err == nil {
				t.Fatal("insecure credential accepted")
			}
		})
	}
}

func TestUnsecuredNonLoopbackListenerFailsBeforeOpeningUnixSocket(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", "192.0.2.1:2476", "admin.example.invalid:2476"} {
		t.Run(address, func(t *testing.T) {
			cfg := &config.DaemonConfig{DashboardAddr: address, SocketPath: filepath.Join(t.TempDir(), "never.sock")}
			s := &Server{config: cfg}
			if err := s.Start(context.Background()); err == nil {
				t.Fatal("unsecured non-loopback startup accepted")
			}
			if _, err := os.Stat(cfg.SocketPath); !os.IsNotExist(err) {
				t.Fatal("private socket created before failed validation")
			}
		})
	}
}

func testTLSFiles(t *testing.T, cfg *config.DaemonConfig) *x509.Certificate {
	t.Helper()
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	defer seed.Close()
	certificate := seed.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DashboardTLSCert = filepath.Join(t.TempDir(), "certificate.pem")
	cfg.DashboardTLSKey = filepath.Join(t.TempDir(), "private-key.pem")
	if err := os.WriteFile(cfg.DashboardTLSCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.DashboardTLSKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	return seed.Certificate()
}

func TestNativeTLSAuthenticatedAdminAndCertificateVerification(t *testing.T) {
	s, _, cleanup := setupDaemonStore(t)
	defer cleanup()
	s.router = chi.NewRouter()
	s.setupMiddleware()
	s.setupRoutes()
	cfg := adminFixture(t)
	cfg.DashboardAddr = "127.0.0.1:0"
	certificate := testTLSFiles(t, cfg)
	s.config = cfg
	listener, server, err := s.prepareTCPServer()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	address := "https://" + listener.Addr().String()
	for _, test := range []struct {
		authenticated bool
		want          int
	}{{false, 401}, {true, 200}} {
		req, _ := http.NewRequest("GET", address+"/status", nil)
		if test.authenticated {
			req.SetBasicAuth("cairn", testAdminCredential)
		}
		req.Header.Set("Origin", address)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != test.want {
			t.Fatalf("TLS response: %d", res.StatusCode)
		}
	}
	untrusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}, Timeout: 3 * time.Second}
	if res, err := untrusted.Get(address + "/status"); err == nil {
		res.Body.Close()
		t.Fatal("untrusted TLS certificate accepted")
	}
	server.Close()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("TLS server failed to close")
	}
}

func TestTLSKeyAndPairFailClosed(t *testing.T) {
	cfg := adminFixture(t)
	cfg.DashboardAddr = "127.0.0.1:0"
	testTLSFiles(t, cfg)
	if err := os.Chmod(cfg.DashboardTLSKey, 0644); err != nil {
		t.Fatal(err)
	}
	s := &Server{config: cfg}
	if _, _, err := s.prepareTCPServer(); err == nil {
		t.Fatal("world-readable TLS key accepted")
	}
	if err := os.Chmod(cfg.DashboardTLSKey, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.DashboardTLSCert, []byte("invalid certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.prepareTCPServer(); err == nil {
		t.Fatal("invalid TLS certificate accepted")
	}
}

func TestExpiredAndFutureTLSCertificatesFailBeforeBinding(t *testing.T) {
	for _, offset := range []time.Duration{-48 * time.Hour, 48 * time.Hour} {
		cfg := adminFixture(t)
		cfg.DashboardAddr = "127.0.0.1:0"
		certificate := testTLSFiles(t, cfg)
		keyData, err := os.ReadFile(cfg.DashboardTLSKey)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(keyData)
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		template := *certificate
		template.NotBefore = time.Now().Add(offset)
		template.NotAfter = template.NotBefore.Add(time.Hour)
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, certificate.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg.DashboardTLSCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
			t.Fatal(err)
		}
		s := &Server{config: cfg}
		if listener, _, err := s.prepareTCPServer(); err == nil {
			listener.Close()
			t.Fatal("out-of-validity certificate accepted")
		}
	}
}

func TestDashboardBindCollisionLeavesUnixAPIUnopened(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg := adminFixture(t)
	cfg.DashboardAddr = listener.Addr().String()
	cfg.SocketPath = filepath.Join(t.TempDir(), "not-created.sock")
	s := &Server{config: cfg}
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("TCP bind collision accepted")
	}
	if _, err := os.Stat(cfg.SocketPath); !os.IsNotExist(err) {
		t.Fatal("Unix listener opened despite bind failure")
	}
}
