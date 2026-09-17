# The fleet, as declared state (SPEC-FLEET-KUBE, Part 1).
#
# One root module, one module per bench role. `benches.auto.tfvars` names each
# bench with its role; `module "bench"` is instantiated once per entry, and this
# card instantiates it for `space`. The writes run over the SSH a null_resource
# carries, but the witnesses are the data sources that read the host on every
# plan: a null_resource trigger hashes the declaration and never the bytes on the
# host, so it cannot see a hand edit.
#
# Nothing secret is ever in state: the seat public key below is public by
# definition, and no secret value is an input, an output or a local_file.

module "bench" {
  source   = "./modules/bench"
  for_each = var.benches

  name             = each.key
  role             = each.value.role
  address          = each.value.address
  mac              = each.value.mac
  ssh_user         = var.ssh_user
  k3s_release      = var.k3s_release
  packages         = var.packages
  users            = var.users
  seat_public_keys = var.seat_public_keys
}
