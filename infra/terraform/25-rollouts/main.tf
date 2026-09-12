# Stage 25: progressive delivery.
#
# Installs the Argo Rollouts controller and its CRDs from the community Helm
# chart. Sits between 20-platform (Argo CD) and 30-apps because it is a cluster
# add-on, not an application, and because the Rollout manifests under
# services/k8s/rollouts need these CRDs to exist before they can be applied.
# It is optional: docs/progressive-delivery-runbook.md section 3 shows the
# equivalent `kubectl apply` for readers who are not using Terraform.
resource "helm_release" "argo_rollouts" {
  name             = "argo-rollouts"
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-rollouts"
  version          = var.argo_rollouts_chart_version
  namespace        = var.namespace
  create_namespace = true
  wait             = true
  timeout          = 600

  values = [
    yamlencode({
      # The chart installs and upgrades the CRDs by default (installCRDs: true).
      controller = {
        # The chart defaults to 2 for high availability. One is enough on kind.
        replicas = var.controller_replicas
      }
      dashboard = {
        # `kubectl argo rollouts dashboard` serves the same UI from your laptop,
        # so the in-cluster dashboard stays off unless asked for.
        enabled = var.dashboard_enabled
      }
    })
  ]
}
