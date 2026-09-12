# Rules for Argo CD Application manifests (the Kustomize overlays and the app
# of apps render these, not the chart).
#
# Why: without the resources finalizer, deleting an Application orphans every
# workload it deployed (Argo CD runbook section 6). And an Application that
# deploys into `default` is almost always a forgotten destination.
package main

import rego.v1

is_argocd_application if {
	input.apiVersion == "argoproj.io/v1alpha1"
	input.kind == "Application"
}

argocd_finalizer := "resources-finalizer.argocd.argoproj.io"

deny contains msg if {
	is_argocd_application
	not argocd_finalizer in object.get(input.metadata, "finalizers", [])
	msg := sprintf("Application/%s: add the %s finalizer so deleting the Application also deletes what it deployed", [input.metadata.name, argocd_finalizer])
}

deny contains msg if {
	is_argocd_application
	input.spec.destination.namespace == "default"
	msg := sprintf("Application/%s: destination.namespace must not be `default`", [input.metadata.name])
}

deny contains msg if {
	is_argocd_application
	not input.spec.project
	msg := sprintf("Application/%s: spec.project is required", [input.metadata.name])
}
