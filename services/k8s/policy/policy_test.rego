# Unit tests for the policies, run with `conftest verify -p services/k8s/policy`.
# Each test builds a minimal manifest, evaluates a rule against it with
# `with input as ...`, and asserts on the result. Testing policy like code is
# what stops a well-meant tweak from silently allowing everything.
package main

import rego.v1

good_container := {
	"name": "wallet",
	"image": "enjoythings/wallet:abc123",
	"readinessProbe": {"httpGet": {"path": "/readyz", "port": "http"}},
	"resources": {
		"requests": {"cpu": "50m", "memory": "64Mi"},
		"limits": {"cpu": "250m", "memory": "256Mi"},
	},
	"securityContext": {"runAsNonRoot": true},
}

deployment(c) := {
	"apiVersion": "apps/v1",
	"kind": "Deployment",
	"metadata": {"name": c.name},
	"spec": {"template": {"spec": {"containers": [c]}}},
}

test_good_deployment_has_no_findings if {
	count(deny) == 0 with input as deployment(good_container)
	count(warn) == 0 with input as deployment(good_container)
}

test_latest_tag_denied if {
	c := object.union(good_container, {"image": "enjoythings/wallet:latest"})
	count(deny) == 1 with input as deployment(c)
}

test_missing_tag_denied if {
	c := object.union(good_container, {"image": "enjoythings/wallet"})
	count(deny) == 1 with input as deployment(c)
}

test_registry_port_is_not_a_tag if {
	c := object.union(good_container, {"image": "registry.local:5000/enjoythings/wallet"})
	count(deny) == 1 with input as deployment(c)
}

test_digest_is_accepted if {
	c := object.union(good_container, {"image": "enjoythings/wallet@sha256:0000000000000000000000000000000000000000000000000000000000000000"})
	count(deny) == 0 with input as deployment(c)
}

test_missing_limits_denied if {
	# object.union merges recursively, so drop resources first or limits stay.
	c := object.union(object.remove(good_container, ["resources"]), {"resources": {"requests": {"cpu": "50m", "memory": "64Mi"}}})
	msgs := deny with input as deployment(c)
	count(msgs) == 1
	some msg in msgs
	contains(msg, "resources.limits")
}

test_resource_exception_is_honoured if {
	c := object.union(good_container, {"name": "kafka", "resources": null})
	count(deny) == 0 with input as deployment(c)
}

test_missing_readiness_probe_denied if {
	c := object.remove(good_container, ["readinessProbe"])
	count(deny) == 1 with input as deployment(c)
}

test_job_without_readiness_probe_is_fine if {
	c := object.remove(good_container, ["readinessProbe"])
	job := {
		"apiVersion": "batch/v1",
		"kind": "Job",
		"metadata": {"name": "topics"},
		"spec": {"template": {"spec": {"containers": [c]}}},
	}
	count(deny) == 0 with input as job
}

test_root_container_warns_not_denies if {
	c := object.remove(good_container, ["securityContext"])
	count(warn) == 1 with input as deployment(c)
	count(deny) == 0 with input as deployment(c)
}

test_pod_level_run_as_non_root_satisfies_warning if {
	c := object.remove(good_container, ["securityContext"])
	d := deployment(c)
	d2 := object.union(d, {"spec": {"template": {"spec": {"securityContext": {"runAsNonRoot": true}, "containers": [c]}}}})
	count(warn) == 0 with input as d2
}

application := {
	"apiVersion": "argoproj.io/v1alpha1",
	"kind": "Application",
	"metadata": {"name": "enjoythings", "finalizers": ["resources-finalizer.argocd.argoproj.io"]},
	"spec": {"project": "default", "destination": {"namespace": "enjoythings"}},
}

test_good_application_passes if {
	count(deny) == 0 with input as application
}

test_application_without_finalizer_denied if {
	a := object.union(application, {"metadata": {"name": "enjoythings", "finalizers": []}})
	count(deny) == 1 with input as a
}

test_application_into_default_namespace_denied if {
	a := object.union(application, {"spec": {"project": "default", "destination": {"namespace": "default"}}})
	count(deny) == 1 with input as a
}
