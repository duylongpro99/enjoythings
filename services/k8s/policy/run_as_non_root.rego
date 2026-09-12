# Containers should run as a non-root user.
#
# Why: a container that runs as root and escapes its runtime is root on the
# node. `runAsNonRoot: true` makes the kubelet refuse to start a container
# whose image would run as uid 0, which catches the mistake at deploy time.
#
# This is a `warn`, not a `deny`, because the chart sets no securityContext yet
# and every one of its containers would fail. conftest prints warnings and
# still exits 0, so CI stays green while the gap is visible in every run.
# Promote it to `deny` once the chart sets runAsNonRoot on its pods; the
# practice section of docs/manifest-tooling-runbook.md walks through that.
package main

import rego.v1

pod_runs_as_non_root if pod_spec.securityContext.runAsNonRoot == true

container_runs_as_non_root(c) if c.securityContext.runAsNonRoot == true

warn contains msg if {
	is_workload
	some c in containers
	not pod_runs_as_non_root
	not container_runs_as_non_root(c)
	msg := sprintf("%s: container %q does not set securityContext.runAsNonRoot: true", [workload_id, c.name])
}
