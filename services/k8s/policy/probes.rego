# Every Deployment container must define a readiness probe.
#
# Why: without one, Kubernetes sends traffic to a pod the moment its process
# starts, before it has connected to Postgres or Kafka, and a rolling update
# with maxUnavailable: 0 (applications.yaml) is only zero-downtime when
# readiness is honest. Jobs are exempt: they run to completion and receive no
# traffic.
package main

import rego.v1

deny contains msg if {
	input.kind == "Deployment"
	some c in containers
	not c.readinessProbe
	msg := sprintf("%s: container %q has no readinessProbe", [workload_id, c.name])
}
