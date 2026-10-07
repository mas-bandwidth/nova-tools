# nova-ci dogfood — Codex, 2026-10-07

I read `nova-ci`'s own help and `docs/CLI.md`, then used each listed verb at
least once against this Linux scratch checkout. I built the tool from base
`ea4a285c32a5f58c3b65b85ca2a5ea6c32924d82`; scaffold verbs and the receipt
writer used `--dry-run`. A branch-built macOS binary used `bench run` to run
`go version go1.26.6 linux/amd64` on a Linux scratch host, then removed its
run directory. I did not write to a Redis store.

No URGENT or NEXT findings. The clean-checkout `local --base HEAD --dry-run`
selected `./internal/ci` and `./internal/docs`, which the CI selector's
specification says are run on every change. `functional ./internal/docs`
reported zero tagged packages with a `reason=` token, as the CLI page
specifies. Both observations matched the documented behavior.

READ 9/10 — the help groups commands by where they work, and each verb's help lists its flags and effect.
USE 9/10 — dry runs made the write-shaped verbs safe, and the CI commands' output matched their documented behavior.
urgent=0 next=0
