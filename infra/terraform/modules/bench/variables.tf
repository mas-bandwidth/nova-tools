variable "name" {
  description = "The bench name."
  type        = string
}

variable "role" {
  description = "The bench role: coordinator, heavy, medium or light. A role is a contract, not a copy."
  type        = string

  validation {
    condition     = contains(["coordinator", "heavy", "medium", "light"], var.role)
    error_message = "role must be one of coordinator, heavy, medium or light."
  }
}

variable "address" {
  description = "The bench's ssh target (a host name or an ssh config alias)."
  type        = string
}

variable "mac" {
  description = "The bench's wake-on-lan mac; \"-\" when the bench never sleeps."
  type        = string
  default     = "-"
}

variable "ssh_user" {
  description = "The unix user the module writes as."
  type        = string
  default     = "rowan"
}

variable "k3s_release" {
  description = "The pinned k3s release."
  type        = string
}

variable "packages" {
  description = "The OS packages to install on the bench."
  type        = list(string)
  default     = []
}

variable "users" {
  description = "The per-line unix users to create on the bench."
  type        = list(string)
  default     = []
}

variable "seat_public_keys" {
  description = "The secrets seat public keys, keyed by seat name; public by definition."
  type        = map(string)
  default     = {}
}
