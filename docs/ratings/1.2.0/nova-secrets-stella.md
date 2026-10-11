# nova-secrets READ and USE rating, nova-tools 1.2.0

Rater: gpt-5.6-terra (Codex harness)
Build: 4d6372b0556d
READ: 7/10
USE: 7/10

## Reasons

READ. I read `nova-secrets help`, every listed verb's `-h` (including both `seat`
subverbs), and `docs/SPEC-SECRETS.md` cold. The tool explains its safety model
early: one seat file, selected names delivered only to one child environment, and
no value printed. Its usage and refusal grammar are unusually concrete, and the
`check` and `exec` help explain the upstream requirement before a caller reaches
the refusal. The reading score stops at 7 because the advertised empty-store
setup cannot produce its own `.sops.yaml`, the first-value path depends on a
macOS-only executable path even though the flag prose says to use `command -v`,
and the package documentation still describes a four-verb tool that now has ten
top-level verbs plus two `seat` subverbs.

USE. I built the exact release-candidate source on Linux as
`nova-secrets v1.0.1-0.20261008030716-4d6372b0556d linux/amd64 go1.26.6`, with
real `age-keygen` and `sops`, in an isolated job directory. No live store,
server, or credential was used. `keygen` without a store succeeded and created
a mode-0600 scratch key without exposing its private material. The advertised
new-store `keygen --store` step then refused because `.sops.yaml` did not yet
exist, and the advertised first-value `seal` likewise refused because the new
seat file did not exist. After a manually seeded encrypted random test value
solely to reach the normal path, `names`, `check`, and `exec --only TOKEN --require TOKEN -- sh -c 'test -n "$TOKEN"'` each exited 0; `exec` named the child and
metadata without printing the value. That core behavior is good, but an AI
cannot complete the documented first store without knowing an undocumented
manual bootstrap, so USE is 7 rather than a release-ready 10.

A 10 needs a single runnable empty-store path that creates the initial rule,
seat file, and first value; platform-neutral executable examples; a corrected
rule-line count; and package documentation that describes the actual interface.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-secrets/main.go:100-117 | The printed setup says to run `keygen --store ./secrets | ... > .sops.yaml`, but `keygen` refuses because that same store has no `.sops.yaml`; after manually supplying a rule, the printed `first value` command refuses because `ada.yaml` does not exist. The documented bootstrap is circular. | Let the empty-store path create or accept the initial rule and empty seat file, and print one command that makes the first encrypted value. | M |
| 2 | cmd/nova-secrets/main.go:114-130 | `first value` and every example pass `/opt/homebrew/bin/sops` or `/opt/homebrew/bin/age-keygen`; those paths do not exist on the Linux bench even though real binaries are available on PATH. | Use `"$(command -v sops)"` and `"$(command -v age-keygen)"` in every runnable example. | S |
| 3 | pkg/secrets/keygen.go:15,21-29 | A successful keygen prints three `SECRETS RULE` lines but then says “add these two lines to .sops.yaml”. | Say “add these lines”, or print an exact two-line snippet. | S |
| 4 | pkg/secrets/secret.go:1-3 | The package doc says it provides four verbs, while the help exposes ten top-level verbs and `seat add` and `seat inject`. | Describe the present interface, or describe the package by responsibility rather than a stale verb count. | S |

## Good, keep

- The built binary's `keygen`, `names`, `check`, and `exec` receipts carry no
  secret value; `exec` gives a useful metadata-only OK line and preserves the
  child command's success.
- `check` validates the scratch store and reports recipients, sealed files,
  ownership, and head in one compact line.
- The help maps the flags and expected effects clearly, and refusal messages
  name the unmet store shape instead of silently guessing.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| package documentation said four verbs | STILL THERE | pkg/secrets/secret.go:1-3 at Build 4d6372b0556d |
| examples used one platform's fixed executable paths | STILL THERE | cmd/nova-secrets/main.go:114-130 at Build 4d6372b0556d |
| core commands were expected to keep values out of output | KEPT | real Linux `names`, `check`, and `exec` receipts all succeeded without a value |
| a stranger needed a reproducible first sitting | WORSE/UNRESOLVED | the exact printed empty-store keygen and first-value commands both refused in the scratch store |
