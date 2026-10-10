# nova-update READ and USE rating, nova-tools 1.2.0

Rater: deepseek/deepseek-v4.1-flash in dsh (the DeepSeek Harness headless runner), dealt the re-rate card for this tool
Build: bc9ee29ed2b0
READ: 7/10
USE: 7/10

Question: is this a good tool for an AI to use?

## Reasons

READ. `nova-update help` opens with one sentence of purpose, a short how-it-works, one usage line per verb, then one short paragraph per subject; every manifest verb's `-h` quotes its usage line from help, states its effect class and lists its flags with what each wants. The manifest's six rules print under check, status and report, and the `example:` block is a first run that works from the binary alone. The refusals are the best part of the read: a missing flag, an unknown flag, an unknown verb and a bad `--max` each name the problem, the valid vocabulary and the next command, and a bad manifest names every problem at once with its line. What holds the score at 7 is the number of texts that disagree with the binary: the banner still says check and report compare the installed and latest sides while `report -h` says no latest and no network; the first line prints `FAILED` where help and the spec grammar say `FAIL`; the usage line lists five release verbs while `help release` prints six; and the release half is still a wall of per-verb policy paragraphs. No release verb's `-h` carries an `effect:` line at all, though every manifest verb's does, and `release install`, the verb that writes the live bin directory, has no `--dry-run`.

USE. Every command below ran for real on a Linux bench machine in a throwaway directory, with no live store, no server and no key touched. `example --out versions.tsv` wrote the manifest, a second run said `unchanged=true`, and `report`, `status` and `apply --dry-run` ran on it. A local fake tool went STALE, `apply` moved it from 0.1.0 to 0.2.0 with BEFORE, RUN and AFTER lines and exit 0, and `status` then said EQUAL. `report --json`, `report --snapshot` (`changed=yes` then `changed=no`) and `report --draft` printed the note. `release install` verified a hand-made artifact directory against SHA256SUMS and installed it, and its missing-artifact refusal printed the exact build command. About fifteen refusals were provoked and every one named its fault and the next command at exit 2. What holds the score at 7 in use: a pin row written the way the help suggests (`local:go version`) answers installed=version latest=version, counts current and exits 0, which is a green that is not true; the example's own `apply` runs `go install golang.org/dl/go{version}@latest`, which installs a wrapper and never moves what `go version` reads, and against the shared 5s `--timeout` a 6s installer dies `APPLY FAILED ... timeout` with no remedy; `watch` splits one pass across stdout and stderr and its ESCALATE line says a duty files an issue while `watch -h` says this tool files nothing; and `apply --version <v>` prints the asked target as `latest=` on the BEFORE and STALE lines even though the source answered something else.

Not tried, because each needs a live service, a forge, or a real fleet: `report --store`, `report --send`, `watch --as --to`, the network latest schemes (github:, npm:, brew:, ollama:), and `release build`, `adopt`, `pull` and `cycle`. Those were judged from their help lines and their Go source.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:165 | the banner says check and report compare installed with latest; `report -h` says report reads no latest and makes no network read | say check and status compare; report prints what this box runs | S |
| 2 | internal/update/out.go:30 | the first line prints `CHECK FAILED` / `STATUS FAILED` / `APPLY FAILED`, while help (internal/update/cli.go:199) and the spec grammar (docs/SPEC-UPDATE.md:630) say a first line is `OK`, `FAIL` or `REFUSED`; a caller matching the documented word misses every failure | one spelling of FAIL across binary, help and spec, held by a test | S |
| 3 | internal/update/cli.go:146 | the usage line lists `release <cut\|build\|install\|adopt\|pull>`, five verbs; `help release` prints a sixth, `cycle`, and the release refusal lists six | name cycle in the usage line, or point the line at `nova-update help release` | S |
| 4 | internal/update/cli.go:167 | the banner says "The release verbs build, publish and install"; `publish` is not a verb, and cut, adopt, pull and cycle are missing | name the six release verbs that exist | S |
| 5 | internal/update/cli.go:92 | `updateVerbNames` omits `help`, so the no-verb and unknown-verb refusals list nine verbs while `nova-update help` is a door the usage prints | add help to the refusal's verb list | S |
| 6 | internal/update/read.go:72 | a `kind=pin` row takes the second token of the latest read with no version shape: the help's own sample `local:go version` gives `STATUS EQUAL name=gp kind=pin installed=version latest=version`, exit 0, and counts the pin current | refuse a pin read whose token is not version-shaped, naming the two-token shape | M |
| 7 | internal/update/cli.go:406 | `apply` gets the same `timeout: 5s` default as a version read, while its own flag help (internal/update/cli.go:413) says "such as 5m for a slow installer"; a 6s installer printed `APPLY FAILED ... took=5.014s: timeout` with no remedy | give apply its own default and print `pass --timeout <d>` on a timeout | S |
| 8 | internal/update/adopt.go:140 | one watch pass is written to two streams: `ADOPT OK` to stdout, `ADOPT REFUSED`, `ADOPT ESCALATE` and `ADOPT DONE` to stderr, so a caller reading stdout loses the tail and the pass sha | keep every ADOPT line of a pass on one stream, or add a `--json` receipt | M |
| 9 | internal/update/cli.go:279 | the watch help says this tool files nothing, while ESCALATE (internal/update/adopt.go:148) says "duty files an issue and a fix card" | say who answers the refusal, not that something was filed | S |
| 10 | `nova-update release <verb> -h` | none of the six release verbs prints an `effect:` line, though every manifest verb's help states its effect class | add the effect line to each release verb's help | S |
| 11 | `nova-update release install -h` | the one verb that writes the live bin directory has no effect line and no `--dry-run`; `release install --dry-run` is refused as an unknown flag, so its only preview is running it | add the effect line and a `--dry-run` that prints the install plan | M |
| 12 | pkg/release/cli.go:242 | every release verb's `--version` help says "such as 1.2.0"; cut and install refuse exactly that spelling as not v-prefixed | say "such as v1.2.0" | S |
| 13 | pkg/release/install.go:352 | a success prints `RELEASE INSTALLED` while the same verb's refusals print `INSTALL REFUSED` and its failures `INSTALL FAILED`, so the verb's first token is not stable | one first token per verb, as the manifest verbs do | S |
| 14 | internal/update/cli.go:702 | with an apply that declares `{version}`, `apply --version 9.9.9` printed `APPLY STALE ... latest=9.9.9` while the source answered 1.26.6, so the asked target reads as the source's answer | print the source's latest and the asked target as separate fields | S |
| 15 | docs/SPEC-UPDATE.md:6 | "One tool, three verbs", then four bullets; :415 "its five usage lines" and :440 "Five verbs" count six release verbs | open with the verbs that exist, and count six | S |
| 16 | docs/SPEC-UPDATE.md:23 | "this estate's first tool", "The estate runs it nightly" and a release pipeline written for a coordinator and a fleet play; a reader checking their own tools has no estate | describe what the tool does now, and give the release pipeline its own page | M |
| 17 | internal/update/report.go:91 | a `report --draft` with no `--host` has the subject "versions on - at <stamp>" | leave the host out of the subject when none is given | S |
| 18 | pkg/release/cut.go:489 | `release cut --dry-run` runs the forge reads (head, check runs, tags, compare) and the spend check before it prints any plan, while its help says only "decide and print, write nothing" and never names the reads it still makes | name the reads a dry-run still makes in the help, or print the plan before the reads | M |
| 19 | internal/update/adoption.go:71 | `adoption -h` never names the eight states its ledger accepts, though the refusal on a bad state lists them all | enumerate the states in the `--file` flag help | S |
| 20 | `nova-update version -h` | the version help quotes the whole manifest exit table, describing verbs and exit meanings that `version` does not have | give version its own one-line exit table | S |

## Good, keep

Every refusal names every problem of the invocation at once, each with its line and remedy, then one `run:` line; a four-problem manifest was fixed from one answer.
The local apply loop: BEFORE, RUN with the resolved argv, AFTER re-read against the target, and `--dry-run` printing the same argv while starting nothing.
`example --out` that never overwrites and says `unchanged=true` on a rerun, so the first run works from the binary alone.
One `--json` envelope (result, facts, items, payload) on every verb that takes it.
`release install` verifying every artifact against SHA256SUMS, and its missing-artifact refusal printing the exact build command.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| READ 6.5/10 at 1.1.0 (docs/ratings/1.1.0/update-read.md) | CHANGED to 7 | the banner and every manifest verb's help now answer what the tool does, how it works and how to use it; the spec-opening and release-wall defects stand |
| USE 8/10 at 1.1.0 (docs/ratings/1.1.0/update-use.md) | DOWN to 7 | the pin green and the example apply that cannot move now stand beside the watch stream split and the FAILED spelling |
| a pin through the help's own `local:go version` reports latest=version | STILL THERE | `status --file pin.tsv --kind pin` prints installed=version latest=version, exit 0 |
| release verbs refuse the help's example version | STILL THERE | `release cut --version 1.2.0 --dry-run` refuses as not v-prefixed while `-h` says "such as 1.2.0" |
| a refused check splits a watch pass across streams | STILL THERE | `watch --adopt checks.tsv` shows only `ADOPT OK` on stdout; refused, escalate and done sit on stderr |
| `release install -h` has no effect line and no dry-run | STILL THERE | `release install --dry-run` is an unknown flag, and the help has no `effect:` line |
| adoption help never names the state vocabulary | STILL THERE | `adoption -h` names the fields and not the states; the refusal lists all eight |
| a local fake upgrade completes and rechecks | STILL THERE | `APPLY OK from=0.1.0 to=0.2.0`, AFTER installed=0.2.0, exit 0 |
