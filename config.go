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

// Config extends pgxpool.Config with session parameters not directly
// representable as fields in the underlying pgxpool/pgconn structs.
type Config struct {
	*pgxpool.Config
}

// NewConfig returns a Config initialised from an empty DSN via pgxpool.ParseConfig.
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

// WithHost sets the database server host.
func (config *Config) WithHost(host string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Host = host
	return config
}

// WithPort sets the database server port.
func (config *Config) WithPort(port uint16) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Port = port
	return config
}

// WithDatabase sets the database name.
func (config *Config) WithDatabase(database string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Database = database
	return config
}

// WithUser sets the database user.
func (config *Config) WithUser(user string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.User = user
	return config
}

// WithPassword sets the database password.
func (config *Config) WithPassword(password string) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Password = password
	return config
}

// WithConnectTimeout sets the maximum wait for a connection to be established.
func (config *Config) WithConnectTimeout(timeout time.Duration) *Config {
	config.ensureConnConfig()
	config.ConnConfig.ConnectTimeout = timeout
	return config
}

// -- pgxpool lifecycle hooks --------------------------------------------------

// WithBeforeConnect sets a hook called before each new connection is dialed.
// The pgx.ConnConfig copy passed to fn is not shared with any open connection.
func (config *Config) WithBeforeConnect(fn func(context.Context, *pgx.ConnConfig) error) *Config {
	config.BeforeConnect = fn
	return config
}

// WithAfterConnect sets a hook called after a connection is established,
// before it is added to the pool.
func (config *Config) WithAfterConnect(fn func(context.Context, *pgx.Conn) error) *Config {
	config.AfterConnect = fn
	return config
}

// WithPrepareConn sets a hook called before a connection is acquired from the
// pool. Return (true, nil) to allow, (false, nil) to destroy and retry on a
// new connection, or (_, err) to surface an error to the caller.
func (config *Config) WithPrepareConn(fn func(context.Context, *pgx.Conn) (bool, error)) *Config {
	config.PrepareConn = fn
	return config
}

// WithBeforeAcquire sets a hook called before a connection is acquired from
// the pool. Deprecated: prefer WithPrepareConn.
func (config *Config) WithBeforeAcquire(fn func(context.Context, *pgx.Conn) bool) *Config {
	config.BeforeAcquire = fn //nolint:staticcheck
	return config
}

// WithAfterRelease sets a hook called after a connection is released but
// before it is returned to the pool. Return false to destroy the connection.
func (config *Config) WithAfterRelease(fn func(*pgx.Conn) bool) *Config {
	config.AfterRelease = fn
	return config
}

// WithBeforeClose sets a hook called just before a connection is closed and
// removed from the pool.
func (config *Config) WithBeforeClose(fn func(*pgx.Conn)) *Config {
	config.BeforeClose = fn
	return config
}

// WithShouldPing sets a hook that decides whether an acquired connection
// should be pinged to check liveness before use.
func (config *Config) WithShouldPing(fn func(context.Context, pgxpool.ShouldPingParams) bool) *Config {
	config.ShouldPing = fn
	return config
}

// -- pgconn lifecycle hooks --------------------------------------------------

// WithDialFunc overrides the function used to establish the underlying network
// connection (e.g. to use a custom dialer or proxy).
func (config *Config) WithDialFunc(fn pgconn.DialFunc) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.DialFunc = fn
	return config
}

// WithLookupFunc overrides the DNS resolver used during connection.
func (config *Config) WithLookupFunc(fn pgconn.LookupFunc) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.LookupFunc = fn
	return config
}

// WithValidateConnect sets a hook called after authentication succeeds.
// Return an error to reject the connection and try the next fallback.
func (config *Config) WithValidateConnect(fn pgconn.ValidateConnectFunc) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.ValidateConnect = fn
	return config
}

// WithOnNotice sets a callback invoked whenever the server sends a notice.
func (config *Config) WithOnNotice(fn pgconn.NoticeHandler) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.OnNotice = fn
	return config
}

// WithOnNotification sets a callback invoked on LISTEN/NOTIFY notifications.
func (config *Config) WithOnNotification(fn pgconn.NotificationHandler) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.OnNotification = fn
	return config
}

// WithOnPgError sets a callback invoked when the server returns a Postgres error.
// Return false to close the connection; return true to keep it open.
func (config *Config) WithOnPgError(fn pgconn.PgErrorHandler) *Config {
	config.ensureConnConfig()
	config.ConnConfig.Config.OnPgError = fn
	return config
}

// WithSSLMode configures ConnConfig.TLSConfig according to the PostgreSQL sslmode:
//   - "disable"     — TLS is disabled entirely.
//   - "require"     — TLS is required; server certificate is not verified.
//   - "verify-ca"   — TLS is required; certificate chain is verified against RootCAs
//     but the server hostname is not checked.
//   - "verify-full" — TLS is required; both certificate chain and hostname are verified.
func (config *Config) WithSSLMode(mode string) *Config {
	config.ensureConnConfig()
	switch mode {
	case "disable":
		config.ConnConfig.TLSConfig = nil
	case "require":
		config.ensureTLSConfig()
		config.ConnConfig.TLSConfig.InsecureSkipVerify = true //nolint:gosec // intentional for sslmode=require
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
	}
	return config
}

// WithSSLRootCert loads the PEM-encoded CA certificate from rootCertFile and
// registers it as the trusted root for TLS verification.
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

// WithSSLClientCert loads the client certificate and private key from certFile
// and keyFile, building a ready-to-use tls.Certificate inside ConnConfig.TLSConfig.
// Pass a non-empty password if the private key is PEM-encrypted; otherwise pass "".
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

// decryptPEMKey decrypts a traditionally PEM-encrypted private key block.
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

// WithApplicationName sets the application_name runtime parameter.
func (config *Config) WithApplicationName(name string) *Config {
	config.ensureRuntimeParams()
	config.ConnConfig.RuntimeParams["application_name"] = name
	return config
}

// WithSearchPath sets the search_path runtime parameter.
func (config *Config) WithSearchPath(path string) *Config {
	config.ensureRuntimeParams()
	config.ConnConfig.RuntimeParams["search_path"] = path
	return config
}

// WithTimezone sets the TimeZone runtime parameter.
func (config *Config) WithTimezone(tz string) *Config {
	config.ensureRuntimeParams()
	config.ConnConfig.RuntimeParams["TimeZone"] = tz
	return config
}

// WithMaxConns sets the maximum number of connections in the pool.
// Maps to pgxpool.Config.MaxConns.
func (config *Config) WithMaxConns(n int32) *Config {
	config.MaxConns = n
	return config
}

// WithMinConns sets the minimum number of connections in the pool.
// Maps to pgxpool.Config.MinConns.
func (config *Config) WithMinConns(n int32) *Config {
	config.MinConns = n
	return config
}

// WithMaxConnLifetime sets the maximum lifetime of a pooled connection.
// Maps to pgxpool.Config.MaxConnLifetime.
func (config *Config) WithMaxConnLifetime(d time.Duration) *Config {
	config.MaxConnLifetime = d
	return config
}

// WithMaxConnIdleTime sets the maximum idle time before a connection is closed.
// Maps to pgxpool.Config.MaxConnIdleTime.
func (config *Config) WithMaxConnIdleTime(d time.Duration) *Config {
	config.MaxConnIdleTime = d
	return config
}

// WithHealthCheckPeriod sets the interval between health checks on idle connections.
// Maps to pgxpool.Config.HealthCheckPeriod.
func (config *Config) WithHealthCheckPeriod(d time.Duration) *Config {
	config.HealthCheckPeriod = d
	return config
}

// WithURL parses a PostgreSQL connection URL and fills all Config fields.
// Delegates entirely to pgxpool.ParseConfig, which handles all standard params,
// SSL (builds ConnConfig.TLSConfig directly), pool_* params, and runtime params.
//
// Example:
//
//	postgres://alice:secret@localhost:5432/mydb?sslmode=verify-full&pool_max_conns=10
func (config *Config) WithURL(rawURL string) (*Config, error) {
	poolCfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return nil, fmt.Errorf("pgxext: parse url: %w", err)
	}
	config.Config = poolCfg

	return config, nil
}
