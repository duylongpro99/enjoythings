variable "monitoring_namespace" {
  description = "Namespace for the whole observability stack."
  type        = string
  default     = "monitoring"
}

variable "kube_prometheus_stack_chart_version" {
  description = "kube-prometheus-stack chart version from https://prometheus-community.github.io/helm-charts."
  type        = string
  default     = "90.0.0"
}

variable "loki_chart_version" {
  description = "loki chart version from https://grafana.github.io/helm-charts."
  type        = string
  default     = "7.3.0"
}

variable "alloy_chart_version" {
  description = "alloy chart version from https://grafana.github.io/helm-charts."
  type        = string
  default     = "1.12.1"
}

variable "tempo_chart_version" {
  description = "tempo chart version from https://grafana.github.io/helm-charts."
  type        = string
  default     = "1.24.4"
}

variable "otel_collector_chart_version" {
  description = "opentelemetry-collector chart version from https://open-telemetry.github.io/opentelemetry-helm-charts."
  type        = string
  default     = "0.172.1"
}

variable "scrape_mode" {
  description = "How Prometheus finds the EnjoyThings pods: \"podmonitor\" (apply podmonitor-enjoythings.yaml with kubectl after this stage) or \"annotations\" (adds the additionalScrapeConfigs overlay to the kube-prometheus-stack release)."
  type        = string
  default     = "podmonitor"

  validation {
    condition     = contains(["podmonitor", "annotations"], var.scrape_mode)
    error_message = "scrape_mode must be \"podmonitor\" or \"annotations\"."
  }
}
