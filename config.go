package pgxext

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config wraps pgxpool.Config.
type Config struct {
	*pgxpool.Config
}

// NewConfig creates a default Config.
func NewConfig() *Config {
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		panic(fmt.Sprintf("pgxext: init config: %v", err))
	}
	return &Config{Config: cfg}
}

func (config *Config) ensureConnConfig() {
	if config.ConnConfig == nil {
		cfg, err := pgx.ParseConfig("")
		if err != nil {
			panic(fmt.Sprintf("pgxext: init conn config: %v", err))
		}
		config.ConnConfig = cfg
	}
}

func (config *Config) ensureRuntimeParams() {
	config.ensureConnConfig()
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
}

func (config *Config) ensureTLSConfig() {
	config.ensureConnConfig()
	if config.ConnConfig.TLSConfig == nil {
		config.ConnConfig.TLSConfig = &tls.Config{}
	}
}

// WithHost sets the host.
func (config *Config) WithHost(host string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Host = host
	return config
}

// WithPort sets the port.
func (config *Config) WithPort(port uint16) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Port = port
	return config
}

// WithDatabase sets the database.
func (config *Config) WithDatabase(database string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Database = database
	return config
}

// WithUser sets the user.
func (config *Config) WithUser(user string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.User = user
	return config
}

// WithPassword sets the password.
func (config *Config) WithPassword(password string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Password = password
	return config
}

// WithConnectTimeout sets the connect timeout.
func (config *Config) WithConnectTimeout(timeout time.Duration) *Config {
	config.ensureConnConfig()
	config.ConnConfig.ConnectTimeout = timeout
	return config
}

// -- pgxpool lifecycle hooks --------------------------------------------------

// WithBeforeConnect sets BeforeConnect.
func (config *Config) WithBeforeConnect(fn func(context.Context, *pgx.ConnConfig) error) *Config {
	config.BeforeConnect = fn
	return config
}

// WithAfterConnect sets AfterConnect.
func (config *Config) WithAfterConnect(fn func(context.Context, *pgx.Conn) error) *Config {
	config.AfterConnect = fn
	return config
}

// WithPrepareConn sets PrepareConn.
func (config *Config) WithPrepareConn(fn func(context.Context, *pgx.Conn) (bool, error)) *Config {
	config.PrepareConn = fn
	return config
}

// WithBeforeAcquire sets BeforeAcquire.
//
// Deprecated: prefer WithPrepareConn.
func (config *Config) WithBeforeAcquire(fn func(context.Context, *pgx.Conn) bool) *Config {
	config.BeforeAcquire = fn //nolint:staticcheck
	return config
}

// WithAfterRelease sets AfterRelease.
func (config *Config) WithAfterRelease(fn func(*pgx.Conn) bool) *Config {
	config.AfterRelease = fn
	return config
}

// WithBeforeClose sets BeforeClose.
func (config *Config) WithBeforeClose(fn func(*pgx.Conn)) *Config {
	config.BeforeClose = fn
	return config
}

// WithShouldPing sets ShouldPing.
func (config *Config) WithShouldPing(fn func(context.Context, pgxpool.ShouldPingParams) bool) *Config {
	config.ShouldPing = fn
	return config
}

// -- pgconn lifecycle hooks --------------------------------------------------

// WithDialFunc sets DialFunc.
func (config *Config) WithDialFunc(fn pgconn.DialFunc) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.DialFunc = fn
	return config
}

// WithLookupFunc sets LookupFunc.
func (config *Config) WithLookupFunc(fn pgconn.LookupFunc) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.LookupFunc = fn
	return config
}

// WithValidateConnect sets ValidateConnect.
func (config *Config) WithValidateConnect(fn pgconn.ValidateConnectFunc) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.ValidateConnect = fn
	return config
}

// WithOnNotice sets OnNotice.
func (config *Config) WithOnNotice(fn pgconn.NoticeHandler) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.OnNotice = fn
	return config
}

// WithOnNotification sets OnNotification.
func (config *Config) WithOnNotification(fn pgconn.NotificationHandler) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.OnNotification = fn
	return config
}

// WithOnPgError sets OnPgError.
func (config *Config) WithOnPgError(fn pgconn.PgErrorHandler) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.OnPgError = fn
	return config
}

// WithSSLMode sets the PostgreSQL sslmode.
func (config *Config) WithSSLMode(mode string) *Config {
	config.ensureConnConfig()
	switch mode {
	case "disable":
		config.ConnConfig.TLSConfig = nil
	case "require":
		config.ensureTLSConfig()
		config.ConnConfig.TLSConfig.InsecureSkipVerify = true //nolint:gosec // intentional for sslmode=require
		config.ConnConfig.TLSConfig.VerifyPeerCertificate = nil
	case "verify-ca":
		config.ensureTLSConfig()
		tlsCfg := config.ConnConfig.TLSConfig
		// Skip the built-in hostname check but still verify the certificate chain.
		tlsCfg.InsecureSkipVerify = true //nolint:gosec
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			certs := make([]*x509.Certificate, len(rawCerts))
			for i, raw := range rawCerts {
				var err error
				if certs[i], err = x509.ParseCertificate(raw); err != nil {
					return fmt.Errorf("pgxext: parse server cert: %w", err)
				}
			}
			opts := x509.VerifyOptions{
				Roots:         tlsCfg.RootCAs,
				Intermediates: x509.NewCertPool(),
			}
			for _, c := range certs[1:] {
				opts.Intermediates.AddCert(c)
			}
			if _, err := certs[0].Verify(opts); err != nil {
				return fmt.Errorf("pgxext: verify server cert chain: %w", err)
			}
			return nil
		}
	default: // "verify-full" and any unrecognised value
		config.ensureTLSConfig()
		config.ConnConfig.TLSConfig.InsecureSkipVerify = false
		config.ConnConfig.TLSConfig.VerifyPeerCertificate = nil
	}
	return config
}

// WithSSLRootCert loads a root certificate.
func (config *Config) WithSSLRootCert(rootCertFile string) (*Config, error) {
	data, err := os.ReadFile(rootCertFile)
	if err != nil {
		return nil, fmt.Errorf("pgxext: read root cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("pgxext: no valid PEM certificates in %s", rootCertFile)
	}
	config.ensureTLSConfig()
	config.ConnConfig.TLSConfig.RootCAs = pool
	return config, nil
}

// WithSSLClientCert loads a client certificate.
func (config *Config) WithSSLClientCert(certFile, keyFile, password string) (*Config, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("pgxext: read cert file: %w", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("pgxext: read key file: %w", err)
	}
	if password != "" {
		if keyPEM, err = decryptPEMKey(keyPEM, password); err != nil {
			return nil, fmt.Errorf("pgxext: decrypt key: %w", err)
		}
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("pgxext: load client cert: %w", err)
	}
	config.ensureTLSConfig()
	config.ConnConfig.TLSConfig.Certificates = append(config.ConnConfig.TLSConfig.Certificates, cert)
	return config, nil
}

// decryptPEMKey decrypts a PEM key.
func decryptPEMKey(keyPEM []byte, password string) ([]byte, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in key file")
	}
	//nolint:staticcheck // x509.DecryptPEMBlock is the standard way to handle
	// traditionally-encrypted PEM keys produced by OpenSSL / PostgreSQL tooling.
	decrypted, err := x509.DecryptPEMBlock(block, []byte(password))
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: decrypted}), nil
}

// WithApplicationName sets application_name.
func (config *Config) WithApplicationName(name string) *Config {
	config.ensureRuntimeParams()
	config.ConnConfig.RuntimeParams["application_name"] = name
	return config
}

// WithSearchPath sets search_path.
func (config *Config) WithSearchPath(path string) *Config {
	config.ensureRuntimeParams()
	config.ConnConfig.RuntimeParams["search_path"] = path
	return config
}

// WithTimezone sets TimeZone.
func (config *Config) WithTimezone(tz string) *Config {
	config.ensureRuntimeParams()
	config.ConnConfig.RuntimeParams["TimeZone"] = tz
	return config
}

// WithMaxConns sets MaxConns.
func (config *Config) WithMaxConns(n int32) *Config {
	config.MaxConns = n
	return config
}

// WithMinConns sets MinConns.
func (config *Config) WithMinConns(n int32) *Config {
	config.MinConns = n
	return config
}

// WithMaxConnLifetime sets MaxConnLifetime.
func (config *Config) WithMaxConnLifetime(d time.Duration) *Config {
	config.MaxConnLifetime = d
	return config
}

// WithMaxConnIdleTime sets MaxConnIdleTime.
func (config *Config) WithMaxConnIdleTime(d time.Duration) *Config {
	config.MaxConnIdleTime = d
	return config
}

// WithHealthCheckPeriod sets HealthCheckPeriod.
func (config *Config) WithHealthCheckPeriod(d time.Duration) *Config {
	config.HealthCheckPeriod = d
	return config
}

// WithURL parses a PostgreSQL URL.
func (config *Config) WithURL(rawURL string) (*Config, error) {
	poolCfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return nil, fmt.Errorf("pgxext: parse url: %w", err)
	}
	config.Config = poolCfg

	return config, nil
}
