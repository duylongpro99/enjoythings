# Everything a later stage needs to talk to the cluster. On a cloud provider the
# EKS/GKE module exposes the same three things: an endpoint, a CA certificate,
# and a way to authenticate.
output "cluster_name" {
  value = kind_cluster.this.name
}

output "kubectl_context" {
  description = "Context name written into your kubeconfig."
  value       = "kind-${kind_cluster.this.name}"
}

output "endpoint" {
  value = kind_cluster.this.endpoint
}

output "cluster_ca_certificate" {
  value     = kind_cluster.this.cluster_ca_certificate
  sensitive = true
}

output "client_certificate" {
  value     = kind_cluster.this.client_certificate
  sensitive = true
}

output "client_key" {
  value     = kind_cluster.this.client_key
  sensitive = true
}

output "gateway_url" {
  value = "http://localhost:${var.gateway_host_port}"
}
