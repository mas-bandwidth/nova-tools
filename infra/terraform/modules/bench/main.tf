# The bench module: what a Linux bench is, declared once and instantiated per
# bench (SPEC-FLEET-KUBE, Part 1). The resources are ordinary Terraform -- OS
# packages, the per-line unix users, the runner services, the hygiene and mirror
# timers, the shared cache, k3s and the secrets seat public keys -- and the
# module is additive to the hand-built fleet.
#
# Every managed file is paired with a read-each-plan witness. The null_resource
# carries the write; the data "external" reads the host on every plan and the
# resource's precondition compares the observed hash with the declared one, so a
# hand edit nobody declared is an attribute that differs and the plan is not
# empty. A null_resource trigger is never the witness: it hashes the
# declaration, not the bytes on the bench.

locals {
  # files.json is the one declaration of managed files: the HCL for_each below
  # reads it and so does `nova-pulse fleet plan`, so the tool and the plan cannot
  # disagree about what is managed.
  declared = jsondecode(file("${path.module}/files.json"))

  witnessed    = { for k, v in local.declared : k => v if v.witness }
  trigger_only = { for k, v in local.declared : k => v if !v.witness }

  # The observed hash of every declared file: the witness's reading for a
  # witnessed file, the declaration itself for a trigger-only file (whose
  # trigger is not the witness).
  observed = {
    for k, v in local.declared : k => try(data.external.file_state[k].result.sha256, v.sha256)
  }

  role_runner_count = {
    coordinator = 0
    heavy       = 8
    medium      = 4
    light       = 2
  }
  runner_count = local.role_runner_count[var.role]
}

# The witness: reads the host on every plan. A hand edit changes the observed
# hash, the precondition below fails, and the plan shows the file.
data "external" "file_state" {
  for_each = local.witnessed
  program  = ["bash", "${path.module}/observe.sh", var.ssh_user, var.address, each.value.path]
}

# bench-standard.sh is the authoritative in-sandbox witness on a live host: the
# plan reads the same script, and a DRIFT line it prints while the plan is clean
# is a red test.
data "external" "bench_standard" {
  program = ["bash", "${path.module}/observe_standard.sh", var.ssh_user, var.address]
}

# The writes run once over the SSH a null_resource carries. The trigger hashes
# the declaration, so it is the idempotence key and never the drift detector.
resource "null_resource" "bench_file" {
  for_each = local.declared

  triggers = {
    path   = each.value.path
    sha256 = each.value.sha256
    role   = var.role
  }

  provisioner "remote-exec" {
    connection {
      type = "ssh"
      user = var.ssh_user
      host = var.address
    }
    inline = [
      "install -d \"$(dirname \"$HOME/${each.value.path}\")\"",
      "cat > \"$HOME/${each.value.path}\" <<'NOVA_FILE'",
      each.value.content,
      "NOVA_FILE",
      "test \"$(sha256sum \"$HOME/${each.value.path}\" | cut -d' ' -f1)\" = \"${each.value.sha256}\"",
    ]
  }

  lifecycle {
    precondition {
      condition     = local.observed[each.key] == each.value.sha256
      error_message = "DRIFT ${each.key}: ${each.value.path} observed ${local.observed[each.key]} declared ${each.value.sha256} (a read-each-plan witness, not a null_resource trigger)"
    }
  }
}

# The OS facts, witnessed by the standard script on every plan.
resource "null_resource" "bench_standard" {
  triggers = {
    address     = var.address
    k3s_release = var.k3s_release
  }

  lifecycle {
    precondition {
      condition     = data.external.bench_standard.result.drift == "0"
      error_message = "DRIFT tools/bench-standard.sh reports drift on ${var.address}; the in-sandbox witness and the declaration disagree"
    }
  }
}

# OS packages.
resource "null_resource" "package" {
  for_each = toset(var.packages)

  triggers = {
    package = each.value
    role    = var.role
  }

  provisioner "remote-exec" {
    connection {
      type = "ssh"
      user = var.ssh_user
      host = var.address
    }
    inline = ["sudo apt-get update -qq && sudo apt-get install -y --no-install-recommends ${each.value}"]
  }
}

# The per-line unix users and their homes.
resource "null_resource" "user" {
  for_each = toset(var.users)

  triggers = {
    user = each.value
  }

  provisioner "remote-exec" {
    connection {
      type = "ssh"
      user = var.ssh_user
      host = var.address
    }
    inline = ["id -u ${each.value} >/dev/null 2>&1 || sudo useradd -m -s /bin/bash ${each.value}"]
  }
}

# One single-node k3s per Linux bench, pinned to the release the fleet tests
# against. The Studio (coordinator) is not a node.
resource "null_resource" "k3s" {
  count = var.role == "coordinator" ? 0 : 1

  triggers = {
    release = var.k3s_release
    role    = var.role
  }

  provisioner "remote-exec" {
    connection {
      type = "ssh"
      user = var.ssh_user
      host = var.address
    }
    inline = ["curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=${var.k3s_release} sh -"]
  }
}

# The secrets seat public keys: public by definition, so they may sit in state
# and in the module's inputs. No secret value is ever an input or an output.
resource "null_resource" "seat_public_key" {
  for_each = var.seat_public_keys

  triggers = {
    seat = each.key
    key  = each.value
  }

  provisioner "remote-exec" {
    connection {
      type = "ssh"
      user = var.ssh_user
      host = var.address
    }
    inline = [
      "install -d -m 700 \"$HOME/.ssh\"",
      "grep -qF '${each.value}' \"$HOME/.ssh/authorized_keys\" 2>/dev/null || echo '${each.value}' >> \"$HOME/.ssh/authorized_keys\"",
    ]
  }
}
