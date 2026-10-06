# Setup

This document describes how to set up the nova tools on a bench machine.

## Dependencies

### dep-secrets-b.w3

**What it is:** The nova-secrets store and keys for the seat.

**Who needs it:** The coordinator seat and any bench that runs nova-secrets verbs.

**How nova-up provides it:** The `secrets` step of nova-up (order 50) creates:
- An age key pair at `NOVA_SECRETS_KEY`
- A git bare upstream beside the store
- A git working copy at `NOVA_SECRETS_STORE`
- The `.sops.yaml` rules file for the seat

**How a person sets it up manually:**
1. Generate an age key: `nova-secrets keygen --as <seat> --key <path> --age-keygen /path/to/age-keygen`
2. Clone the secrets store: `git clone <store-url> $NOVA_SECRETS_STORE`
3. Add the seat via `nova-secrets seat add`

**What the doctor checks:** The `secrets` check in nova-doctor verifies:
- sops is on PATH
- The seat's age key file exists and is readable
- The key file has the public key comment line
- The store is a git working copy
- The seat's yaml file exists in the store

---

### dep-go-sdk-b.w2

**What it is:** The Go toolchain needed to build and test the repository.

**Who needs it:** Every bench that builds or tests nova-tools.

**How nova-up provides it:** Not automatic; install Go manually or via a package manager.

**How a person sets it up manually:** Install Go matching the version in go.mod's toolchain line.

**What the doctor checks:** The `gosdk` check in nova-doctor verifies:
- go is on PATH
- go version matches go.mod's toolchain line
- GOCACHE is set and writable
- GOFLAGS carries -mod=readonly
