## Fleet

`tools/bench-standard.sh` is the one admin entry for a Linux bench: it checks
the bench against the standard and prints `STANDARD OK ...`, or one
`DRIFT <what>` line per finding followed by `STANDARD DRIFT (see lines
above)`. It checks that each `$HOME/runner-nova-tools-<i>/` holds exactly one
listener process descending from `nova-runner-<i>.service` (runner checks run
only when `uname` is Linux), that each unit file carries `Environment=PATH`
with `go/bin` and `.local/bin`, `KillMode=control-group` and
`TimeoutStopSec=30s`, that `go version` equals `$NOVA_GO` (default
`go1.26.5`) with `sbcl` on `PATH` and the harness at
`$HOME/nova-bench/harness-<ver>/opencode`, that the 16 nova bins in
`$HOME/.local/bin` each report `$NOVA_WANT`, that exactly one `*.key` sits
under `$HOME/.config/nova-secrets` with `nova-secrets check` passing for it,
and that no plaintext key file (`$HOME/.local/share/opencode/auth.json`,
`$HOME/.config/deepseek/env`) and no literal `apiKey": "sk-` in
`$HOME/.config/opencode/*.json` survives; `--apply` kills stray runner
listeners not under their unit, nothing else destructive.
