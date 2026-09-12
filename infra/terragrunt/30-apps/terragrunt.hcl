# Stage 30: the Argo CD Application. Last in the graph.

include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../terraform/30-apps"
}

# Two dependencies, matching the two terraform_remote_state data sources in
# the stage. Both order this unit last; neither is consumed by the stage today.
dependency "cluster" {
  config_path = "../10-cluster"

  mock_outputs = {
    cluster_name    = "enjoythings"
    kubectl_context = "kind-enjoythings"
    endpoint        = "https://127.0.0.1:6443"
    gateway_url     = "http://localhost:18080"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan"]
}

dependency "platform" {
  config_path = "../20-platform"

  mock_outputs = {
    argocd_namespace = "argocd"
    app_namespace    = "enjoythings"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan"]
}

# Same override as stage 20, for both data sources, plus one more: the stage
# reads services/k8s/argocd/application.yaml relative to path.module, which
# also does not exist in the cache copy. Override files merge locals value by
# value, so only application_file is replaced; the rest of the locals block in
# main.tf is untouched.
generate "absolute_paths" {
  path      = "terragrunt_override.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    data "terraform_remote_state" "cluster" {
      config = {
        path = "${get_repo_root()}/infra/terraform/10-cluster/terraform.tfstate"
      }
    }

    data "terraform_remote_state" "platform" {
      config = {
        path = "${get_repo_root()}/infra/terraform/20-platform/terraform.tfstate"
      }
    }

    locals {
      application_file = yamldecode(file("${get_repo_root()}/services/k8s/argocd/application.yaml"))
    }
  EOF
}
