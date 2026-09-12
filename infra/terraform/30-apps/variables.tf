variable "target_revision" {
  description = "Git branch, tag or commit Argo CD deploys. null keeps the value in services/k8s/argocd/application.yaml."
  type        = string
  default     = null
}
