output "application_name" {
  value = kubernetes_manifest.enjoythings.manifest.metadata.name
}

output "target_revision" {
  value = kubernetes_manifest.enjoythings.manifest.spec.source.targetRevision
}
