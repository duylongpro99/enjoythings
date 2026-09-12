# Every container image must name an explicit tag or digest, and the tag must
# not be `latest`.
#
# Why: a mutable tag makes "what is running" unanswerable. `latest` today and
# `latest` tomorrow can be different images, and a rollback to `latest` is not
# a rollback. Argo CD also cannot notice a change behind an unchanged tag, so
# nothing syncs and nothing restarts (Argo CD runbook section 8c).
package main

import rego.v1

# The part of the reference after the last slash, so a registry port such as
# `registry:5000/app` is not mistaken for a tag.
image_last_segment(image) := parts[count(parts) - 1] if {
	parts := split(image, "/")
}

has_digest(image) if contains(image, "@sha256:")

has_tag(image) if {
	not has_digest(image)
	contains(image_last_segment(image), ":")
}

image_tag(image) := tag if {
	has_tag(image)
	segs := split(image_last_segment(image), ":")
	tag := segs[count(segs) - 1]
}

deny contains msg if {
	is_workload
	some c in containers
	not has_digest(c.image)
	not has_tag(c.image)
	msg := sprintf("%s: container %q uses image %q without a tag; pin a tag or digest", [workload_id, c.name, c.image])
}

deny contains msg if {
	is_workload
	some c in containers
	image_tag(c.image) == "latest"
	msg := sprintf("%s: container %q uses the `latest` tag on %q; pin an immutable tag", [workload_id, c.name, c.image])
}
