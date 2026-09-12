# Read the cluster credentials from stage 10's state instead of a kubeconfig
# file, exactly as 20-platform does. A provider must be fully configured before
# `terraform plan` runs, so it cannot depend on a resource created in the same
# apply; the cluster therefore lives in its own stage.
data "terraform_remote_state" "cluster" {
  backend = "local"
  config = {
    path = "${path.module}/../10-cluster/terraform.tfstate"
  }
}

locals {
  cluster = data.terraform_remote_state.cluster.outputs
}

provider "helm" {
  kubernetes = {
    host                   = local.cluster.endpoint
    cluster_ca_certificate = local.cluster.cluster_ca_certificate
    client_certificate     = local.cluster.client_certificate
    client_key             = local.cluster.client_key
  }
}
