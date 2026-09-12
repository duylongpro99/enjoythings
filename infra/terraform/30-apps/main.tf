# Stage 30: applications.
#
# Registers the EnjoyThings Application with Argo CD. From here on Argo CD, not
# Terraform, owns the workloads: Terraform says "this repo, this path, this
# branch", and Argo CD keeps the cluster equal to it.

locals {
  # Single source of truth: the same manifest the Argo CD runbook applies by
  # hand. Terraform only overrides the branch when asked.
  application_file = yamldecode(file("${path.module}/../../../services/k8s/argocd/application.yaml"))

  application = merge(local.application_file, {
    metadata = merge(local.application_file.metadata, {
      namespace = data.terraform_remote_state.platform.outputs.argocd_namespace
    })
    spec = merge(local.application_file.spec, {
      source = merge(local.application_file.spec.source, {
        targetRevision = coalesce(var.target_revision, local.application_file.spec.source.targetRevision)
      })
      destination = merge(local.application_file.spec.destination, {
        namespace = data.terraform_remote_state.platform.outputs.app_namespace
      })
    })
  })
}

resource "kubernetes_manifest" "enjoythings" {
  manifest = local.application

  # Argo CD's controller adds fields (status, some annotations) that Terraform
  # must not try to remove on the next plan.
  computed_fields = ["metadata.annotations", "metadata.labels", "metadata.finalizers"]
}
