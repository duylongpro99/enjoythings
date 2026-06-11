#!/bin/sh
set -eu

namespace="${KUBE_NAMESPACE:-enjoythings}"
gateway_ready_url="${GATEWAY_READY_URL:-http://localhost:18080/readyz}"
wallet_probe_url="${WALLET_PROBE_URL:-}"
gateway_token="${GATEWAY_TOKEN:-}"
rollout_timeout="${ROLLOUT_TIMEOUT:-5m}"

if [ -z "$wallet_probe_url" ]; then
  echo "wallet rollout test: WALLET_PROBE_URL is required" >&2
  exit 1
fi
if [ -z "$gateway_token" ]; then
  echo "wallet rollout test: GATEWAY_TOKEN is required" >&2
  exit 1
fi

probe_log="$(mktemp)"
probe_failures="$(mktemp)"
probe_pid=""

cleanup() {
  if [ -n "$probe_pid" ]; then
    kill "$probe_pid" 2>/dev/null || true
    wait "$probe_pid" 2>/dev/null || true
  fi
  rm -f "$probe_log" "$probe_failures"
}
trap cleanup EXIT INT TERM

kubectl scale deployment/wallet --replicas=2 -n "$namespace"
kubectl rollout status deployment/wallet -n "$namespace" --timeout="$rollout_timeout"

(
  while :; do
    if ! curl -fsS --max-time 2 "$gateway_ready_url" >>"$probe_log" 2>&1; then
      printf "%s gateway readiness boundary\n" "$(date -u "+%Y-%m-%dT%H:%M:%SZ")" >>"$probe_failures"
    fi
    if ! curl -fsS --max-time 2 -H "Authorization: Bearer $gateway_token" "$wallet_probe_url" >>"$probe_log" 2>&1; then
      printf "%s wallet request boundary\n" "$(date -u "+%Y-%m-%dT%H:%M:%SZ")" >>"$probe_failures"
    fi
    sleep 0.2
  done
) &
probe_pid=$!

sleep 1
kubectl rollout restart deployment/wallet -n "$namespace"
kubectl rollout status deployment/wallet -n "$namespace" --timeout="$rollout_timeout"
sleep 2

kill "$probe_pid" 2>/dev/null || true
wait "$probe_pid" 2>/dev/null || true
probe_pid=""

if [ -s "$probe_failures" ]; then
  echo "wallet rollout test: readiness or wallet requests failed during rollout" >&2
  cat "$probe_failures" >&2
  cat "$probe_log" >&2
  exit 1
fi

ready_replicas="$(kubectl get deployment/wallet -n "$namespace" -o jsonpath='{.status.readyReplicas}')"
if [ "${ready_replicas:-0}" -lt 2 ]; then
  echo "wallet rollout test: ready replicas=${ready_replicas:-0}, want at least 2" >&2
  exit 1
fi

echo "wallet rollout test: ok"
