output "monitoring_namespace" {
  value = kubernetes_namespace_v1.monitoring.metadata[0].name
}

output "grafana_port_forward_command" {
  description = "Run this, then open http://localhost:3000 (admin / the adminPassword in kube-prometheus-stack-values.yaml)."
  value       = "kubectl -n ${kubernetes_namespace_v1.monitoring.metadata[0].name} port-forward svc/monitoring-grafana 3000:80"
}

output "prometheus_port_forward_command" {
  value = "kubectl -n ${kubernetes_namespace_v1.monitoring.metadata[0].name} port-forward svc/monitoring-kube-prometheus-prometheus 9090:9090"
}

output "alertmanager_port_forward_command" {
  value = "kubectl -n ${kubernetes_namespace_v1.monitoring.metadata[0].name} port-forward svc/monitoring-kube-prometheus-alertmanager 9093:9093"
}

output "otel_exporter_otlp_endpoint" {
  description = "Value for OTEL_EXPORTER_OTLP_ENDPOINT in the EnjoyThings chart (see services/k8s/observability/otel-endpoint-values.example.yaml)."
  value       = "http://otel-collector.${kubernetes_namespace_v1.monitoring.metadata[0].name}.svc.cluster.local:4318"
}

output "post_apply_commands" {
  description = "Custom resources that need the CRDs from this stage to exist first."
  value = [
    "kubectl apply -f services/k8s/observability/podmonitor-enjoythings.yaml",
    "kubectl apply -f services/k8s/observability/prometheusrule-enjoythings.yaml",
  ]
}
