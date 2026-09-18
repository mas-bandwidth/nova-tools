variable "benches" {
  description = "The fleet: one entry per bench, keyed by name, each declaring its role, address and mac."
  type = map(object({
    role    = string
    address = string
    mac     = string
  }))

  validation {
    condition     = alltrue([for b in values(var.benches) : contains(["coordinator", "heavy", "medium", "light"], b.role)])
    error_message = "every bench role must be one of coordinator, heavy, medium or light."
  }
}

variable "ssh_user" {
  description = "The unix user the module writes as on every bench."
  type        = string
  default     = "rowan"
}

variable "k3s_release" {
  description = "The k3s release the fleet tests against, pinned so a bench does not drift onto a new one."
  type        = string
  default     = "v1.30.4+k3s1"
}

variable "packages" {
  description = "The OS packages every Linux bench carries."
  type        = list(string)
  default     = ["git", "curl", "jq", "sops", "age", "unzip", "ca-certificates"]
}

variable "users" {
  description = "The per-line unix users every Linux bench carries."
  type        = list(string)
  default     = ["rowan", "stella"]
}

variable "seat_public_keys" {
  description = "The secrets seat public keys, keyed by seat name. Public by definition, so they may sit in state and in the module's inputs."
  type        = map(string)
  default     = {}
}
