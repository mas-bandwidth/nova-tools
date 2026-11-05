# Moved out of nova-tools

Code that left nova-tools when it was stripped to its minimal set (2026-11-05). Each is still reachable at
tag v1.6.29.

| What | Where it went |
|---|---|
| `nova-update release` (cut, build, install, adopt, pull, cycle), `pkg/release`, `tools/ghrelease`, `pkg/dogfood` | mas-bandwidth/rowan-tools, as `rowan-release` (cut, build, publish, install, adopt, pull, cycle) |
| `cmd/nova-update`, `internal/update` (check, status, apply, report, watch, adoption, example) | `update/`: a new nova-update that only installs releases (report, install, release install, version) |
| `pkg/releasewire`, `schemas/release` | retired: generated, and imported by nothing |
