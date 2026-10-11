# nova-check READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: 193a6f8002d0
READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-check version` prints `nova-check v1.0.1-0.20261006150140-193a6f8002d0 linux/amd64 go1.26.6`. Built and run on a Linux bench machine, in a scratch directory made for the trial: a small markdown tree, a throwaway git repository, a receipts directory and a fake `gh` that answers `[]`. No live store, no server, no network. Every verb was run, its refusals included; convergence ran only against the fake forge.

## Reasons

READ. The banner says what the tool is in one line, then which verbs write and which read, then a first run that needs only `mkdir` and `printf`. Every verb answers `-h` and `help <verb>` with a banner excerpt, its flags, an `effect:` line and the exit codes, and the effect lines are true of the binary: spelling and dogfood record write only without `--dry-run`, convergence writes only `--state`. The `--max` and `--json` paragraphs say which verbs take them, and the per-verb contract in docs/SPEC.md (Asserts, Says NO, Refuses, Deliberately does not check) is still the clearest in the repository.

What keeps READ at 7. docs/SPEC-CHECK.md is titled `nova-dev convergence` and says `help` prints `nova-dev convergence ...` byte for byte (docs/SPEC-CHECK.md:1, docs/SPEC-CHECK.md:13); no `nova-dev` binary exists. docs/SPEC.md:353 says every listing takes `--fail-max`, which `-h` now calls the old spelling of `--max`. The `-h` excerpts lose the banner's alignment, so the description column runs into the usage lines, and `dogfood -h` and `dogfood record -h` carry a stray `nova-check dogfood record the finding`, a fragment of convergence's text (cmd/nova-check/main.go:126). `hygiene -h` does not mark `--identity` required and the verb refuses without it; it does not list the eleven `--kind` values it accepts. `dogfood ledger -h` prints `--authors <<tool> <verb> = <who wrote it>>`, and most file and directory flags print `<string>` or `<value>`. convergence's banner line omits `--gh`, `--git`, `--now` and `--timeout`. The newcomer path still meets kernel, floors, corpus, door and seed with no definition (docs/CLI.md:13).

USE. The first run is clean and the QUICKSTART line names the next verbs and what each wants. links, nocode, kernel, attest, corpus, spelling and hygiene each found what was planted and named it by file and line: seven broken links capped to two with a MORE line and a full count; the `.py`, the Makefile and the workflow by their two floors; a 187-byte file against a 10-byte budget; a manifest entry that escapes `--home`; a ledger row whose words were removed; three misspellings with code spans left alone; and on a branch, the out-of-path files, a stray `*.orig`, a foreign author and an AWS-shaped key without printing it. Every missing required flag is named in one run with what it wants, an empty manifest and an empty receipt set are refusals, not greens, and `spelling --write --dry-run` now previews and writes nothing. The dogfood loop works end to end: record, ledger, a gate that fails on an open edge and names `--closes <id>`, then passes once it is closed.

What keeps USE at 7. Things that should not be green are: `links` and `spelling` on an empty directory, `spelling --path` on a glob that matches nothing, and `hygiene` over an empty range all exit 0. `dogfood record --closes deadbeef` writes a receipt closing an edge that never existed. Every finding and FAILED line goes to stderr and stdout is empty on failure. Refusals point at the whole banner (`run: nova-check help`, cmd/nova-check/main.go:237), not `help <verb>`, and two unknown flags name only the first. `convergence --dry-run` still calls `gh` before anything else, and takes `--repo ./g` though the help says it is never a directory. spelling's columns are 0-based (`S.md:1:0`) and its paths are absolute where links echoes them as given. floors prints no count line and calls missing input files FAILED (exit 1) where attest calls a missing manifest REFUSED (exit 2). With `--authors`, a verb the file does not name is reported as `only the author has run it`. `--exclude ./self/sub` still excludes nothing without a word.

A 10 would fix the spec's tool name and `--fail-max` claim, make every `-h` mark what is required and print a word for every file flag, send findings to stdout, point each refusal at `help <verb>` and name every bad flag, refuse a green over nothing, refuse a `--closes` id no receipt carries, and give one rule for a missing input file across verbs.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-CHECK.md:1 | Titled `nova-dev convergence`, and line 13 says `help` prints `nova-dev convergence --repo ...` byte for byte; the verb is `nova-check convergence` and no nova-dev binary exists. | Retitle to `nova-check convergence` and quote the line the banner prints. | S |
| 2 | docs/SPEC.md:353 | "Every listing here takes `--fail-max <n>`" and lists five verbs; `-h` calls `--fail-max` the old spelling of `--max`, and spelling, hygiene and dogfood also take `--max`. | Name `--max`, list every verb that takes it, and say `--fail-max` is the old spelling. | S |
| 3 | cmd/nova-check/main.go:126 | `dogfood -h` and `dogfood record -h` end with `nova-check dogfood record the finding`, a fragment of convergence's banner text matched by verb name. | Excerpt the banner by usage block, not by matching the verb name. | S |
| 4 | `nova-check links -h` (every verb) | The excerpt strips the banner's alignment, so descriptions such as `every relative md link resolves;` read as more usage lines. | Keep the banner's indentation in the excerpt. | S |
| 5 | cmd/nova-check/hygiene.go:41 | `hygiene -h` does not mark `--identity` required; without it the verb refuses `--identity is required`. `--kind`'s eleven values are only in the refusal. | Add `(required)` to `--identity` and list the kinds in `--kind`'s usage. | S |
| 6 | cmd/nova-check/dogfood.go:260 | `dogfood ledger -h` and `gate -h` print `--authors <<tool> <verb> = <who wrote it>>`; `--dir`, `--file`, `--exclude`, `--ledger` and others print `<string>` or `<value>`. | Backtick one word (`file`, `dir`, `prefix`) in each flag's usage text. | S |
| 7 | `nova-check help` | convergence's usage omits `--gh`, `--git`, `--now` and `--timeout`; dogfood ledger's omits `--tools`, `--git-timeout` and `--tools-timeout`. | Generate the banner's usage lines from the flag sets, or add `[flags in -h]`. | S |
| 8 | `nova-check links --dir ./self --max 1 2>/dev/null` | Prints nothing: every FAILED, MORE and summary line goes to stderr, OK lines to stdout. Carried from 1.1.0. | Print findings and the summary on stdout; keep stderr for refusals. | M |
| 9 | cmd/nova-check/main.go:237 | Every refusal ends `run: nova-check help`, the 133-line banner, not `nova-check help <verb>`. Carried from 1.1.0. | Print `run: nova-check help <verb>` when the verb is known. | S |
| 10 | `nova-check links --bogus --alsobad` | Names only `--bogus`. Carried from 1.1.0. | Collect every unknown flag before refusing. | S |
| 11 | `nova-check links --dir e`, `spelling --dir e`, `spelling --path 'nomatch/*.md'` | `LINKS OK files=0` and `SPELLING OK files=0`, exit 0, over nothing; nocode prints a NOTE for the same tree. Carried from 1.1.0. | Print the NOTE nocode prints, or exit 1 when nothing was classified. | S |
| 12 | `nova-check hygiene --repo g --base HEAD --head HEAD --identity ...` | `HYGIENE OK ... findings=0` over an empty range; the line does not say how many commits it read. | Add `commits=<n>` and refuse or NOTE on zero. | S |
| 13 | `nova-check dogfood record ... --closes deadbeef` | Writes a receipt closing an edge id no receipt carries, exit 0. | Refuse a `--closes` id the receipts do not hold, naming the open ids. | S |
| 14 | `nova-check dogfood gate --authors auth.txt` | With an authors file naming only links, kernel is failed as `only the author has run it`; its author is unknown, not the runner. | Say `author unknown` for a verb the file does not name, or refuse an authors file that misses a verb. | S |
| 15 | cmd/nova-check/dogfood.go:330 | The empty-receipts failure prints `nova-check dogfood gate FAIL: ...`, outside the `DOGFOOD GATE FAIL` grammar of every other gate line. | Print `DOGFOOD GATE FAIL receipts=0 ...` with the remedy. | S |
| 16 | `nova-check convergence ... --gh /bin/false --dry-run` | Refuses on `gh pr list` before any reading, so an offline dry run is impossible. Carried from 1.1.0. | With `--dry-run`, mark the forge streams ABSENT instead of reading them. | M |
| 17 | `nova-check convergence --repo ./g ...` | Accepted and passed to gh; the help says `--repo` is a name on a forge, never a directory. | Refuse a `--repo` that is not `owner/name`. | S |
| 18 | `nova-check convergence --ledger ledger.md` (a table with no result column) | Read anyway; the header row counts as an open row (`rows=3 open=3` for two data rows). | Refuse a ledger whose table has no result column, naming the header it wanted. | S |
| 19 | cmd/nova-check/spelling.go:143 | Columns print 0-based (`S.md:1:0: teh -> the`); links, editors and compilers count from 1. | Print `Column+1`, and say 1-based in `-h`. | S |
| 20 | `nova-check spelling --dir self --file self/S.md` | `--file` is resolved under `--dir` (refuses `self/self/S.md`), but alone it is relative to the working directory; `-h` says neither. Refusals and `--json` print absolute paths where links prints them as given. | Say the base in `-h`, and echo paths as given. | S |
| 21 | `nova-check floors --core nope --source nope2` | Missing files are `FLOORS FAILED ... does not exist`, exit 1, and floors prints no count line; attest calls a missing manifest REFUSED, exit 2. | Pick one rule for a missing named input across verbs, and add a `FLOORS FAILED failed=<n>` line. | S |
| 22 | `nova-check links --dir ./self --exclude ./self/sub` | `excluded=0`, the subtree still scanned; the prefix is relative to `--dir`. Carried from 1.1.0. | Say the base in `--exclude`'s usage, or accept a path that starts with `--dir`. | S |
| 23 | `nova-check nocode --dir ./self --deny-ext ''` | An empty replacement list is silently ignored and the floor list stays in force. | Refuse an empty `--deny-ext`. | S |
| 24 | `nova-check kernel --file f --max-bytes 10 --max-tokens 5 --bytes-per-token 4` | Two REFUSED lines, each followed by the same three-line `state the unit` paragraph. | Print the remedy paragraph once per run. | S |
| 25 | `nova-check quickstart --dir ./self --json`, `dogfood ledger --json` | Unknown flag; the banner lists the exception, but the first-run verb and the receipt verbs are the ones a script reads. Carried from 1.1.0. | Accept `--json` on quickstart and dogfood. | M |
| 26 | docs/CLI.md:13 | kernel, floors, corpus, door and seed meet the newcomer with no definition. Carried from 1.1.0. | Open the nova-check section with a short glossary. | S |
| 27 | cmd/nova-check/main.go | 1,008 lines that hand-roll dispatch, help excerpts and refusals, up from 877 at 1.1.0; findings 3, 4, 9 and 10 live there. | Move dispatch and help onto pkg/tool. | L |

## Good, keep

The effect lines are true: nothing that says it writes nothing wrote anything in the trial, and `--dry-run` on spelling, dogfood record and convergence writes nothing. Missing required flags are named all at once, each with a line on what it wants and why it is never guessed. kernel, corpus and attest refuse the quiet greens (a missing kernel is the worst over-budget, an empty manifest attests nothing, a shrinking ledger is red). The `--max` cap with a MORE line and a count line on failure, and `--json` as one object with `more {shown, total}`, are right for an AI. hygiene's secret finding names the shape and never prints the text. The dogfood gate's failure line names the receipt, who can close it and the exact `--closes <id>`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| spelling --write had no dry-run | FIXED | `spelling --file s2.md --write --dry-run` prints three `SPELLING FIX` lines and `written=0 dry_run=true`; the file is unchanged |
| two refusals name one problem | FIXED | `nova-check convergence` with no flags names all five missing flags, each with its reason |
| green over zero files (links, spelling) | STILL THERE | `nova-check links --dir e` prints `LINKS OK files=0 links=0 excluded=0`, exit 0 |
| every remedy is the whole help | STILL THERE | `nova-check links REFUSED: unknown flag --frob; ...; run: nova-check help` |
| only the first unknown flag is named | STILL THERE | `links --bogus --alsobad` names `--bogus` only |
| findings on stderr, OK on stdout | STILL THERE | `links --dir ./self --max 1 2>/dev/null` prints nothing |
| --exclude relative to --dir, silently | STILL THERE | `--exclude ./self/sub` gives `excluded=0` |
| convergence --dry-run reads the forge | STILL THERE | `--gh /bin/false --dry-run` refuses `gh pr list --state open: exit status 1` |
| quickstart and dogfood refuse --json | STILL THERE | `quickstart --dir ./self --json`: `unknown flag --json` |
| spelling paths absolute, links as given | STILL THERE | `spelling --dir self --json` facts.dir is absolute; links echoes `./self` |
| spec claims help prints the convergence line byte for byte | WORSE | docs/SPEC-CHECK.md:13 now names a `nova-dev` binary that does not exist |
| a private vocabulary on the first-run path | STILL THERE | docs/CLI.md:13 uses kernel with no definition |
| an 877-line hand-rolled main.go | WORSE | cmd/nova-check/main.go is 1,008 lines |
