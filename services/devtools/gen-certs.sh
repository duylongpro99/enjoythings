#!/usr/bin/env bash
#
# Generate a local development CA and per-service leaf certificates for the
# internal gRPC mTLS described in docs/design-notes/phase5-mtls.md.
#
# Development only. These certs are self-signed and short-lived; they must never
# leave a local stack. Nothing produced here is committed (see .gitignore).
# Re-run any time — an existing CA is reused so already-issued leaves stay valid.
#
# Usage: services/devtools/gen-certs.sh [output-dir]   (default: services/certs)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${1:-${SCRIPT_DIR}/../certs}"
DAYS="${CERT_DAYS:-825}"

# Services that terminate or originate internal gRPC and therefore need an
# identity. notification and payment-processor are Kafka-only and are omitted.
# Each name is also the service's Compose/Kubernetes DNS name, which is what the
# peer verifies against the certificate SANs.
SERVICES=(gateway wallet ledger verification saga-orchestrator fraud-worker)

mkdir -p "${OUT_DIR}"
cd "${OUT_DIR}"

if [[ ! -f ca.crt ]]; then
  echo "Generating development CA..."
  openssl genrsa -out ca.key 4096 >/dev/null 2>&1
  openssl req -x509 -new -nodes -key ca.key -sha256 -days "${DAYS}" \
    -subj "/CN=enjoythings-dev-ca" -out ca.crt >/dev/null 2>&1
fi

for svc in "${SERVICES[@]}"; do
  echo "Issuing certificate for ${svc}..."
  openssl genrsa -out "${svc}.key" 2048 >/dev/null 2>&1
  cat > "${svc}.ext" <<EOF
subjectAltName = DNS:${svc},DNS:localhost,IP:127.0.0.1
extendedKeyUsage = serverAuth,clientAuth
keyUsage = critical,digitalSignature,keyEncipherment
EOF
  openssl req -new -key "${svc}.key" -subj "/CN=${svc}" -out "${svc}.csr" >/dev/null 2>&1
  openssl x509 -req -in "${svc}.csr" -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out "${svc}.crt" -days "${DAYS}" -sha256 -extfile "${svc}.ext" >/dev/null 2>&1
  rm -f "${svc}.csr" "${svc}.ext"
done

chmod 600 ./*.key
echo "Certificates written to ${OUT_DIR}"
echo "Enable mTLS by setting GRPC_TLS_ENABLED=true and pointing the services at ca.crt plus each <service>.crt/.key."
