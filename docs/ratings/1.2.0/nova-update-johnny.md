# nova-update READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 (Claude Code), a Claude worker given the Grok friend card; this is not Grok's own read
Build: ba3d867c0e31
READ: 7/10
USE: 7/10

Question: is this a good tool for an AI to use?

## Reasons

READ. `nova-update help` opens with one sentence of purpose, a short how-it-works, a usage block, then one short paragraph per subject; every verb's `-h` quotes its own usage line from help, says its effect class (inspection, local write, delivery) and lists its flags. That is findable in a minute. The manifest's six rules print under check, status and report, and the example block is the first-run path. What keeps it from 10 is that three texts still disagree with the binary. The banner says "check and report compare the two" (internal/update/cli.go:165), while `report -h` says "no latest, no network" and the spec says report prints the installed side only (docs/SPEC-UPDATE.md:18). Help says a result's first line is "OK, FAIL or REFUSED" (internal/update/cli.go:199) and the spec's grammar says `CHECK <OK|FAIL>` (docs/SPEC-UPDATE.md:630), but the binary prints `CHECK FAILED`, `STATUS FAILED`, `REPORT FAILED` (internal/update/out.go:30). The spec opens "One tool, three verbs" (docs/SPEC-UPDATE.md:6) and lists four. The usage line names `release <cut|build|install|adopt|pull>` (internal/update/cli.go:146) while `help release` prints a sixth, `release cycle`. The release help is still a wall: each verb's -h repeats multi-hundred-character policy paragraphs.

USE. On a scratch directory on a Linux bench, the first run worked as printed: `example --out versions.tsv` exit 0 and names the next command; a second run says unchanged=true; `report` exit 0 with the installed identity; `status` exit 0 EQUAL; `check` exit 0 with only the count line; `apply ... go --dry-run` exit 0 with the plan `go install golang.org/dl/go1.26.6@latest` and "nothing installed, nothing written". The real job, done on a toy manifest of local scripts: `check` exit 1 with STALE 1.0.0 against 1.2.0 and an UNKNOWN for a missing command; `apply toy --dry-run` left the file at 1.0.0; `apply toy` exit 0 with BEFORE, RUN, AFTER and the file at 1.2.0; the next `check --kind tool` no longer lists toy. `apply toy --version 2.0.0` installed 2.0.0 and the next `status` said NEWER, exit 1, as the exit-code text promises. Refusals are the best part: missing --file, unknown flag (lists the flags), unknown verb (lists the verbs), `--max -1`, `report --send` (names --file, --as, --to at once), a bad header and a short line in one refusal, a name the file lacks (lists its entries), an apply column of none (names the owner), example over a different file. All exit 2. `--json` on version, report and adoption carries the same facts as the lines.

A 10 needs the help's own samples to survive contact. The help offers `local:go version` as a latest (internal/update/cli.go:211, and manifest rule 6 at internal/update/cli.go:256); as a `kind=pin` row it exits 0 EQUAL with installed=version latest=version, because a pin reads the second token (docs/SPEC-UPDATE.md:210). `release cut` and `release install` say the version is "such as 1.2.0" (pkg/release/cli.go:233) and refuse exactly that spelling as not v-prefixed. `watch` puts ADOPT REFUSED, ESCALATE and DONE on stderr and only ADOPT OK on stdout, and the escalate line says "duty files an issue and a fix card" (internal/update/adopt.go:148) while `watch -h` says this tool files nothing. With `apply --version`, APPLY BEFORE prints `latest=2.0.0`, which is the asked target; the source said 1.2.0.

Not tried, because each needs a live service or a real fleet: `report --store`, `report --send` and `--draft` to a bus, `watch --as --to`, `release adopt`, `pull`, `cycle`, and network latest schemes. `release build` has no --dry-run and was not run for real.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:165 | The banner says check and report compare installed with latest; report reads installed only (report -h, docs/SPEC-UPDATE.md:18). | Say check and status compare; report prints what this box runs. | S |
| 2 | internal/update/out.go:30 | The first line prints FAILED; help (internal/update/cli.go:199) and the spec grammar (docs/SPEC-UPDATE.md:630) say FAIL. An AI matching the documented word misses every failure. | Make the word and the two texts one spelling, held by a test. | S |
| 3 | `nova-update status --file toy.tsv --kind pin` | A pin row using the help's own sample `local:go version` exits 0 EQUAL with installed=version latest=version. | Stop offering `go version` as a sample where a pin can take it, or refuse a pin read whose token is not version-shaped. | M |
| 4 | pkg/release/cli.go:233 | --version help says "such as 1.2.0"; cut and install refuse 1.2.0 as not v-prefixed. | Say "such as v1.2.0". | S |
| 5 | internal/update/adopt.go:148 | watch splits one pass over stdout and stderr, and ESCALATE claims a duty files an issue while watch -h says this tool files nothing. | One stream for the pass; say who would file, not that it happened. | S |
| 6 | internal/update/cli.go:146 | The usage line lists five release verbs; help release lists six (cycle). | Name cycle in the usage line, or say "nova-update help release". | S |
| 7 | docs/SPEC-UPDATE.md:6 | "One tool, three verbs", followed by four verb bullets and an eight-surface verb block. | Open with the verbs that exist. | S |
| 8 | `nova-update apply --file toy.tsv toy --version 2.0.0` | APPLY BEFORE prints latest=2.0.0, the asked target, under source=local:./toy/latest.sh, whose answer is 1.2.0. | Print latest= as the source's answer and target= separately. | S |

## Good, keep

Every refusal names the problem, the full set of what is missing or allowed, and the next command, with exit 2 (`report --send`, `apply ... nosuch`, a bad manifest).
`apply` prints BEFORE, RUN and AFTER and re-reads the version, so an install that did nothing cannot read as OK; `--dry-run` plans the same argv and starts no process.
Each verb's -h quotes its usage line from help and names its effect class, so a cold reader knows before running whether it writes.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| Grok 1.1.0 READ 6.5/10 | IMPROVED to 7 | per-verb -h now quotes help's usage and names the effect class; the banner and spec-opening defects remain |
| Grok 1.1.0 USE 7/10 | SAME, 7 | local upgrade completes and rechecks; pin, watch stream and release sample are unchanged |
| banner says report compares | STILL THERE | internal/update/cli.go:165 |
| pin through the help's local:go version reports latest=version | STILL THERE | `status --kind pin` prints installed=version latest=version, exit 0 |
| release verbs refuse the help's example version | STILL THERE | `release cut --version 1.2.0 --dry-run` REFUSED, not v-prefixed |
| watch summary on stderr | STILL THERE | `watch --adopt checks.tsv 2>/dev/null` prints only ADOPT OK |
| spec says three verbs | STILL THERE | docs/SPEC-UPDATE.md:6 |
| manifest illustration says apply none, example writes an installer | STILL THERE | internal/update/cli.go:256 vs internal/update/example.go:20 |
