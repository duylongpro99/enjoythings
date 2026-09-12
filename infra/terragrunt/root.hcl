# Root Terragrunt configuration, shared by every stage under this directory.
#
# Each stage's terragrunt.hcl includes this file. The file is named root.hcl,
# not terragrunt.hcl, on purpose: `terragrunt run --all` treats every directory
# that holds a terragrunt.hcl as a unit to run, and the root is not a unit.
# Terragrunt's current docs recommend this name and warn on the old one.

locals {
  # The existing Terraform stage this unit wraps: infra/terraform/<stage>.
  # path_relative_to_include() is the child directory name, for example
  # "10-cluster", so the Terragrunt tree mirrors the Terraform tree by name.
  stage_dir = "${get_repo_root()}/infra/terraform/${path_relative_to_include()}"
}

# Terragrunt copies the stage source into .terragrunt-cache and runs Terraform
# in that copy. Without this block Terraform would write terraform.tfstate into
# the copy, and the stages read each other's state by file path. Generating a
# local backend that points at the original stage directory keeps the state
# exactly where a plain `terraform apply` puts it, so hand-run Terraform and
# Terragrunt see the same state and stages 20 and 30 keep working unchanged.
#
# On a real cloud this block becomes backend = "s3" with a bucket, a key built
# from path_relative_to_include(), encryption and locking, and nothing else in
# the tree changes.
remote_state {
  backend = "local"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    path = "${local.stage_dir}/terraform.tfstate"
  }
}

terraform {
  # Every unit runs Terraform, never OpenTofu, and never asks questions.
  extra_arguments "no_input" {
    commands  = get_terraform_commands_that_need_input()
    arguments = ["-input=false"]
  }
}
