#!/usr/bin/env sh
# Produce manifests/enjoythings-sealedsecret.yaml from the plain template.
#
# Usage:
#   ./seal.sh                       # random passwords, LLM key "local-development-key"
#   LLM_API_KEY=sk-... ./seal.sh    # any of the four inputs can be given
#   KUBESEAL_CERT=pub.pem ./seal.sh # seal offline with a fetched certificate
#
# Inputs (environment variables, all optional):
#   JWT_SECRET, PG_PASSWORD, FRAUD_PASSWORD, LLM_API_KEY, KUBESEAL_CERT
#
# The plain Secret exists only in a pipe. Nothing unencrypted touches disk, and
# the output file is safe to commit: only the controller's private key, which
# never leaves the cluster, can decrypt it.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
template="$here/enjoythings-secret.template.yaml"
out="$here/manifests/enjoythings-sealedsecret.yaml"

JWT_SECRET=${JWT_SECRET:-$(openssl rand -hex 32)}
PG_PASSWORD=${PG_PASSWORD:-$(openssl rand -hex 16)}
FRAUD_PASSWORD=${FRAUD_PASSWORD:-$(openssl rand -hex 16)}
LLM_API_KEY=${LLM_API_KEY:-local-development-key}

# --cert seals offline against a certificate fetched earlier with
# `kubeseal --fetch-cert`. Without it kubeseal asks the controller for the
# certificate through the Kubernetes API, so a working kubectl context is needed.
if [ -n "${KUBESEAL_CERT:-}" ]; then
  set -- --cert "$KUBESEAL_CERT"
else
  set --
fi

mkdir -p "$here/manifests"

sed \
  -e "s#REPLACE_ME_JWT_SECRET#$JWT_SECRET#g" \
  -e "s#REPLACE_ME_PG_PASSWORD#$PG_PASSWORD#g" \
  -e "s#REPLACE_ME_FRAUD_PASSWORD#$FRAUD_PASSWORD#g" \
  -e "s#REPLACE_ME_LLM_API_KEY#$LLM_API_KEY#g" \
  "$template" |
  kubeseal \
    --controller-name sealed-secrets-controller \
    --controller-namespace kube-system \
    --scope strict \
    --format yaml \
    "$@" > "$out"

echo "wrote $out"
echo "JWT_SECRET (keep it, cmd/devtoken needs it): $JWT_SECRET"
