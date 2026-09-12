# Stage 27: observability.
#
# Metrics, logs, traces and alerting for the cluster, installed from the same
# values files the runbook applies by hand (services/k8s/observability). It sits
# between the platform (20) and the applications (30) because nothing here
# depends on the EnjoyThings release existing, and stage 30 does not depend on
# this stage either. Apply it any time after stage 10.
#
# Two things from the runbook are deliberately NOT here: the PodMonitor and the
# PrometheusRule. Both are custom resources whose CRDs are created by the
# kube-prometheus-stack release in this same stage, and the Kubernetes provider
# must know a resource's schema at plan time. Putting them here would fail on
# the first plan. Apply them with kubectl after this stage, or add a stage 28.

locals {
  manifests_dir  = "${path.module}/../../../services/k8s/observability"
  dashboards_dir = "${path.module}/../../../services/observability/grafana/dashboards"

  kube_prometheus_stack_values = concat(
    [file("${local.manifests_dir}/kube-prometheus-stack-values.yaml")],
    var.scrape_mode == "annotations" ? [file("${local.manifests_dir}/kube-prometheus-stack-values.annotations.yaml")] : [],
  )
}

resource "kubernetes_namespace_v1" "monitoring" {
  metadata {
    name = var.monitoring_namespace
  }
}

# Prometheus Operator, Prometheus, Alertmanager, Grafana, kube-state-metrics,
# node-exporter. Installs the monitoring.coreos.com CRDs as well.
resource "helm_release" "kube_prometheus_stack" {
  name       = "monitoring"
  repository = "https://prometheus-community.github.io/helm-charts"
  chart      = "kube-prometheus-stack"
  version    = var.kube_prometheus_stack_chart_version
  namespace  = kubernetes_namespace_v1.monitoring.metadata[0].name
  values     = local.kube_prometheus_stack_values
  wait       = true
  timeout    = 900
}

# One ConfigMap per dashboard JSON, exactly what build-dashboard-configmaps.sh
# prints. The Grafana sidecar picks them up by label.
resource "kubernetes_config_map_v1" "dashboards" {
  for_each = fileset(local.dashboards_dir, "*.json")

  metadata {
    name      = "grafana-dashboard-${trimsuffix(each.value, ".json")}"
    namespace = kubernetes_namespace_v1.monitoring.metadata[0].name
    labels = {
      grafana_dashboard = "1"
    }
    annotations = {
      grafana_folder = "EnjoyThings"
    }
  }

  data = {
    (each.value) = file("${local.dashboards_dir}/${each.value}")
  }
}

resource "helm_release" "loki" {
  name       = "loki"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "loki"
  version    = var.loki_chart_version
  namespace  = kubernetes_namespace_v1.monitoring.metadata[0].name
  values     = [file("${local.manifests_dir}/loki-values.yaml")]
  wait       = true
  timeout    = 600
}

resource "helm_release" "alloy" {
  name       = "alloy"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "alloy"
  version    = var.alloy_chart_version
  namespace  = kubernetes_namespace_v1.monitoring.metadata[0].name
  values     = [file("${local.manifests_dir}/alloy-values.yaml")]
  wait       = true
  timeout    = 600

  depends_on = [helm_release.loki]
}

resource "helm_release" "tempo" {
  name       = "tempo"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "tempo"
  version    = var.tempo_chart_version
  namespace  = kubernetes_namespace_v1.monitoring.metadata[0].name
  values     = [file("${local.manifests_dir}/tempo-values.yaml")]
  wait       = true
  timeout    = 600
}

resource "helm_release" "otel_collector" {
  name       = "otel-collector"
  repository = "https://open-telemetry.github.io/opentelemetry-helm-charts"
  chart      = "opentelemetry-collector"
  version    = var.otel_collector_chart_version
  namespace  = kubernetes_namespace_v1.monitoring.metadata[0].name
  values     = [file("${local.manifests_dir}/otel-collector-values.yaml")]
  wait       = true
  timeout    = 600

  depends_on = [helm_release.tempo]
}

# The Alertmanager webhook receiver: an echo server that logs every
# notification. Same image and ports as alert-webhook.yaml.
resource "kubernetes_deployment_v1" "alert_webhook" {
  metadata {
    name      = "alert-webhook"
    namespace = kubernetes_namespace_v1.monitoring.metadata[0].name
    labels = {
      "app.kubernetes.io/name"      = "alert-webhook"
      "app.kubernetes.io/component" = "observability"
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        "app.kubernetes.io/name" = "alert-webhook"
      }
    }

    template {
      metadata {
        labels = {
          "app.kubernetes.io/name"      = "alert-webhook"
          "app.kubernetes.io/component" = "observability"
        }
      }

      spec {
        container {
          name  = "echo"
          image = "mendhak/http-https-echo:41"

          env {
            name  = "HTTP_PORT"
            value = "8080"
          }
          env {
            name  = "LOG_WITHOUT_NEWLINE"
            value = "true"
          }

          port {
            name           = "http"
            container_port = 8080
          }

          readiness_probe {
            http_get {
              path = "/healthz"
              port = "http"
            }
            period_seconds = 5
          }

          resources {
            requests = {
              cpu    = "10m"
              memory = "32Mi"
            }
            limits = {
              cpu    = "100m"
              memory = "64Mi"
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_service_v1" "alert_webhook" {
  metadata {
    name      = "alert-webhook"
    namespace = kubernetes_namespace_v1.monitoring.metadata[0].name
    labels = {
      "app.kubernetes.io/name"      = "alert-webhook"
      "app.kubernetes.io/component" = "observability"
    }
  }

  spec {
    type = "ClusterIP"
    selector = {
      "app.kubernetes.io/name" = "alert-webhook"
    }
    port {
      name        = "http"
      port        = 8080
      target_port = "http"
    }
  }
}
