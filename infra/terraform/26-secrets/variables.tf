variable "sealed_secrets_chart_version" {
  description = "sealed-secrets Helm chart version from https://bitnami.github.io/sealed-secrets. Chart 2.19.3 ships controller 0.39.1."
  type        = string
  default     = "2.19.3"
}

variable "external_secrets_chart_version" {
  description = "external-secrets Helm chart version from https://charts.external-secrets.io."
  type        = string
  default     = "2.10.0"
}

variable "vault_chart_version" {
  description = "vault Helm chart version from https://helm.releases.hashicorp.com. Chart 0.34.1 ships Vault 2.0.4."
  type        = string
  default     = "0.34.1"
}

variable "install_sealed_secrets" {
  description = "Install the Sealed Secrets controller into kube-system."
  type        = bool
  default     = true
}

variable "install_external_secrets" {
  description = "Install External Secrets Operator and the Vault dev server."
  type        = bool
  default     = true
}
