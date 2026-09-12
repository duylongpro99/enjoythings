#!/usr/bin/env bash
# Steady traffic against the gateway so a canary analysis has data to judge.
#
#   services/k8s/rollouts/traffic.sh                 # 10 req/s to /readyz via the NodePort
#   services/k8s/rollouts/traffic.sh URL RPS         # override target and rate
#
# Every five seconds it prints how many responses were 2xx and how many were
# not, with the last non-2xx status code. Stop with Ctrl-C. Works with the
# bash 3.2 that ships with macOS; only curl is needed.
set -u

URL="${1:-http://localhost:18080/readyz}"
RPS="${2:-10}"
SLEEP_FOR=$(awk -v r="$RPS" 'BEGIN { printf "%.3f", 1 / r }')

ok=0
bad=0
last_bad=""
window_start=$(date +%s)

echo "traffic: $URL at about $RPS requests per second (Ctrl-C to stop)"
while true; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$URL" || echo "000")
  case "$code" in
    2*) ok=$((ok + 1)) ;;
    *)  bad=$((bad + 1)); last_bad="$code" ;;
  esac

  now=$(date +%s)
  if [ $((now - window_start)) -ge 5 ]; then
    summary="$(date '+%H:%M:%S')  2xx=$ok  non-2xx=$bad"
    [ -n "$last_bad" ] && summary="$summary (last: $last_bad)"
    echo "$summary"
    ok=0
    bad=0
    last_bad=""
    window_start=$now
  fi
  sleep "$SLEEP_FOR"
done
