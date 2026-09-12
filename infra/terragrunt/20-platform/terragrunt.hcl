# Stage 20: Argo CD, the application namespace, the generated Secret.

include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../terraform/20-platform"
}

# The dependency block does two things. It orders this unit after 10-cluster in
# every `run --all` command, forwards on apply and reversed on destroy. And it
# exposes stage 10's outputs as dependency.cluster.outputs.<name>, read by
# running `terraform output -json` in the 10-cluster unit.
#
# The stage as written does not consume those outputs. It reads stage 10's
# state through a terraform_remote_state data source instead, so the outputs
# are not referenced here. Section 5 of docs/provisioning-tooling-runbook.md
# shows the edit that replaces the data source with inputs from this block.
dependency "cluster" {
  config_path = "../10-cluster"

  # Used only when stage 10 has no state yet, so that `run --all validate` and
  # `run --all plan` work on a fresh checkout. Never used by apply.
  mock_outputs = {
    cluster_name    = "enjoythings"
    kubectl_context = "kind-enjoythings"
    endpoint        = "https://127.0.0.1:6443"
    gateway_url     = "http://localhost:18080"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan"]
}

# The stage locates stage 10's state with a path relative to its own directory:
# ${path.module}/../10-cluster/terraform.tfstate. Inside the .terragrunt-cache
# copy that path does not exist. Terraform override files merge into existing
# blocks, so this one replaces only the `config` of that data source with the
# absolute path of the real state file. The file is written into the cache
# copy only; infra/terraform is never modified.
generate "absolute_paths" {
  path      = "terragrunt_override.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    data "terraform_remote_state" "cluster" {
      config = {
        path = "${get_repo_root()}/infra/terraform/10-cluster/terraform.tfstate"
      }
    }
  EOF
}
