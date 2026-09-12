output "argocd_namespace" {
  value = helm_release.argocd.namespace
}

output "argocd_admin_password_command" {
  description = "Run this to read the generated Argo CD admin password."
  value       = "kubectl -n ${helm_release.argocd.namespace} get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d"
}

output "app_namespace" {
  value = kubernetes_namespace_v1.app.metadata[0].name
}

output "jwt_secret" {
  description = "Needed to mint a local token with cmd/devtoken. Read with: terraform output -raw jwt_secret"
  value       = random_password.jwt.result
  sensitive   = true
}

output "postgres_password" {
  description = "Needed by the phase3 smoke test's DATABASE_URL."
  value       = random_password.postgres.result
  sensitive   = true
}
