data "terraform_remote_state" "cluster" {
  backend = "local"
  config = {
    path = "${path.module}/../10-cluster/terraform.tfstate"
  }
}

# Not used for credentials, but reading it makes the dependency on stage 20
# explicit: this stage cannot plan until Argo CD's CRDs exist.
data "terraform_remote_state" "platform" {
  backend = "local"
  config = {
    path = "${path.module}/../20-platform/terraform.tfstate"
  }
}

locals {
  cluster = data.terraform_remote_state.cluster.outputs
}

provider "kubernetes" {
  host                   = local.cluster.endpoint
  cluster_ca_certificate = local.cluster.cluster_ca_certificate
  client_certificate     = local.cluster.client_certificate
  client_key             = local.cluster.client_key
}
