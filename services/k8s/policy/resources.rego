# Every container must declare CPU and memory requests and limits.
#
# Why: without requests the scheduler packs pods blindly and one noisy
# container can starve the node. Without limits a memory leak takes the node
# down instead of one pod. Kubernetes runbook section 13 item 7 asks for the
# same thing.
#
# Exceptions: the containers named below do not declare resources in the chart
# today. Listing them here keeps the rule enforced for everything else and
# makes the gap visible in git. Remove a name once the chart fixes it; the
# policy then fails until the chart really does. Never add a name without a
# reason in the review.
package main

import rego.v1

resource_exceptions := {
	"kafka", # kafka.resources is not defined in values.yaml, so the template renders `resources: null`
	"fraud-timescaledb", # the template does not render fraudTimescaledb.resources at all
	"kafka-topic-init", # the hook Job has no resources block
}

required_resources := {"cpu", "memory"}

missing_resources(c) := missing if {
	missing := {kind |
		some kind in {"requests", "limits"}
		some res in required_resources
		not c.resources[kind][res]
	}
}

deny contains msg if {
	is_workload
	some c in containers
	not c.name in resource_exceptions
	missing := missing_resources(c)
	count(missing) > 0
	msg := sprintf("%s: container %q must set cpu and memory under resources.%s", [workload_id, c.name, concat(" and resources.", sort(missing))])
}
