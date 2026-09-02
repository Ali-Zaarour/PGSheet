package dbconn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectInput is what the operator types. It lives for one session and is
// never written anywhere.
type ConnectInput struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Database   string `json:"database"`
	User       string `json:"user"`
	Password   string `json:"password"`
	SSLMode    string `json:"sslMode"`
	CACertPath string `json:"caCertPath"`
}

// SSLModes in increasing strictness. prefer is the default: encrypts where
// the server supports it, still connects where it does not.
var SSLModes = []string{"disable", "prefer", "require", "verify-ca", "verify-full"}

var sslModeSet = func() map[string]bool {
	m := make(map[string]bool, len(SSLModes))
	for _, s := range SSLModes {
		m[s] = true
	}
	return m
}()

// Pool is the connection pool for one session.
type Pool struct {
	*pgxpool.Pool
	Info ServerInfo
}

// Connect opens the pool and probes the server. MaxConns is 4: introspection,
// uniqueness checks and the dry run overlap, and one connection would
// serialize them.
func Connect(ctx context.Context, in ConnectInput) (*Pool, error) {
	cfg, err := buildConfig(in)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, Redact(err, in.Password)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, Redact(err, in.Password)
	}

	info, err := Probe(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, Redact(err, in.Password)
	}

	return &Pool{Pool: pool, Info: info}, nil
}

// buildConfig assembles the pgx configuration without concatenating operator
// input into a DSN. Only the allowlisted SSL mode reaches the connection
// string; everything else is set as a field, where quoting cannot go wrong.
func buildConfig(in ConnectInput) (*pgxpool.Config, error) {
	mode := strings.TrimSpace(strings.ToLower(in.SSLMode))
	if mode == "" {
		mode = "prefer"
	}
	if !sslModeSet[mode] {
		return nil, fmt.Errorf("unknown SSL mode %q", in.SSLMode)
	}
	if in.Host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if in.Database == "" {
		return nil, fmt.Errorf("database is required")
	}
	port := in.Port
	if port == 0 {
		port = 5432
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", in.Port)
	}

	cfg, err := pgxpool.ParseConfig("sslmode=" + mode)
	if err != nil {
		return nil, fmt.Errorf("connection configuration: %w", err)
	}

	cfg.MaxConns = 4
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	cc := cfg.ConnConfig
	cc.Host = in.Host
	cc.Port = uint16(port)
	cc.Database = in.Database
	cc.User = in.User
	cc.Password = in.Password
	cc.ConnectTimeout = 10 * time.Second
	cc.RuntimeParams = map[string]string{
		"application_name": "pgsheet",
		// Introspection names every schema explicitly, so the search path is
		// deliberately minimal: nothing should resolve by accident.
		"search_path": "pg_catalog",
	}

	if in.CACertPath != "" {
		if cc.TLSConfig == nil {
			cc.TLSConfig = &tls.Config{ServerName: in.Host, MinVersion: tls.VersionTLS12}
		}
		pem, err := os.ReadFile(in.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s does not contain a PEM certificate", in.CACertPath)
		}
		cc.TLSConfig.RootCAs = pool
	}

	return cfg, nil
}

// Close releases the pool.
func (p *Pool) Close() {
	if p == nil || p.Pool == nil {
		return
	}
	p.Pool.Close()
}

// Redact removes a password from an error before it reaches a log or the
// frontend. pgx error strings can include the connection string.
func Redact(err error, password string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if password != "" {
		msg = strings.ReplaceAll(msg, password, "********")
	}
	return fmt.Errorf("%s", msg)
}

// ConnectDSN opens a pool from a connection string, for the CLI. The password
// is supplied separately and never taken from the DSN: one with a password in
// it lands in shell history and process listings.
func ConnectDSN(ctx context.Context, dsn, password string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("connection string: %w", err)
	}

	cfg.MaxConns = 4
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute

	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	if password != "" {
		cfg.ConnConfig.Password = password
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "pgsheet-cli"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, Redact(err, password)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, Redact(err, password)
	}

	info, err := Probe(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, Redact(err, password)
	}
	return &Pool{Pool: pool, Info: info}, nil
}
