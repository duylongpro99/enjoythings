// Package mtls builds the transport credentials that secure internal gRPC.
//
// It mirrors the shape of internal/auth: one small constructor per role, a
// single feature flag, and a safe default. With mTLS disabled the credentials
// fall back to insecure transport so local development and the existing test
// suites keep running unchanged; enabling it makes every internal hop present
// and verify a certificate signed by the shared CA.
//
// The enabled credentials re-read their files before each handshake, so a
// renewal that rewrites the leaf or CA on disk — cert-manager updating a
// mounted Secret, an operator re-running the generator — takes effect on the
// next connection without a restart.
package mtls

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"

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
	return newRotating(cfg, serverTLS)
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
	return newRotating(cfg, clientTLS)
}

func serverTLS(cert tls.Certificate, pool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}
}

func clientTLS(cert tls.Certificate, pool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}
}

// material is the raw PEM of the three files, read together so that comparing
// it against the last load decides whether anything changed. Content rather
// than modtime is compared: it is immune to filesystem timestamp granularity
// and to the symlink swap Kubernetes uses to update a mounted Secret.
type material struct {
	cert, key, ca []byte
}

func readMaterial(cfg Config) (material, error) {
	var m material
	var err error
	if m.cert, err = os.ReadFile(cfg.CertFile); err != nil {
		return material{}, fmt.Errorf("mtls: read cert file: %w", err)
	}
	if m.key, err = os.ReadFile(cfg.KeyFile); err != nil {
		return material{}, fmt.Errorf("mtls: read key file: %w", err)
	}
	if m.ca, err = os.ReadFile(cfg.CAFile); err != nil {
		return material{}, fmt.Errorf("mtls: read CA file: %w", err)
	}
	return m, nil
}

func (m material) equal(o material) bool {
	return bytes.Equal(m.cert, o.cert) && bytes.Equal(m.key, o.key) && bytes.Equal(m.ca, o.ca)
}

// parse turns raw material into gRPC credentials for one role. The leaf is
// returned alongside so callers can log what was loaded.
func parse(m material, build func(tls.Certificate, *x509.CertPool) *tls.Config) (credentials.TransportCredentials, *x509.Certificate, error) {
	cert, err := tls.X509KeyPair(m.cert, m.key)
	if err != nil {
		return nil, nil, fmt.Errorf("mtls: load key pair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(m.ca) {
		return nil, nil, errors.New("mtls: CA file contained no certificates")
	}
	return credentials.NewTLS(build(cert, pool)), cert.Leaf, nil
}

// rotating is a credentials.TransportCredentials that re-reads the leaf and CA
// files before every handshake and swaps in fresh credentials when they have
// changed on disk. A read or parse failure — a half-written file mid-rotation,
// a new key whose certificate has not landed yet — is logged and the last good
// credentials keep serving, so a rotation in progress never fails a handshake.
//
// It wraps credentials.NewTLS rather than using tls.Config callbacks so that
// both the leaf and the trust pool rotate through one path, on both roles,
// without disabling the standard library's verification.
type rotating struct {
	cfg   Config
	build func(tls.Certificate, *x509.CertPool) *tls.Config

	mu    sync.Mutex
	mat   material
	creds credentials.TransportCredentials
}

// newRotating loads the material once, eagerly: a missing or invalid file at
// startup is a misconfiguration and fails fast, exactly as before.
func newRotating(cfg Config, build func(tls.Certificate, *x509.CertPool) *tls.Config) (*rotating, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "" {
		return nil, errors.New("mtls: cert, key, and CA files are all required when enabled")
	}
	mat, err := readMaterial(cfg)
	if err != nil {
		return nil, err
	}
	creds, _, err := parse(mat, build)
	if err != nil {
		return nil, err
	}
	return &rotating{cfg: cfg, build: build, mat: mat, creds: creds}, nil
}

// current returns credentials built from what is on disk now, or the last good
// set when the files are unreadable or not yet consistent with each other.
func (r *rotating) current() credentials.TransportCredentials {
	r.mu.Lock()
	defer r.mu.Unlock()
	mat, err := readMaterial(r.cfg)
	if err == nil {
		if mat.equal(r.mat) {
			return r.creds
		}
		var creds credentials.TransportCredentials
		var leaf *x509.Certificate
		if creds, leaf, err = parse(mat, r.build); err == nil {
			r.mat, r.creds = mat, creds
			slog.Info("mtls: certificate reloaded", "cert_file", r.cfg.CertFile, "not_after", leaf.NotAfter)
			return creds
		}
	}
	slog.Warn("mtls: certificate reload failed, keeping previous", "cert_file", r.cfg.CertFile, "error", err)
	return r.creds
}

func (r *rotating) ClientHandshake(ctx context.Context, authority string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return r.current().ClientHandshake(ctx, authority, conn)
}

func (r *rotating) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return r.current().ServerHandshake(conn)
}

func (r *rotating) Info() credentials.ProtocolInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.creds.Info()
}

func (r *rotating) Clone() credentials.TransportCredentials {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &rotating{cfg: r.cfg, build: r.build, mat: r.mat, creds: r.creds}
}

// OverrideServerName is deprecated in gRPC and never called by it; the
// authority is set with grpc.WithAuthority instead.
func (r *rotating) OverrideServerName(string) error {
	return errors.New("mtls: OverrideServerName is not supported; use grpc.WithAuthority")
}
