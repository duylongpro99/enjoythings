# Stage 10: the cluster.
#
# Stands in for the VPC + EKS (or GKE, AKS) layer of a cloud deployment. The
# kind provider drives the same library the `kind` CLI uses, so this creates the
# identical cluster to `kind create cluster --config services/k8s/kind/cluster.yaml`,
# but now the cluster is described in code and tracked in Terraform state.
resource "kind_cluster" "this" {
  name           = var.cluster_name
  node_image     = var.node_image
  wait_for_ready = true

  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"

    node {
      role = "control-plane"

      # The gateway Service is a NodePort on gateway_node_port. Publishing it
      # on the node container makes the API reachable at localhost:<host_port>.
      extra_port_mappings {
        container_port = var.gateway_node_port
        host_port      = var.gateway_host_port
        protocol       = "TCP"
      }
    }
  }
}
