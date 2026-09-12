# Stage 20: the platform layer.
#
# Cluster add-ons and the things applications need to exist before they start.
# On a cloud this stage would also hold ingress controllers, cert-manager,
# metrics-server, and IAM bindings.

# Argo CD, installed from its Helm chart. Replaces the `kubectl apply -f
# install.yaml` of the Argo CD runbook with a pinned, upgradable release.
resource "helm_release" "argocd" {
  name             = "argocd"
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = var.argocd_chart_version
  namespace        = "argocd"
  create_namespace = true
  wait             = true
  timeout          = 600
}

# The application namespace. Argo CD could create it (CreateNamespace=true),
# but the Secret below has to be in it before the first sync.
resource "kubernetes_namespace_v1" "app" {
  metadata {
    name = var.app_namespace
  }
}

# Generated credentials. Stands in for a cloud secret manager: nobody types
# these, nobody commits them, and the same value feeds both the database
# container and the connection string so they can never disagree.
resource "random_password" "postgres" {
  length  = 32
  special = false
}

resource "random_password" "fraud_db" {
  length  = 32
  special = false
}

resource "random_password" "jwt" {
  length  = 64
  special = false
}

# The Secret the Helm chart expects when secrets.create=false. Same name and
# keys as services/charts/enjoythings/templates/secret.yaml.
resource "kubernetes_secret_v1" "app" {
  metadata {
    name      = "enjoythings-secret"
    namespace = kubernetes_namespace_v1.app.metadata[0].name
  }

  data = {
    JWT_SECRET              = random_password.jwt.result
    POSTGRES_PASSWORD       = random_password.postgres.result
    FRAUD_POSTGRES_PASSWORD = random_password.fraud_db.result
    DATABASE_URL            = "postgres://enjoythings:${random_password.postgres.result}@postgres:5432/enjoythings?sslmode=disable"
    FRAUD_DATABASE_URL      = "postgres://fraud_worker:${random_password.fraud_db.result}@fraud-timescaledb:5432/fraud_audit?sslmode=disable"
    LOCAL_LLM_API_KEY       = var.llm_api_key
  }
}
