package mtls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestDisabledReturnsInsecureCredentials(t *testing.T) {
	server, err := ServerCredentials(Config{Enabled: false})
	if err != nil {
		t.Fatalf("server credentials: %v", err)
	}
	if got := server.Info().SecurityProtocol; got != "insecure" {
		t.Fatalf("server security protocol = %q, want insecure", got)
	}
	client, err := ClientCredentials(Config{Enabled: false})
	if err != nil {
		t.Fatalf("client credentials: %v", err)
	}
	if got := client.Info().SecurityProtocol; got != "insecure" {
		t.Fatalf("client security protocol = %q, want insecure", got)
	}
}

func TestEnabledRejectsPartialConfig(t *testing.T) {
	if _, err := ServerCredentials(Config{Enabled: true, CertFile: "cert", KeyFile: "key"}); err == nil {
		t.Fatal("expected error when CA file is missing")
	}
	if _, err := ClientCredentials(Config{Enabled: true, CertFile: "cert", CAFile: "ca"}); err == nil {
		t.Fatal("expected error when key file is missing")
	}
}

func TestEnabledRejectsEmptyCA(t *testing.T) {
	ca := newCA(t)
	certFile, keyFile := ca.writeLeaf(t, "server")
	emptyCA := filepath.Join(t.TempDir(), "empty-ca.pem")
	if err := os.WriteFile(emptyCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write empty ca: %v", err)
	}
	if _, err := ServerCredentials(Config{Enabled: true, CertFile: certFile, KeyFile: keyFile, CAFile: emptyCA}); err == nil {
		t.Fatal("expected error for CA file with no certificates")
	}
}

// TestHandshakeAcceptsTrustedClient is the defining test: a client holding a
// certificate signed by the shared CA completes an RPC, and mutual TLS is in
// force on both ends.
func TestHandshakeAcceptsTrustedClient(t *testing.T) {
	ca := newCA(t)
	serverCert, serverKey := ca.writeLeaf(t, "server")
	clientCert, clientKey := ca.writeLeaf(t, "client")
	caFile := ca.writeCA(t)

	addr := startHealthServer(t, Config{Enabled: true, CertFile: serverCert, KeyFile: serverKey, CAFile: caFile})

	clientCreds, err := ClientCredentials(Config{Enabled: true, CertFile: clientCert, KeyFile: clientKey, CAFile: caFile})
	if err != nil {
		t.Fatalf("client credentials: %v", err)
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check over mTLS failed: %v", err)
	}
}

// TestHandshakeRejectsUntrustedClient proves the server refuses a client whose
// certificate is signed by a different CA — the trusted-network hole is closed.
func TestHandshakeRejectsUntrustedClient(t *testing.T) {
	ca := newCA(t)
	serverCert, serverKey := ca.writeLeaf(t, "server")
	caFile := ca.writeCA(t)

	rogue := newCA(t)
	rogueCert, rogueKey := rogue.writeLeaf(t, "client")

	addr := startHealthServer(t, Config{Enabled: true, CertFile: serverCert, KeyFile: serverKey, CAFile: caFile})

	// The rogue client trusts the real server CA (so it dials) but presents a
	// certificate the server will not accept.
	clientCreds, err := ClientCredentials(Config{Enabled: true, CertFile: rogueCert, KeyFile: rogueKey, CAFile: caFile})
	if err != nil {
		t.Fatalf("client credentials: %v", err)
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("expected untrusted client to be rejected")
	}
}

func startHealthServer(t *testing.T, cfg Config) string {
	t.Helper()
	creds, err := ServerCredentials(cfg)
	if err != nil {
		t.Fatalf("server credentials: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

type testCA struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certPEM []byte
}

func newCA(t *testing.T) *testCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	return &testCA{cert: cert, key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// writeLeaf issues a leaf certificate valid for 127.0.0.1 (matching the test
// listener) with both server and client auth usages, and returns its cert and
// key file paths.
func (ca *testCA) writeLeaf(t *testing.T, cn string) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, cn+".crt")
	keyFile = filepath.Join(dir, cn+".key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return certFile, keyFile
}

func (ca *testCA) writeCA(t *testing.T) string {
	t.Helper()
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, ca.certPEM, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return caFile
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
