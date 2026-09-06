package mtls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
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
	"google.golang.org/grpc/credentials"
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
	if err := healthCheck(addr, clientCreds); err != nil {
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
	if err := healthCheck(addr, clientCreds); err == nil {
		t.Fatal("expected untrusted client to be rejected")
	}
}

// TestRotationServesRenewedLeaf is the rotation test: the server's cert and key
// files are rewritten in place with a renewed leaf and the very next handshake
// presents the new certificate — no restart, no new credentials object.
func TestRotationServesRenewedLeaf(t *testing.T) {
	ca := newCA(t)
	serverCert, serverKey := ca.writeLeaf(t, "server")
	caFile := ca.writeCA(t)
	addr := startHealthServer(t, Config{Enabled: true, CertFile: serverCert, KeyFile: serverKey, CAFile: caFile})

	before := servedLeaf(t, addr, ca)

	renewedCert, renewedKey := ca.issueLeaf(t, "server")
	writeFile(t, serverKey, renewedKey)
	writeFile(t, serverCert, renewedCert)

	after := servedLeaf(t, addr, ca)
	if after.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatal("server still presents the old leaf after its files were rewritten")
	}
}

// TestRotationKeepsLastGoodPairThroughPartialWrite covers a rotation in
// progress: the new key landing before its certificate, then a half-written
// certificate. Neither may fail a handshake — the previous pair keeps serving
// until the files are consistent again.
func TestRotationKeepsLastGoodPairThroughPartialWrite(t *testing.T) {
	ca := newCA(t)
	serverCert, serverKey := ca.writeLeaf(t, "server")
	caFile := ca.writeCA(t)
	addr := startHealthServer(t, Config{Enabled: true, CertFile: serverCert, KeyFile: serverKey, CAFile: caFile})

	before := servedLeaf(t, addr, ca)
	renewedCert, renewedKey := ca.issueLeaf(t, "server")

	writeFile(t, serverKey, renewedKey)
	if got := servedLeaf(t, addr, ca); got.SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatal("expected the previous leaf while the key and certificate are mismatched")
	}

	writeFile(t, serverCert, renewedCert[:len(renewedCert)/2])
	if got := servedLeaf(t, addr, ca); got.SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatal("expected the previous leaf while the certificate is half-written")
	}

	writeFile(t, serverCert, renewedCert)
	if got := servedLeaf(t, addr, ca); got.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatal("expected the renewed leaf once both files are consistent")
	}
}

// TestRotationReloadsCABundleOnBothRoles replaces the trust root itself. Once
// the server has moved to the new CA a client still on the old one is refused,
// which proves the server reloaded; once the client's files move too, the same
// client credentials object succeeds, which proves the client reloaded both its
// leaf and its root pool.
func TestRotationReloadsCABundleOnBothRoles(t *testing.T) {
	ca := newCA(t)
	serverCert, serverKey := ca.writeLeaf(t, "server")
	serverCA := ca.writeCA(t)
	clientCert, clientKey := ca.writeLeaf(t, "client")
	clientCA := ca.writeCA(t)

	addr := startHealthServer(t, Config{Enabled: true, CertFile: serverCert, KeyFile: serverKey, CAFile: serverCA})
	clientCreds, err := ClientCredentials(Config{Enabled: true, CertFile: clientCert, KeyFile: clientKey, CAFile: clientCA})
	if err != nil {
		t.Fatalf("client credentials: %v", err)
	}
	if err := healthCheck(addr, clientCreds); err != nil {
		t.Fatalf("baseline health check failed: %v", err)
	}

	next := newCA(t)
	cert, key := next.issueLeaf(t, "server")
	writeFile(t, serverKey, key)
	writeFile(t, serverCert, cert)
	writeFile(t, serverCA, next.certPEM)
	if err := healthCheck(addr, clientCreds); err == nil {
		t.Fatal("client on the old CA should be refused after the server rotated")
	}

	cert, key = next.issueLeaf(t, "client")
	writeFile(t, clientKey, key)
	writeFile(t, clientCert, cert)
	writeFile(t, clientCA, next.certPEM)
	if err := healthCheck(addr, clientCreds); err != nil {
		t.Fatalf("health check after rotating both sides failed: %v", err)
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

// healthCheck opens a fresh connection with the given credentials and performs
// one RPC, returning the handshake or RPC error.
func healthCheck(addr string, creds credentials.TransportCredentials) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

// servedLeaf completes a raw TLS handshake with the server, presenting a client
// certificate issued by ca, and returns the leaf the server presented.
func servedLeaf(t *testing.T, addr string, ca *testCA) *x509.Certificate {
	t.Helper()
	certPEM, keyPEM := ca.issueLeaf(t, "probe")
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("probe key pair: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.certPEM)
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.ConnectionState().PeerCertificates[0]
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

// issueLeaf issues a leaf certificate valid for 127.0.0.1 (matching the test
// listener) with both server and client auth usages, and returns its cert and
// key as PEM.
func (ca *testCA) issueLeaf(t *testing.T, cn string) (certPEM, keyPEM []byte) {
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
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

// writeLeaf issues a leaf and writes it to a fresh directory, returning the cert
// and key file paths.
func (ca *testCA) writeLeaf(t *testing.T, cn string) (certFile, keyFile string) {
	t.Helper()
	certPEM, keyPEM := ca.issueLeaf(t, cn)
	dir := t.TempDir()
	certFile = filepath.Join(dir, cn+".crt")
	keyFile = filepath.Join(dir, cn+".key")
	writeFile(t, certFile, certPEM)
	writeFile(t, keyFile, keyPEM)
	return certFile, keyFile
}

func (ca *testCA) writeCA(t *testing.T) string {
	t.Helper()
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	writeFile(t, caFile, ca.certPEM)
	return caFile
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
