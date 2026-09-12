# Stage 26: secrets tooling.
#
# Installs the three operators docs/secrets-management-runbook.md uses, each
# from its Helm chart with the same values files the runbook applies by hand.
# It installs tooling only. The SecretStore, ExternalSecret and SealedSecret
# are custom resources whose definitions do not exist until these releases are
# applied, so they cannot be planned in the same root module. Apply them with
# kubectl or Argo CD as the runbook shows, exactly as stage 30 is separate from
# stage 20 for the Argo CD Application.
#
# Ownership warning: stage 20 creates enjoythings-secret. Before letting Sealed
# Secrets or ESO produce that Secret, remove stage 20's copy with
#   terraform -chdir=../20-platform destroy -target=kubernetes_secret_v1.app
# or two controllers will fight over one object. Runbook section 3.

locals {
  secrets_dir = "${path.module}/../../../services/k8s/secrets"
}

# Sealed Secrets controller. kube-system is the namespace kubeseal assumes, and
# the values file renames the controller to what kubeseal assumes too.
resource "helm_release" "sealed_secrets" {
  count = var.install_sealed_secrets ? 1 : 0

  name       = "sealed-secrets"
  repository = "https://bitnami.github.io/sealed-secrets"
  chart      = "sealed-secrets"
  version    = var.sealed_secrets_chart_version
  namespace  = "kube-system"
  wait       = true
  timeout    = 300

  values = [file("${local.secrets_dir}/sealed-secrets/values.yaml")]
}

# External Secrets Operator, with its CRDs.
resource "helm_release" "external_secrets" {
  count = var.install_external_secrets ? 1 : 0

  name             = "external-secrets"
  repository       = "https://charts.external-secrets.io"
  chart            = "external-secrets"
  version          = var.external_secrets_chart_version
  namespace        = "external-secrets"
  create_namespace = true
  wait             = true
  timeout          = 300

  values = [file("${local.secrets_dir}/external-secrets/eso-values.yaml")]
}

# Vault in dev mode: the backend ESO reads from. In-memory, root token "root",
# wiped on every pod restart. A stand-in for a cloud secret manager.
resource "helm_release" "vault" {
  count = var.install_external_secrets ? 1 : 0

  name             = "vault"
  repository       = "https://helm.releases.hashicorp.com"
  chart            = "vault"
  version          = var.vault_chart_version
  namespace        = "vault"
  create_namespace = true
  wait             = true
  timeout          = 300

  values = [file("${local.secrets_dir}/external-secrets/vault-values.yaml")]
}
