# Postmortem: <short title of what broke>

Blameless. The goal is to learn what the system and the process let happen,
not who typed the command. Write it within a day of the incident while the
details are fresh. Keep it to two pages.

| | |
| --- | --- |
| Date | YYYY-MM-DD |
| Author | |
| Severity | SEV1 (users cannot pay) / SEV2 (degraded) / SEV3 (internal only) |
| Duration | From first user impact to full recovery, in minutes |
| Detected by | Alert name, or "a person noticed" |
| Status | Draft / Reviewed / Actions complete |

## Summary

Three sentences. What broke, what users saw, how it was fixed.

## Impact

- Which requests failed, and roughly how many. Quote the Prometheus query you
  used to count them.
- Which data was affected, if any.
- Who noticed first and how.

## Timeline

All times in one time zone. One line per event. Include the moments you were
wrong as well as the moments you were right; the wrong turns are where the
lessons are.

| Time | What happened | Evidence |
| --- | --- | --- |
| 10:00 | Change deployed: `kubectl scale deployment/postgres --replicas=0` | Terminal history |
| 10:02 | `EnjoyThingsDeploymentUnavailable` fires for wallet, ledger, saga-orchestrator, verification | Alertmanager, webhook log |
| 10:03 | System Overview dashboard: gateway request rate drops to zero | Grafana panel "Requests per second" |
| 10:05 | saga-orchestrator logs show `saga consumer record failed ... connection refused` | LogQL `{app="saga-orchestrator"} \|= "failed"` |
| 10:07 | Trace of the failed transfer ends at saga-orchestrator with a database error span | Tempo |
| 10:08 | Root cause identified: postgres has 0 replicas | `kubectl get deploy -n enjoythings` |
| 10:09 | Fix applied: `kubectl scale deployment/postgres --replicas=1` | |
| 10:11 | All Deployments ready, alerts resolve | Alertmanager |

## Root cause

One paragraph. The technical reason, stated so that someone who was not there
understands it. "Postgres was scaled to zero" is the trigger. "Every Go service
depends on one single-replica Postgres with no persistent volume and readiness
probes that fail when it is gone, so one Deployment taking a nap takes the
whole platform down" is the root cause.

## Contributing factors

Things that did not cause the incident but made it worse or longer.

- The alert said which services were unavailable, not why.
- The gateway also went unready, so the API returned connection refused rather
  than a useful error.
- No dashboard panel shows database availability directly.

## What went well

- Detection came from an alert, within two minutes.
- The alert to dashboard to logs to trace path led to the cause in under ten
  minutes.

## What went badly

- ...

## Where we got lucky

- It was a learning cluster with no real users.

## Action items

Every item has an owner and a date. Prefer items that remove a class of
failure over items that add a runbook step for this exact failure.

| Action | Type | Owner | Due | Status |
| --- | --- | --- | --- | --- |
| Add a `pg_isready`-based alert or a Postgres exporter so the database itself alerts | Detect | | | |
| Give postgres a PersistentVolumeClaim | Prevent | | | |
| Add a structured log line with saga id and outcome to saga-orchestrator so LogQL can find one saga | Diagnose | | | |
| Link the DeploymentUnavailable alert annotation to the dashboard URL | Diagnose | | | |

## Lessons

Two or three sentences a future engineer should read before touching this
system. If you cannot write them, the postmortem is not finished.
