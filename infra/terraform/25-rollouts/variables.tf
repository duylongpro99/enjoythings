variable "argo_rollouts_chart_version" {
  description = "argo-rollouts Helm chart version from https://argoproj.github.io/argo-helm. 2.43.0 ships Argo Rollouts v1.10.0."
  type        = string
  default     = "2.43.0"
}

variable "namespace" {
  description = "Namespace the Argo Rollouts controller is installed into."
  type        = string
  default     = "argo-rollouts"
}

variable "controller_replicas" {
  description = "Controller pods. The chart defaults to 2; a single-node kind cluster needs 1."
  type        = number
  default     = 1
}

variable "dashboard_enabled" {
  description = "Deploy the in-cluster dashboard Deployment and Service (port 3100). Off because the kubectl plugin serves the same UI locally."
  type        = bool
  default     = false
}
