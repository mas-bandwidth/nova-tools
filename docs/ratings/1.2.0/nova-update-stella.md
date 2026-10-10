# nova-update READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker given a friend's re-rate card
Build: 3a5792afa395
READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-update version` prints `nova-update v1.0.1-0.20261006144833-3a5792afa395 linux/amd64 go1.26.6`. Built and run on a Linux bench machine, in a scratch directory with a scratch HOME; no live store, no server, no bus, no forge.

## Reasons

READ. `nova-update help`, every verb's `-h`, `help release`, every release verb's `-h` and docs/SPEC-UPDATE.md were read cold. The banner says what the tool does in one line, then how it works and a first run that works from the binary alone. The manifest is stated in six numbered lines, byte for byte, and the exit codes are one paragraph. Each manifest verb's `-h` carries its usage line, an `effect:` line and its flags. The first place of confusion is internal/update/cli.go:165: "check and report compare the two", while `report -h` says it reads no latest. The first place of doubting a claim is the output grammar: the help and the spec (docs/SPEC-UPDATE.md:621) say a first line is OK, FAIL or REFUSED, and the binary prints FAILED (internal/update/out.go:30). The first place of boredom is the release half: the top usage line lists five release verbs (internal/update/cli.go:146) and `help release` lists six. Each release note is one paragraph of several hundred characters about a coordinator, a dogfood gate and a fleet play, which a reader checking their own tools never needs. The spec opens "One tool, three verbs" (docs/SPEC-UPDATE.md:6), then lists four, and later says "five usage lines" and "Five verbs" (docs/SPEC-UPDATE.md:415 and :440) for six.

USE. Everything below was run for real. `example` printed the manifest, wrote it, left it unchanged on a second run and refused to overwrite another file. `report`, `check`, `status` (lines and `--json`) and `apply --dry-run` ran on it. A local fake tool with a local latest went STALE, `apply` moved it from 0.1.0 to 0.2.0 with BEFORE, RUN and AFTER lines and exit 0, `status` then said EQUAL, `apply --version 9.9.9` installed past latest, and `check` said NEWER with exit 1. `report --snapshot` said changed=yes and then changed=no and left its .lock file beside it. `report --draft` printed the note, and `--send` without `--to` was refused. `watch` ran a passing check and a failing one, and `adoption` read a ledger with and without `--as` and `--json`. `release install` installed a hand-made artifact directory verified by SHA256SUMS, and `release pull --dry-run` printed its plan. About fifteen refusals were provoked: no verb, an unknown verb, an unknown flag, missing `--file`, a bad `--kind`, `--max -1`, a manifest with five problems (all five named in one line, each with its line number and remedy), an apply with no name and with a wrong-case name, an unversioned `--version` on install, a missing artifact directory (answered with the exact build command), and an unknown adoption state. Every one named the fault and the next command.

The score is held at 7 by four things met in use. The example manifest's own `apply` cannot succeed. A real `apply --file versions.tsv go` ran `go install golang.org/dl/go1.26.6@latest`, hit the default 5s `--timeout` and printed `APPLY FAILED ... took=5.057s: timeout` with no remedy. Even with time, that command installs a `go1.26.6` wrapper and never changes what `go version` reads, so the AFTER read can never move. A pin row written the way the help suggests (`local:go version`) answers installed=version latest=version, EQUAL, exit 0: a green that is not true. `watch` splits one pass over two streams, so a caller reading stdout sees `ADOPT OK` and loses the REFUSED, ESCALATE and DONE lines. And the FAILED spelling means a caller parsing by the documented grammar misses every failure line.

A 10 needs:
- an example manifest whose apply can succeed, or an apply of none, and an apply default timeout that fits an installer;
- a pin read that refuses when no token is version-shaped;
- one stream for a watch pass, or a `--json` receipt;
- one spelling of FAIL across binary, help and spec;
- the release pipeline's surface stated once and correctly (six verbs), in help and spec.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/example.go:20 | the example's apply `go install golang.org/dl/go{version}@latest` installs a `go<v>` wrapper and never changes what `go version` reads, so apply on it can only end FAIL; with `local:go version` on both sides it is also always EQUAL | make the example's apply `none` and say so, or ship an example whose apply can move the installed read | S |
| 2 | internal/update/cli.go:196 | apply inherits the 5s default `--timeout` for the install command itself; the example's own apply timed out at 5.057s and the FAIL line names no remedy | give apply its own default (minutes), and print `pass --timeout <d>` on a timeout FAIL | S |
| 3 | internal/update/out.go:30 | first lines print FAILED; help (cli.go text "OK, FAIL or REFUSED") and docs/SPEC-UPDATE.md:621 grammar say FAIL | one spelling across binary, help and spec, held by a test | S |
| 4 | status --kind pin | a pin row with the help's own `local:go version` reads the second token, so installed=version latest=version, EQUAL, exit 0 | refuse a pin read whose token is not version-shaped, naming the two-token shape | S |
| 5 | internal/update/adopt.go:148 | watch prints ADOPT OK on stdout and ADOPT REFUSED, ESCALATE and DONE on stderr; ESCALATE says "duty files an issue and a fix card" while watch -h says this tool files nothing | one stream for the whole pass, or a --json receipt; say who answers, not that something was filed | M |
| 6 | internal/update/cli.go:165 | banner says check and report compare installed with latest; report -h says it reads no latest | say check and status compare; report prints what this box runs | S |
| 7 | internal/update/cli.go:146 | the top usage line lists release verbs cut, build, install, adopt, pull; `help release` and the refusal list six with cycle (also cli.go:239) | name cycle in both places | S |
| 8 | docs/SPEC-UPDATE.md:6 | "One tool, three verbs", then four bullets; :415 "five usage lines" and :440 "Five verbs" for six release verbs | open with the verbs that exist, and count six | S |
| 9 | internal/update/cli.go:702 | APPLY BEFORE (and :733 under --dry-run) prints latest= as the asked --version (9.9.9) while the source answered 0.2.0 | print latest= as the source's answer and target= separately | S |
| 10 | pkg/release/cli.go:233 | every release verb's --version help says "such as 1.2.0"; install refuses 1.2.0 as not v-prefixed | say such as v1.2.0 | S |
| 11 | release install -h | the only verb that writes a live bin directory has no effect line and no --dry-run (refused as an unknown flag) | add the effect line and a --dry-run that prints the plan | M |
| 12 | pkg/release/install.go:352 | success prints RELEASE INSTALLED, after `release:` progress lines, while its refusals print INSTALL REFUSED; the first token is not the verb's own | one first token per verb, as the manifest verbs do | S |
| 13 | pkg/release/pull.go:275 | `pull --dry-run` says files=3 for a directory holding two files: the count includes a SUMS.digest that is not there, and the files are not named | count and name only the files that exist | S |
| 14 | internal/update/report.go:91 | a draft with no --host has the subject "versions on - at <stamp>" | leave the host out of the subject when none is given | S |
| 15 | docs/SPEC-UPDATE.md:23 | "this estate's first tool", "The estate runs it nightly", and a release pipeline written for a coordinator and a fleet play; a stranger has no estate | describe what the tool does, and move the release pipeline to its own spec | M |

## Good, keep
Refusals that name every problem of an invocation at once, each with its line and remedy, then one `run:` line (a five-problem manifest was fixed from one answer).
The local apply loop: BEFORE, RUN with the resolved argv, AFTER re-read against the target, with `--dry-run` showing the same command and starting nothing.
`example --out` that never overwrites and says `unchanged=true` on a rerun, and a first run that works from the binary alone.
One `--json` envelope (result, facts, items) on every manifest verb.
`release install` verifying every artifact against SHA256SUMS, and its missing-artifact refusal printing the exact build command.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| READ 6.5/10 and USE 8/10 at 1.1.0 (docs/ratings/1.1.0/update-read.md, update-use.md) | CHANGED | READ 7/10, USE 7/10: the example's apply fails in use, and FAILED against the documented FAIL |
| a pin through the help's own `local:go version` reports latest=version | STILL THERE | `status --file pin.tsv` prints `STATUS EQUAL name=gp kind=pin installed=version latest=version`, exit 0 |
| release verbs refuse the help's example version | STILL THERE | `release install --version 1.2.0` refuses: not v-prefixed |
| a refused check splits a watch pass across streams | STILL THERE | ADOPT OK on stdout; ADOPT REFUSED, ESCALATE and DONE on stderr, exit 1 |
| `release install -h` has no effect line and no --dry-run | STILL THERE | `release install --dry-run` refused as an unknown flag |
| adoption -h never names the state vocabulary | CHANGED | -h still does not; the refusal for an unknown state lists all eight |
| "One tool, three verbs" | STILL THERE | docs/SPEC-UPDATE.md:6 |
| a hand-built CLI beside the skeleton | STILL THERE | internal/update/cli.go:408 builds its flags with flag.NewFlagSet; cli.go is 1109 lines |
| the local fake upgrade completes and rechecks | STILL THERE | APPLY OK from=0.1.0 to=0.2.0, AFTER installed=0.2.0, exit 0 |
