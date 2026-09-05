// Package mtls builds the transport credentials that secure internal gRPC.
//
// It mirrors the shape of internal/auth: one small constructor per role, a
// single feature flag, and a safe default. With mTLS disabled the credentials
// fall back to insecure transport so local development and the existing test
// suites keep running unchanged; enabling it makes every internal hop present
// and verify a certificate signed by the shared CA.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config is the transport-security settings for one service. Enabled is the
// only field consulted when mTLS is off; the file paths are required once it is
// on and are validated by the config loader before they reach here.
type Config struct {
	Enabled  bool
	CertFile string
	KeyFile  string
	CAFile   string
}

// ServerCredentials builds credentials for a gRPC server. Enabled, the server
// presents its leaf certificate and requires every client to present one signed
// by the same CA — this is the half that closes the trusted-network assumption.
// Disabled, it returns insecure credentials.
func ServerCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if !cfg.Enabled {
		return insecure.NewCredentials(), nil
	}
	cert, pool, err := load(cfg)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// ClientCredentials builds credentials for a gRPC client. The client presents
// the same leaf certificate as its identity and verifies the server against the
// shared CA; the verified name comes from the dial target's host, which must
// appear in the server certificate's SANs. Disabled, it returns insecure
// credentials.
func ClientCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if !cfg.Enabled {
		return insecure.NewCredentials(), nil
	}
	cert, pool, err := load(cfg)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// load reads the leaf key pair and the CA bundle. The three paths are validated
// as a set: a partial configuration is a misconfiguration, not a fallback.
func load(cfg Config) (tls.Certificate, *x509.CertPool, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "" {
		return tls.Certificate{}, nil, errors.New("mtls: cert, key, and CA files are all required when enabled")
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("mtls: load key pair: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("mtls: read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return tls.Certificate{}, nil, errors.New("mtls: CA file contained no certificates")
	}
	return cert, pool, nil
}
