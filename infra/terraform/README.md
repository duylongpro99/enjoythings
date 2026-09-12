# Terraform

Infrastructure as code for the local Kubernetes environment, in three stages
that are applied in order and destroyed in reverse:

| Stage | Creates | Cloud equivalent |
| --- | --- | --- |
| `10-cluster` | kind cluster with the gateway port mapping | VPC + EKS/GKE/AKS |
| `20-platform` | Argo CD, application namespace, generated credentials Secret | Cluster add-ons + secret manager |
| `30-apps` | Argo CD `Application` for the EnjoyThings chart | Same |

Step-by-step instructions with explanations: [`docs/terraform-runbook.md`](../../docs/terraform-runbook.md).
