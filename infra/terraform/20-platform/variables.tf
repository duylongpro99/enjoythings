variable "argocd_chart_version" {
  description = "argo-cd Helm chart version from https://argoproj.github.io/argo-helm."
  type        = string
  default     = "10.8.1"
}

variable "app_namespace" {
  description = "Namespace the EnjoyThings chart is deployed into."
  type        = string
  default     = "enjoythings"
}

variable "llm_api_key" {
  description = "API key the fraud worker sends to the LLM provider. Ollama ignores it; a hosted provider needs the real key. Set it in terraform.tfvars, never in this file."
  type        = string
  sensitive   = true
  default     = "local-development-key"
}
