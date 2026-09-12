output "argo_rollouts_namespace" {
  value = helm_release.argo_rollouts.namespace
}

output "argo_rollouts_chart_version" {
  value = helm_release.argo_rollouts.version
}

output "verify_command" {
  description = "Run this to confirm the controller is up."
  value       = "kubectl get pods -n ${helm_release.argo_rollouts.namespace}"
}
