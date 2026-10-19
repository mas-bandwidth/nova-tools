The stable tools point release after v1.6.8. It removes the two refusals that kept a native Windows member from starting. nova-sprint stays at v1.6.6.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.9 --bin <dir>` with the nova-sprint v1.6.6 binaries staged beside them (see docs/SPEC-RELEASE.md).

## What changed in v1.6.9

- `nova-swarm member` starts on Windows. Before this, it refused with "no setpriority on windows". `internal/yield` now has a Windows step behind CI: the process goes to `BELOW_NORMAL_PRIORITY_CLASS`, and every child inherits it. `yield.Supported` is true there.
- nova-secrets reads a key file's access list on Windows instead of its POSIX mode. Windows reports every file as 0666, so every key used to be refused. The new `pkg/winacl.OwnerOnly` uses the rule OpenSSH for Windows applies to a private key: owner and grants are limited to this user, SYSTEM and Administrators. Invariant 6 (the key file and its directory), keygen's directory check and the seat environment file (`pkg/seatcred`) use it.
- Windows unit tests cover both changes. Linux CI type-checks them through `make vet-windows`.
