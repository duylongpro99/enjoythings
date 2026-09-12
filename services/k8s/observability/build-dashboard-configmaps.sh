#!/usr/bin/env sh
# Build one ConfigMap per Grafana dashboard JSON in
# services/observability/grafana/dashboards and print them as YAML.
#
# The Grafana sidecar in kube-prometheus-stack watches ConfigMaps labelled
# grafana_dashboard=1 and loads their contents. The grafana_folder annotation
# puts them in the same "EnjoyThings" folder the Compose provisioning uses.
# The JSON files are not modified, so the dashboards appear exactly as they do
# under Docker Compose.
#
# Usage:
#   services/k8s/observability/build-dashboard-configmaps.sh | kubectl apply -f -
#   services/k8s/observability/build-dashboard-configmaps.sh > dashboards.generated.yaml
#
# Needs kubectl only; --dry-run=client never contacts a cluster.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
dashboards="${DASHBOARDS_DIR:-$here/../../observability/grafana/dashboards}"
namespace="${MONITORING_NAMESPACE:-monitoring}"
folder="${GRAFANA_FOLDER:-EnjoyThings}"

found=0
for file in "$dashboards"/*.json; do
  [ -f "$file" ] || continue
  found=1
  name="grafana-dashboard-$(basename "$file" .json)"
  kubectl create configmap "$name" \
    --namespace "$namespace" \
    --from-file="$file" \
    --dry-run=client -o yaml |
    kubectl label --local -f - --dry-run=client -o yaml grafana_dashboard=1 |
    kubectl annotate --local -f - --dry-run=client -o yaml "grafana_folder=$folder"
  echo "---"
done

if [ "$found" -eq 0 ]; then
  echo "no dashboard JSON files found in $dashboards" >&2
  exit 1
fi
