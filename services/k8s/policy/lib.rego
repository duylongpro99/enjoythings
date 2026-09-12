# Shared helpers for the EnjoyThings conftest policies. Every policy file in
# this directory is in package `main`, which is the package conftest evaluates
# by default, so the definitions here are visible to all of them.
package main

import rego.v1

# Kinds whose pod template lives at spec.template.spec.
workload_kinds := {"Deployment", "StatefulSet", "DaemonSet", "Job"}

# Kinds whose pod template is one level deeper.
cron_kinds := {"CronJob"}

is_workload if input.kind in workload_kinds

is_workload if input.kind in cron_kinds

pod_spec := input.spec.template.spec if input.kind in workload_kinds

pod_spec := input.spec.jobTemplate.spec.template.spec if input.kind in cron_kinds

# Every container of the pod, init containers included. Rego sets are
# unordered and deduplicated; that is fine because we only report names.
containers contains c if some c in pod_spec.containers

containers contains c if some c in pod_spec.initContainers

# "Deployment/wallet" for messages.
workload_id := sprintf("%s/%s", [input.kind, input.metadata.name])
