output "bench_managed_files" {
  description = "The managed files of every bench, keyed by bench, so a plan's witness covers the same set the module declares."
  value       = { for name, m in module.bench : name => m.managed_files }
}

output "bench_witnesses" {
  description = "The read-each-plan witness command of every bench; a null_resource trigger is not the witness."
  value       = { for name, m in module.bench : name => m.witness }
}
