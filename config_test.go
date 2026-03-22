package pgxext

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newCfg() *Config { return NewConfig() }

// assertSamePointer fails if got != want.
func assertSamePointer(t *testing.T, want, got *Config) {
	t.Helper()
	if got != want {
		t.Errorf("expected method to return same *Config pointer for chaining")
	}
}

// generateCA creates a self-signed CA certificate and RSA key.
func generateCA(t *testing.T) (caPEM, caKeyPEM []byte, caCert *x509.Certificate, caKey *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		parsed, key
}

// generateClientCert creates a client certificate signed by caCert/caKey.
func generateClientCert(t *testing.T, caCert *x509.Certificate, caKey *rsa.PrivateKey) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// generateServerCert creates a server certificate (with IP SAN) signed by caCert/caKey.
func generateServerCert(t *testing.T, caCert *x509.Certificate, caKey *rsa.PrivateKey) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}
	return der
}

// writeTempFile writes data to a temp file and registers cleanup.
func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp("", "pgxext-test-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// ---------------------------------------------------------------------------
// NewConfig
// ---------------------------------------------------------------------------

func TestNewConfig(t *testing.T) {
	cfg := NewConfig()
	if cfg == nil {
		t.Fatal("NewConfig returned nil")
	}
	if cfg.Config == nil {
		t.Fatal("NewConfig: embedded *pgxpool.Config is nil")
	}
}

// ---------------------------------------------------------------------------
// Connection parameters
// ---------------------------------------------------------------------------

func TestWithHost(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithHost("db.example.com")
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Host != "db.example.com" {
		t.Errorf("Host = %q, want %q", cfg.ConnConfig.Host, "db.example.com")
	}
}

func TestWithPort(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithPort(5433)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Port != 5433 {
		t.Errorf("Port = %d, want 5433", cfg.ConnConfig.Port)
	}
}

func TestWithDatabase(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithDatabase("mydb")
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Database != "mydb" {
		t.Errorf("Database = %q, want %q", cfg.ConnConfig.Database, "mydb")
	}
}

func TestWithUser(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithUser("alice")
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.User != "alice" {
		t.Errorf("User = %q, want %q", cfg.ConnConfig.User, "alice")
	}
}

func TestWithPassword(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithPassword("s3cr3t")
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Password != "s3cr3t" {
		t.Errorf("Password = %q, want %q", cfg.ConnConfig.Password, "s3cr3t")
	}
}

func TestWithConnectTimeout(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithConnectTimeout(10 * time.Second)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.ConnectTimeout != 10*time.Second {
		t.Errorf("ConnectTimeout = %v, want 10s", cfg.ConnConfig.ConnectTimeout)
	}
}

// ensureConnConfig must initialise ConnConfig when it is nil.
func TestEnsureConnConfig(t *testing.T) {
	cfg := &Config{Config: &pgxpool.Config{}} // ConnConfig left nil
	cfg.WithHost("localhost")
	if cfg.ConnConfig == nil {
		t.Fatal("ensureConnConfig did not initialise ConnConfig")
	}
}

// ---------------------------------------------------------------------------
// Pool parameters
// ---------------------------------------------------------------------------

func TestWithMaxConns(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithMaxConns(20)
	assertSamePointer(t, cfg, got)
	if cfg.MaxConns != 20 {
		t.Errorf("MaxConns = %d, want 20", cfg.MaxConns)
	}
}

func TestWithMinConns(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithMinConns(2)
	assertSamePointer(t, cfg, got)
	if cfg.MinConns != 2 {
		t.Errorf("MinConns = %d, want 2", cfg.MinConns)
	}
}

func TestWithMaxConnLifetime(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithMaxConnLifetime(time.Hour)
	assertSamePointer(t, cfg, got)
	if cfg.MaxConnLifetime != time.Hour {
		t.Errorf("MaxConnLifetime = %v, want 1h", cfg.MaxConnLifetime)
	}
}

func TestWithMaxConnIdleTime(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithMaxConnIdleTime(30 * time.Minute)
	assertSamePointer(t, cfg, got)
	if cfg.MaxConnIdleTime != 30*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 30m", cfg.MaxConnIdleTime)
	}
}

func TestWithHealthCheckPeriod(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithHealthCheckPeriod(time.Minute)
	assertSamePointer(t, cfg, got)
	if cfg.HealthCheckPeriod != time.Minute {
		t.Errorf("HealthCheckPeriod = %v, want 1m", cfg.HealthCheckPeriod)
	}
}

// ---------------------------------------------------------------------------
// Runtime parameters
// ---------------------------------------------------------------------------

func TestWithApplicationName(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithApplicationName("myapp")
	assertSamePointer(t, cfg, got)
	if v := cfg.ConnConfig.RuntimeParams["application_name"]; v != "myapp" {
		t.Errorf("application_name = %q, want %q", v, "myapp")
	}
}

func TestWithSearchPath(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithSearchPath("myschema,public")
	assertSamePointer(t, cfg, got)
	if v := cfg.ConnConfig.RuntimeParams["search_path"]; v != "myschema,public" {
		t.Errorf("search_path = %q, want %q", v, "myschema,public")
	}
}

func TestWithTimezone(t *testing.T) {
	cfg := newCfg()
	got := cfg.WithTimezone("UTC")
	assertSamePointer(t, cfg, got)
	if v := cfg.ConnConfig.RuntimeParams["TimeZone"]; v != "UTC" {
		t.Errorf("TimeZone = %q, want %q", v, "UTC")
	}
}

// ---------------------------------------------------------------------------
// SSL mode
// ---------------------------------------------------------------------------

func TestWithSSLMode(t *testing.T) {
	tests := []struct {
		mode                 string
		wantNilTLS           bool
		wantInsecureSkip     bool
		wantVerifyPeerNotNil bool
	}{
		{"disable", true, false, false},
		{"require", false, true, false},
		{"verify-ca", false, true, true},
		{"verify-full", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			cfg := newCfg()
			got := cfg.WithSSLMode(tc.mode)
			assertSamePointer(t, cfg, got)

			tlsCfg := cfg.ConnConfig.TLSConfig
			if tc.wantNilTLS {
				if tlsCfg != nil {
					t.Errorf("mode %q: expected TLSConfig == nil", tc.mode)
				}
				return
			}
			if tlsCfg == nil {
				t.Fatalf("mode %q: expected TLSConfig != nil", tc.mode)
			}
			if tlsCfg.InsecureSkipVerify != tc.wantInsecureSkip {
				t.Errorf("mode %q: InsecureSkipVerify = %v, want %v",
					tc.mode, tlsCfg.InsecureSkipVerify, tc.wantInsecureSkip)
			}
			if tc.wantVerifyPeerNotNil && tlsCfg.VerifyPeerCertificate == nil {
				t.Errorf("mode %q: expected VerifyPeerCertificate to be set", tc.mode)
			}
			if !tc.wantVerifyPeerNotNil && tlsCfg.VerifyPeerCertificate != nil {
				t.Errorf("mode %q: expected VerifyPeerCertificate to be nil", tc.mode)
			}
		})
	}
}

// TestWithSSLModeVerifyCA_PeerCertificateCallback tests that the verify-ca
// callback accepts a cert signed by the configured CA and rejects an untrusted one.
func TestWithSSLModeVerifyCA_PeerCertificateCallback(t *testing.T) {
	caPEM, _, caCert, caKey := generateCA(t)
	trustedServerDER := generateServerCert(t, caCert, caKey)

	// Untrusted: different CA
	_, _, otherCA, otherKey := generateCA(t)
	untrustedServerDER := generateServerCert(t, otherCA, otherKey)

	caFile := writeTempFile(t, caPEM)

	cfg := newCfg()
	cfg.WithSSLMode("verify-ca")
	cfg, err := cfg.WithSSLRootCert(caFile)
	if err != nil {
		t.Fatalf("WithSSLRootCert: %v", err)
	}

	cb := cfg.ConnConfig.TLSConfig.VerifyPeerCertificate

	t.Run("trusted cert", func(t *testing.T) {
		if err := cb([][]byte{trustedServerDER}, nil); err != nil {
			t.Errorf("expected nil error for trusted cert, got: %v", err)
		}
	})

	t.Run("untrusted cert", func(t *testing.T) {
		if err := cb([][]byte{untrustedServerDER}, nil); err == nil {
			t.Error("expected error for untrusted cert, got nil")
		}
	})

	t.Run("malformed cert", func(t *testing.T) {
		if err := cb([][]byte{[]byte("not a cert")}, nil); err == nil {
			t.Error("expected error for malformed cert, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// WithSSLRootCert
// ---------------------------------------------------------------------------

func TestWithSSLRootCert(t *testing.T) {
	t.Run("valid CA file", func(t *testing.T) {
		caPEM, _, _, _ := generateCA(t)
		caFile := writeTempFile(t, caPEM)

		cfg := newCfg()
		got, err := cfg.WithSSLRootCert(caFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertSamePointer(t, cfg, got)
		if cfg.ConnConfig.TLSConfig == nil || cfg.ConnConfig.TLSConfig.RootCAs == nil {
			t.Error("RootCAs not set")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		cfg := newCfg()
		_, err := cfg.WithSSLRootCert("/nonexistent/ca.crt")
		if err == nil {
			t.Error("expected error for missing file, got nil")
		}
	})

	t.Run("file with no valid PEM", func(t *testing.T) {
		badFile := writeTempFile(t, []byte("not a certificate"))
		cfg := newCfg()
		_, err := cfg.WithSSLRootCert(badFile)
		if err == nil {
			t.Error("expected error for invalid PEM, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// WithSSLClientCert
// ---------------------------------------------------------------------------

func TestWithSSLClientCert(t *testing.T) {
	caPEM, _, caCert, caKey := generateCA(t)
	clientCertPEM, clientKeyPEM := generateClientCert(t, caCert, caKey)

	t.Run("valid cert and key", func(t *testing.T) {
		certFile := writeTempFile(t, clientCertPEM)
		keyFile := writeTempFile(t, clientKeyPEM)

		cfg := newCfg()
		got, err := cfg.WithSSLClientCert(certFile, keyFile, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertSamePointer(t, cfg, got)
		if len(cfg.ConnConfig.TLSConfig.Certificates) != 1 {
			t.Errorf("Certificates len = %d, want 1", len(cfg.ConnConfig.TLSConfig.Certificates))
		}
	})

	t.Run("multiple certs appended", func(t *testing.T) {
		certFile := writeTempFile(t, clientCertPEM)
		keyFile := writeTempFile(t, clientKeyPEM)

		cfg := newCfg()
		cfg.WithSSLMode("require")                   // initialise TLSConfig with existing cert list
		cfg.WithSSLClientCert(certFile, keyFile, "") //nolint:errcheck
		cfg.WithSSLClientCert(certFile, keyFile, "") //nolint:errcheck
		if len(cfg.ConnConfig.TLSConfig.Certificates) != 2 {
			t.Errorf("Certificates len = %d, want 2", len(cfg.ConnConfig.TLSConfig.Certificates))
		}
	})

	t.Run("missing cert file", func(t *testing.T) {
		keyFile := writeTempFile(t, clientKeyPEM)
		cfg := newCfg()
		_, err := cfg.WithSSLClientCert("/no/cert.crt", keyFile, "")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("missing key file", func(t *testing.T) {
		certFile := writeTempFile(t, clientCertPEM)
		cfg := newCfg()
		_, err := cfg.WithSSLClientCert(certFile, "/no/key.key", "")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("mismatched cert and key", func(t *testing.T) {
		_, otherKeyPEM := generateClientCert(t, caCert, caKey) // different key
		certFile := writeTempFile(t, clientCertPEM)
		keyFile := writeTempFile(t, otherKeyPEM)
		cfg := newCfg()
		_, err := cfg.WithSSLClientCert(certFile, keyFile, "")
		if err == nil {
			t.Error("expected error for mismatched cert/key, got nil")
		}
	})

	t.Run("encrypted key with correct password", func(t *testing.T) {
		//nolint:staticcheck
		encBlock, err := x509.EncryptPEMBlock(
			rand.Reader, "RSA PRIVATE KEY",
			x509.MarshalPKCS1PrivateKey(func() *rsa.PrivateKey {
				k, _ := rsa.GenerateKey(rand.Reader, 2048)
				return k
			}()),
			[]byte("testpassword"), x509.PEMCipherAES256,
		)
		if err != nil {
			t.Fatalf("encrypt PEM block: %v", err)
		}
		// Build a cert that matches this key — easier to just test decryptPEMKey directly.
		encKeyPEM := pem.EncodeToMemory(encBlock)
		decrypted, err := decryptPEMKey(encKeyPEM, "testpassword")
		if err != nil {
			t.Fatalf("decryptPEMKey: %v", err)
		}
		if decrypted == nil {
			t.Fatal("decryptPEMKey returned nil")
		}
		block, _ := pem.Decode(decrypted)
		if block == nil {
			t.Fatal("decrypted PEM has no block")
		}
		if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
			t.Errorf("decrypted key is invalid: %v", err)
		}
	})

	t.Run("encrypted key with wrong password", func(t *testing.T) {
		//nolint:staticcheck
		encBlock, _ := x509.EncryptPEMBlock(
			rand.Reader, "RSA PRIVATE KEY",
			x509.MarshalPKCS1PrivateKey(func() *rsa.PrivateKey {
				k, _ := rsa.GenerateKey(rand.Reader, 2048)
				return k
			}()),
			[]byte("correct"), x509.PEMCipherAES256,
		)
		encKeyPEM := pem.EncodeToMemory(encBlock)
		_, err := decryptPEMKey(encKeyPEM, "wrong")
		if err == nil {
			t.Error("expected error for wrong password, got nil")
		}
	})

	t.Run("no PEM block in key file", func(t *testing.T) {
		_, err := decryptPEMKey([]byte("garbage"), "pass")
		if err == nil {
			t.Error("expected error for missing PEM block, got nil")
		}
	})

	_ = caPEM // referenced via generateCA only
}

// ---------------------------------------------------------------------------
// pgxpool lifecycle hooks
// ---------------------------------------------------------------------------

func TestWithBeforeConnect(t *testing.T) {
	fn := func(_ context.Context, _ *pgx.ConnConfig) error { return nil }
	cfg := newCfg()
	got := cfg.WithBeforeConnect(fn)
	assertSamePointer(t, cfg, got)
	if cfg.BeforeConnect == nil {
		t.Error("BeforeConnect not set")
	}
}

func TestWithAfterConnect(t *testing.T) {
	fn := func(_ context.Context, _ *pgx.Conn) error { return nil }
	cfg := newCfg()
	got := cfg.WithAfterConnect(fn)
	assertSamePointer(t, cfg, got)
	if cfg.AfterConnect == nil {
		t.Error("AfterConnect not set")
	}
}

func TestWithPrepareConn(t *testing.T) {
	fn := func(_ context.Context, _ *pgx.Conn) (bool, error) { return true, nil }
	cfg := newCfg()
	got := cfg.WithPrepareConn(fn)
	assertSamePointer(t, cfg, got)
	if cfg.PrepareConn == nil {
		t.Error("PrepareConn not set")
	}
}

func TestWithBeforeAcquire(t *testing.T) {
	fn := func(_ context.Context, _ *pgx.Conn) bool { return true }
	cfg := newCfg()
	got := cfg.WithBeforeAcquire(fn)
	assertSamePointer(t, cfg, got)
	if cfg.BeforeAcquire == nil { //nolint:staticcheck
		t.Error("BeforeAcquire not set")
	}
}

func TestWithAfterRelease(t *testing.T) {
	fn := func(_ *pgx.Conn) bool { return true }
	cfg := newCfg()
	got := cfg.WithAfterRelease(fn)
	assertSamePointer(t, cfg, got)
	if cfg.AfterRelease == nil {
		t.Error("AfterRelease not set")
	}
}

func TestWithBeforeClose(t *testing.T) {
	fn := func(_ *pgx.Conn) {}
	cfg := newCfg()
	got := cfg.WithBeforeClose(fn)
	assertSamePointer(t, cfg, got)
	if cfg.BeforeClose == nil {
		t.Error("BeforeClose not set")
	}
}

func TestWithShouldPing(t *testing.T) {
	fn := func(_ context.Context, _ pgxpool.ShouldPingParams) bool { return false }
	cfg := newCfg()
	got := cfg.WithShouldPing(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ShouldPing == nil {
		t.Error("ShouldPing not set")
	}
}

// ---------------------------------------------------------------------------
// pgconn hooks
// ---------------------------------------------------------------------------

func TestWithDialFunc(t *testing.T) {
	fn := pgconn.DialFunc(func(_ context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("stub")
	})
	cfg := newCfg()
	got := cfg.WithDialFunc(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Config.DialFunc == nil {
		t.Error("DialFunc not set")
	}
}

func TestWithLookupFunc(t *testing.T) {
	fn := pgconn.LookupFunc(func(_ context.Context, host string) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	})
	cfg := newCfg()
	got := cfg.WithLookupFunc(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Config.LookupFunc == nil {
		t.Error("LookupFunc not set")
	}
}

func TestWithValidateConnect(t *testing.T) {
	fn := pgconn.ValidateConnectFunc(func(_ context.Context, _ *pgconn.PgConn) error { return nil })
	cfg := newCfg()
	got := cfg.WithValidateConnect(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Config.ValidateConnect == nil {
		t.Error("ValidateConnect not set")
	}
}

func TestWithOnNotice(t *testing.T) {
	fn := pgconn.NoticeHandler(func(_ *pgconn.PgConn, _ *pgconn.Notice) {})
	cfg := newCfg()
	got := cfg.WithOnNotice(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Config.OnNotice == nil {
		t.Error("OnNotice not set")
	}
}

func TestWithOnNotification(t *testing.T) {
	fn := pgconn.NotificationHandler(func(_ *pgconn.PgConn, _ *pgconn.Notification) {})
	cfg := newCfg()
	got := cfg.WithOnNotification(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Config.OnNotification == nil {
		t.Error("OnNotification not set")
	}
}

func TestWithOnPgError(t *testing.T) {
	fn := pgconn.PgErrorHandler(func(_ *pgconn.PgConn, _ *pgconn.PgError) bool { return true })
	cfg := newCfg()
	got := cfg.WithOnPgError(fn)
	assertSamePointer(t, cfg, got)
	if cfg.ConnConfig.Config.OnPgError == nil {
		t.Error("OnPgError not set")
	}
}

// ---------------------------------------------------------------------------
// WithURL
// ---------------------------------------------------------------------------

func TestWithURL(t *testing.T) {
	t.Run("full URL", func(t *testing.T) {
		const url = "postgres://alice:supersecret@localhost:5432/mydb" +
			"?application_name=myapp" +
			"&search_path=myschema,public" +
			"&connect_timeout=10" +
			"&pool_max_conns=10" +
			"&pool_min_conns=2" +
			"&pool_max_conn_lifetime=1h" +
			"&pool_max_conn_idle_time=30m" +
			"&pool_health_check_period=1m"

		cfg := newCfg()
		got, err := cfg.WithURL(url)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertSamePointer(t, cfg, got)

		checks := []struct {
			name string
			got  any
			want any
		}{
			{"Host", cfg.ConnConfig.Host, "localhost"},
			{"Port", cfg.ConnConfig.Port, uint16(5432)},
			{"User", cfg.ConnConfig.User, "alice"},
			{"Password", cfg.ConnConfig.Password, "supersecret"},
			{"Database", cfg.ConnConfig.Database, "mydb"},
			{"MaxConns", cfg.MaxConns, int32(10)},
			{"MinConns", cfg.MinConns, int32(2)},
			{"MaxConnLifetime", cfg.MaxConnLifetime, time.Hour},
			{"MaxConnIdleTime", cfg.MaxConnIdleTime, 30 * time.Minute},
			{"HealthCheckPeriod", cfg.HealthCheckPeriod, time.Minute},
			{"ConnectTimeout", cfg.ConnConfig.ConnectTimeout, 10 * time.Second},
			{"application_name", cfg.ConnConfig.RuntimeParams["application_name"], "myapp"},
			{"search_path", cfg.ConnConfig.RuntimeParams["search_path"], "myschema,public"},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
			}
		}
	})

	t.Run("sslmode=require sets InsecureSkipVerify", func(t *testing.T) {
		cfg := newCfg()
		_, err := cfg.WithURL("postgres://localhost/mydb?sslmode=require")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tlsCfg := cfg.ConnConfig.TLSConfig
		if tlsCfg == nil {
			t.Fatal("TLSConfig is nil for sslmode=require")
		}
		if !tlsCfg.InsecureSkipVerify {
			t.Error("InsecureSkipVerify should be true for sslmode=require")
		}
	})

	t.Run("sslmode=disable clears TLSConfig", func(t *testing.T) {
		cfg := newCfg()
		_, err := cfg.WithURL("postgres://localhost/mydb?sslmode=disable")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ConnConfig.TLSConfig != nil {
			t.Error("TLSConfig should be nil for sslmode=disable")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		cfg := newCfg()
		_, err := cfg.WithURL("not a url ://")
		if err == nil {
			t.Error("expected error for invalid URL, got nil")
		}
	})

	t.Run("WithURL replaces previous config", func(t *testing.T) {
		cfg := newCfg()
		cfg.WithHost("old-host")
		cfg.WithURL("postgres://new-host/db") //nolint:errcheck
		if cfg.ConnConfig.Host != "new-host" {
			t.Errorf("Host = %q, want %q", cfg.ConnConfig.Host, "new-host")
		}
	})
}

// ---------------------------------------------------------------------------
// Chaining
// ---------------------------------------------------------------------------

func TestChaining(t *testing.T) {
	cfg := newCfg()
	result := cfg.
		WithHost("localhost").
		WithPort(5432).
		WithUser("alice").
		WithPassword("s3cr3t").
		WithDatabase("mydb").
		WithConnectTimeout(5 * time.Second).
		WithApplicationName("app").
		WithSearchPath("public").
		WithTimezone("UTC").
		WithMaxConns(10).
		WithMinConns(1).
		WithMaxConnLifetime(time.Hour).
		WithMaxConnIdleTime(30 * time.Minute).
		WithHealthCheckPeriod(time.Minute).
		WithSSLMode("require")

	if result != cfg {
		t.Error("chain did not return same *Config pointer")
	}
	// Spot-check a few values.
	if cfg.ConnConfig.Host != "localhost" {
		t.Errorf("Host = %q", cfg.ConnConfig.Host)
	}
	if cfg.MaxConns != 10 {
		t.Errorf("MaxConns = %d", cfg.MaxConns)
	}
	if cfg.ConnConfig.TLSConfig == nil {
		t.Error("TLSConfig is nil after WithSSLMode(require)")
	}
}

// ---------------------------------------------------------------------------
// SSL combined: mode + root cert + client cert
// ---------------------------------------------------------------------------

func TestSSLCombined(t *testing.T) {
	caPEM, _, caCert, caKey := generateCA(t)
	clientCertPEM, clientKeyPEM := generateClientCert(t, caCert, caKey)

	caFile := writeTempFile(t, caPEM)
	certFile := writeTempFile(t, clientCertPEM)
	keyFile := writeTempFile(t, clientKeyPEM)

	cfg := newCfg()
	cfg.WithSSLMode("verify-full")
	cfg, err := cfg.WithSSLRootCert(caFile)
	if err != nil {
		t.Fatalf("WithSSLRootCert: %v", err)
	}
	cfg, err = cfg.WithSSLClientCert(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("WithSSLClientCert: %v", err)
	}

	tlsCfg := cfg.ConnConfig.TLSConfig
	if tlsCfg == nil {
		t.Fatal("TLSConfig is nil")
	}
	if tlsCfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false for verify-full")
	}
	if tlsCfg.RootCAs == nil {
		t.Error("RootCAs not set")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("Certificates len = %d, want 1", len(tlsCfg.Certificates))
	}

	// Returned TLSConfig must be directly usable by tls.Client without further processing.
	_ = tls.Config{
		InsecureSkipVerify: tlsCfg.InsecureSkipVerify, //nolint:gosec
		RootCAs:            tlsCfg.RootCAs,
		Certificates:       tlsCfg.Certificates,
	}
}
