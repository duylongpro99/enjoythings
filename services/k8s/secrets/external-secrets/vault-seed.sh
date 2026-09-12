#!/usr/bin/env sh
# Write the source values for enjoythings-secret into Vault KV v2 at
# secret/enjoythings (docs/secrets-management-runbook.md section 7).
#
# Usage:
#   ./vault-seed.sh                  # random passwords, LLM key "local-development-key"
#   LLM_API_KEY=sk-... ./vault-seed.sh
#
# Only four values live in Vault. The two connection URLs are built by the
# ExternalSecret template from the passwords, so a password can never disagree
# with the URL that contains it. Rotating a password in Vault rotates the URL.
#
# Dev mode only: the root token is passed on the command line of a process
# inside the vault pod, and the values are visible in that pod's process list
# for the moment the command runs. Fine for a laptop, never for production.
set -eu

VAULT_NAMESPACE=${VAULT_NAMESPACE:-vault}
VAULT_POD=${VAULT_POD:-vault-0}
VAULT_ROOT_TOKEN=${VAULT_ROOT_TOKEN:-root}

JWT_SECRET=${JWT_SECRET:-$(openssl rand -hex 32)}
PG_PASSWORD=${PG_PASSWORD:-$(openssl rand -hex 16)}
FRAUD_PASSWORD=${FRAUD_PASSWORD:-$(openssl rand -hex 16)}
LLM_API_KEY=${LLM_API_KEY:-local-development-key}

# `vault kv put -mount=secret <path>` writes one KV v2 version holding all four
# fields. `put` replaces the whole document; use `vault kv patch` to change one
# field later, as the rotation exercise does.
kubectl exec -n "$VAULT_NAMESPACE" "$VAULT_POD" -- sh -c "
  VAULT_TOKEN='$VAULT_ROOT_TOKEN' vault kv put -mount=secret enjoythings \
    JWT_SECRET='$JWT_SECRET' \
    POSTGRES_PASSWORD='$PG_PASSWORD' \
    FRAUD_POSTGRES_PASSWORD='$FRAUD_PASSWORD' \
    LOCAL_LLM_API_KEY='$LLM_API_KEY'
"

echo "JWT_SECRET (keep it, cmd/devtoken needs it): $JWT_SECRET"
