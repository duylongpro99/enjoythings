output "sealed_secrets_controller" {
  description = "Controller name and namespace kubeseal must be pointed at."
  value       = var.install_sealed_secrets ? "sealed-secrets-controller in kube-system" : "not installed"
}

output "external_secrets_namespace" {
  value = var.install_external_secrets ? helm_release.external_secrets[0].namespace : "not installed"
}

output "vault_address" {
  description = "In-cluster address the SecretStore uses."
  value       = var.install_external_secrets ? "http://vault.${helm_release.vault[0].namespace}.svc.cluster.local:8200" : "not installed"
}

output "next_steps" {
  value = "Seed Vault with services/k8s/secrets/external-secrets/vault-seed.sh, then kubectl apply the SecretStore and ExternalSecret. See docs/secrets-management-runbook.md."
}
