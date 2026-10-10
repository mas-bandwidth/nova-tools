# nova-update READ and USE rating, nova-tools 1.2.0

Rater: Zhi (deepseek/deepseek-v4 on dsh)
Build: bfc6a3118d7e
READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of
`sprint/mechanical-2026-10-02` (bfc6a3118d7e), whose `nova-update version` prints
`nova-update v1.0.1-0.20261007203554-bfc6a3118d7e linux/amd64 go1.26.6`. `nova-update help`, every
verb's `-h`, `help release`, each release verb's `-h` and docs/SPEC-UPDATE.md were read cold, then
the binary was built and used for real on a Linux bench in a scratch directory with a scratch HOME:
no live store, no server, no bus, no key. The only network reach was the tool's own forge read inside
`release cut --dry-run` and one `github:` latest read; `report --store`, `--send` and watch delivery
were not tried (no fleet store, no bus), and the release verbs past `cut --dry-run` were judged from
their help lines.

## Reasons

READ. The banner answers the three questions: one line for what it does, a `how it works:` paragraph,
and a `first run` that runs from the binary alone. Each manifest verb's `-h` carries its usage, an
`effect:` line, its flags with what each wants, and the exit table; the manifest is stated in six
numbered lines. The first place of confusion is internal/update/cli.go:165: the banner says "check and
report compare the two", while `report -h` says it reads no latest and makes no network. The first
place of doubt is the surface. The top usage line lists five release verbs (internal/update/cli.go:146)
and `help release` lists six with `cycle` (pkg/release/cli.go:22); the banner calls them "build,
publish and install" (internal/update/cli.go:167), naming a verb that does not exist; and the no-verb
and unknown-verb refusals list eight verbs (internal/update/cli.go:92) while the usage prints nine with
`help`. The first place of boredom is the release half: each release note is one paragraph of several
hundred characters about a coordinator, a dogfood gate and a fleet play, which a reader checking their
own tools never needs. The spec opens "One tool, three verbs" (docs/SPEC-UPDATE.md:6), then lists four,
and later says "five usage lines" (:415) and "Five verbs" (:440) for six release verbs. A 10 states one
surface once, in help and spec, and counts it correctly.

USE. Everything below was run for real. `example` printed the manifest, wrote it, left it unchanged on a
second run and never overwrote another file. `report`, `check`, `status` (lines and `--json`) and
`apply --dry-run` ran on it; a local fake tool with a local latest went STALE and `apply` moved it
0.1.0 to 0.2.0 with BEFORE, RUN and AFTER and exit 0. A manifest with two bad lines was refused once,
naming all three problems, each with its line. About twenty refusals were provoked: no verb, an unknown
verb, an unknown flag, a missing `--file`, a `--file` that does not exist, a bad `--kind`, `--max -1`,
a bad header, five and seven fields, an empty field, an argv with two adjacent blanks, a bad latest scheme, `--dry-run`
on check/status/report, `apply` with no name and with two names and with a name the file lacks and on
a model and on `apply none`, `--version` against a `{version}`-less argv, an unknown adoption state,
`--send` missing `--as` and missing `--to`, and `watch --json`. Every one named the fault and the next
command, and the exit contract held (0 current, 1 the tool saying NO, 2 a refusal). `report --snapshot`
said changed=yes then changed=no, and `report --draft` printed the four-line header and the body with
its `MORE` line. `release install --version 1.2.0` and `release cut --dry-run` were probed.

The score is held at 7 by six things met in use. The pin read is a green lie: a `kind=pin` row written
the way the help suggests (`go version` against `local:go version`) reads `installed=version latest=version`, says EQUAL and exits 0 (internal/update/read.go:72). One watch pass is split over two
streams, so a caller reading stdout sees `ADOPT OK` and loses `ADOPT REFUSED`, `ADOPT ESCALATE` and
`ADOPT DONE` (internal/update/adopt.go:155). `apply` gives the install command the same 5s default as a
version read: a 6s installer died `APPLY FAILED ... took=5.022s: timeout` with no remedy, though
internal/update/cli.go:413 says "such as 5m for a slow installer". Failure lines print FAILED while the
banner (internal/update/cli.go:199) and docs/SPEC-UPDATE.md:630 say FAIL, so a parser built on the
documented grammar misses every one. `release install`, the verb that writes a live bin directory, has
no effect line and no `--dry-run`. And `release cut --dry-run` asks the forge where the branch is
before any plan exists (pkg/release/cut.go:489), so a no-touch verb makes a real remote read. A 10
needs a pin token that must be version-shaped, one stream (or a `--json` receipt) for a watch pass, an
apply default that fits an installer, one spelling of FAIL, the install effect line and dry-run, and an
offline cut plan.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:146 | the top usage line lists release verbs cut, build, install, adopt and pull, while `help release` (pkg/release/cli.go:22) and the release refusal (pkg/release/cli.go:186) list six with cycle, so the front door hides a verb the binary answers | name cycle in the one usage line too | S |
| 2 | internal/update/cli.go:167 | the banner says "The release verbs build, publish and install nova-tools' own releases"; publish is not a verb, and cut, adopt, pull and cycle are missing | name the six release verbs that exist | S |
| 3 | internal/update/cli.go:92 | updateVerbNames omits help, so the no-verb and unknown-verb refusals list eight verbs while `nova-update help` is a verb the usage prints | add help to the refusal's verb list | S |
| 4 | internal/update/read.go:72 | a pin read takes the second token with no version shape: `go version` against `local:go version` prints installed=version latest=version, EQUAL, exit 0 | refuse a pin token that is not version-shaped, with the two-token remedy | S |
| 5 | internal/update/adopt.go:155 | one watch pass is written to two streams: ADOPT OK to stdout, and ADOPT REFUSED, ADOPT ESCALATE and ADOPT DONE to stderr when any check refuses | keep every ADOPT line of a pass on one stream, or add a `--json` receipt | M |
| 6 | internal/update/cli.go:406 | apply gives the install command the same 5s default as a version read; a 6s installer died `APPLY FAILED ... took=5.022s: timeout` with no remedy, while cli.go:413 says "such as 5m for a slow installer" | give apply its own default and print `pass --timeout <d>` on a timeout | S |
| 7 | internal/update/out.go:30 | first lines print FAILED while the banner (internal/update/cli.go:199) and docs/SPEC-UPDATE.md:630 say FAIL | one spelling of FAIL across binary, help and spec | S |
| 8 | internal/update/cli.go:165 | the banner says "check and report compare the two", while `report -h` says it reads no latest and makes no network | say check and status compare; report prints what this box runs | S |
| 9 | internal/update/example.go:20 | the example's apply `go install golang.org/dl/go{version}@latest` installs a `go<v>` wrapper and never changes what `go version` reads, and disagrees with the banner's own example row (internal/update/cli.go:256, apply none, owner me) | make the example's apply none, or ship an example whose apply moves the installed read, and make the two examples agree | S |
| 10 | pkg/release/cli.go:242 | every release verb's `--version` help says "such as 1.2.0", and install, build and pull all refuse `1.2.0` as not v-prefixed | say "such as v1.2.0" | S |
| 11 | release install -h | the release verb that writes a live bin directory has no effect line and no `--dry-run` (refused as an unknown flag) | add the effect line and a `--dry-run` that prints the install plan | M |
| 12 | pkg/release/cut.go:489 | `release cut --dry-run` runs the forge read `gh api repos/<owner>/<name>/commits/<branch>` before any plan exists, so a no-touch verb makes a real remote read | read the range locally first and reserve the forge read for a real cut | M |
| 13 | pkg/release/install.go:352 | a success prints RELEASE INSTALLED while the same verb's refusals print INSTALL REFUSED and a failure INSTALL FAILED (pkg/release/install.go:267), so the verb's first token is not stable | one first token per verb, as the manifest verbs do | S |
| 14 | internal/update/adoption.go:71 | `adoption -h` never names the eight states its ledger accepts, though the refusal on a bad state lists them | enumerate the states in the `--file` flag help | S |
| 15 | docs/SPEC-UPDATE.md:6 | "One tool, three verbs" is false (ten verbs), and `:415` "five usage lines" and `:440` "Five verbs" count six release verbs | open with the verbs that exist, and count six release verbs | S |
| 16 | internal/update/cli.go:220 | `version -h` quotes the whole check, apply and report exit table, which describes verbs it does not have | give version its own one-line exit table | S |
| 17 | internal/update/cli.go:702 | `apply --dry-run --version 9.9.9` prints `APPLY STALE ... latest=9.9.9` while the source answered 0.1.0, so the source's answer is not readable on the line | print the source's latest and the asked target as separate fields | S |

## Good, keep
- Every refusal names the problem, the vocabulary and the exact next command, and one run reports every
  independent problem at once: a manifest with two bad lines named all three problems with their line
  numbers; a missing `--file`, an unknown flag, an unknown verb, `--max -1` and a bad `--kind` each
  cost one line.
- The exit contract printed in every help held in every probe: 0 current or a dry-run plan, 1 the NO
  with counts, 2 the refusal with the remedy.
- One `--json` envelope on every verb that takes it, from the same value as the lines: an item's
  `reason` and `remedy` are typed fields, and a refusal is `result` with `why`.
- `example --out` never overwrites, `report --snapshot` writes the `observed` map and says
  `changed=yes` then `changed=no`, and `--max` bounds every listing with a `MORE` line carrying the
  true total.
- A local apply loop leaves nothing to infer: BEFORE, RUN and AFTER, and an APPLY OK only when the
  after-read equals the target.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-built CLI beside the skeleton (1.1.0 READ) | STILL THERE | internal/update/cli.go:288 builds its own flags, dispatch and output; internal/update/versiontool.go:14 is the skeleton |
| a release pipeline larger than the tool (1.1.0 READ) | STILL THERE | docs/SPEC-UPDATE.md:441 and the pkg/release package |
| "One tool, three verbs" is false (1.1.0 READ) | STILL THERE | docs/SPEC-UPDATE.md:6; the binary answers ten verbs |
| a pin read answers installed=version latest=version (1.1.0 USE, 2026-10-02) | STILL THERE | `go version` against `local:go version` as kind=pin: STATUS EQUAL, exit 0 |
| watch splits a pass over two streams (1.1.0 USE) | STILL THERE | internal/update/adopt.go:155; ADOPT OK on stdout, REFUSED, ESCALATE and DONE on stderr |
| release install has no effect line and no dry-run (1.1.0 USE) | STILL THERE | `release install -h` and `release install --dry-run` (unknown flag) |
| release cut --dry-run reaches the forge first (1.1.0 USE) | STILL THERE | pkg/release/cut.go:489; the run printed "asking owner/name where dev is" then a gh read |
| three cold raters scored 6, 6.5 and 7 (2026-10-01); one scored 8 USE (1.1.0) | CHANGED to 7 READ and 7 USE | the surface and the pin lie are unchanged, while refusals, the JSON shape, bounded output and the local apply loop remain the tool's strength |
