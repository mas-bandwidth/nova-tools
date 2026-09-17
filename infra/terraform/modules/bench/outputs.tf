output "name" {
  description = "The bench name."
  value       = var.name
}

output "managed_files" {
  description = "The managed files of this bench, keyed by resource label."
  value       = [for k in sort(keys(local.declared)) : k]
}

output "witness" {
  description = "The read-each-plan witness command for one managed file; the null_resource trigger is not the witness."
  value       = "ssh ${var.ssh_user}@${var.address} sha256sum <path>"
}
