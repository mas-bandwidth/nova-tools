# nova-check READ and USE rating, nova-tools 1.2.0

Rater: Grok (xAI), Grok Build
Build: ce398b2dcc7d
READ: 7.5/10
USE: 8/10

Question: is this a good tool for an AI to use?

The binary that ran prints `nova-check v1.2.0-dev.0d56536c darwin/arm64 go1.27.1`. Between that commit and this head the only change under the check packages is a test file, so the verbs below are this head's. Read cold: `nova-check help`, every verb's `-h`, `docs/SPEC.md` section `## nova-check`, and `docs/SPEC-CHECK.md`. Used on a throwaway directory. No live store, no server. `convergence` was given a fake `gh` that prints `[]`, except one `--dry-run` with no `--gh`, which called the real `gh` and is finding 13.

## Reasons

READ. The banner says what the tool is, which three verbs write, and that `--dry-run` writes none of it. The setup and the three examples run as printed: `QUICKSTART OK`, `LINKS OK`, `KERNEL OK` on a one-file tree. Every verb's `-h` lists its flags, marks the required ones, states an effect, and repeats the one exit law (0 pass, 1 check failed, 2 could not run). A missing flag names every missing flag in one run, each with a sentence about what it wants. That is the shape an AI wants.

What keeps READ at 7.5. Help says a thing the default does not do: `dogfood gate` "exits 1 with the verbs no non-author has run" (cmd/nova-check/main.go:66), but without `--require-all` it exits 0 with an unrun verb still in the list (`DOGFOOD GATE OK verbs=2 by-nonauthor=1 require-all=no`). The convergence excerpt is not the usage: internal/nsprint/verbflag/verbflag.go:526 takes any line whose first words are the verb, so `convergence -h` prints the mid-sentence `nova-check convergence --state <file> again once the source moves, or nova-check dogfood record the` (cmd/nova-check/main.go:73) as a second usage line. The exit table has the setup recipe glued on (cmd/nova-check/main.go:57), so `version -h` and `dogfood -h` print `mkdir -p ./self/docs` under the exit codes. docs/SPEC-CHECK.md:1 is titled `nova-dev convergence` and line 13 says `help` prints a `nova-dev convergence` line byte for byte; the binary is `nova-check` and prints no such line. docs/SPEC.md:353 still says every listing takes `--fail-max` and omits `spelling`, while every `-h` calls `--fail-max` the old spelling of `--max`. `dogfood gate -h` renders `--authors` as `<<tool> <verb> = <who wrote it>>` because the backticks in cmd/nova-check/dogfood.go:309 are read as a value type. Spelling's `-h` says "misspellings" (cmd/nova-check/spelling.go:25); the check is the fixed American list, so `sentance` passes and `teh` is caught. README.md:86 still says these are the 1.0.0 commands.

USE. All fourteen names in the refusal's verb list were run: quickstart, attest, links, kernel, nocode, floors, corpus, hygiene, dogfood ledger, dogfood record, dogfood gate, convergence, spelling, version. Planted defects were named by file and line: a missing link, an attest `../` escape and a non-canonical `./` entry, a corpus fragment that is gone and a ledger under its `--min-anchors` floor, a `.py`, a shebang `.sh` and a `.github/workflows/` file, a key-shaped line that hygiene reported as `pem-private-key` without printing the text, and an out-of-path file against `--paths keep.md`. `dogfood record` on a verb not in the list said `did you mean`. `--dry-run` on record wrote no receipt. With a fake `gh`, convergence printed seven stream lines and `--dry-run` left `--state` unwritten (`CONVERGENCE NOTE dry_run=true`). `--json` was one object. Exit 2 against exit 1 held on the refusals and the failed checks.

What keeps USE at 8. Paths are quietly relative to `--dir`: `--exclude ./self/sub` excludes nothing (`excluded=0`) while `--exclude sub` excludes the subtree, and `--file ./self/docs/page.md` is a broken link `self/docs/page.md: unreadable`. `links` and `spelling` print OK over zero files; `nocode` prints a NOTE for the same tree. A failed run puts every line, the count line too, on stderr, and help does not say so (docs/CLI.md:365 does). A missing flag ends `run: nova-check help`, the whole 51-line banner; an unknown flag already ends `run: nova-check <verb> -h`. `--dry-run` on convergence still runs `gh`, and when that fails the local streams are refused with it. LANDING with `--repo` set and no batch in the window is `trend=absent` and counted in `absent=`, and the reason is stuffed into `source=` with `\x20` for each blank. An empty receipt set prints `DOGFOOD-GATE FAILED` at exit 1, while `-h` calls it a refusal, and an unrun verb prints `DOGFOOD GATE FAIL`. `spelling --json` puts the absolute path in `facts.dir`; `links --json` prints `--dir` as given.

A 10 would make the gate sentence match the default, keep each `-h` excerpt to that verb's usage, take the setup recipe out of the exit table, make docs/SPEC-CHECK.md and docs/SPEC.md name what the binary prints, say in `-h` that spelling is a fixed list and that `--file` and `--exclude` are relative to `--dir`, NOTE a check over zero files, put the stream contract in help, point a missing-flag refusal at `help <verb>`, and let an empty window or a missing `gh` be a reading instead of a refusal.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/main.go:66 | help says `dogfood gate` exits 1 with the verbs no non-author has run; without `--require-all` the run is `DOGFOOD GATE OK verbs=2 by-nonauthor=1 require-all=no`, exit 0 | say the default passes open edges only, and that `--require-all` is what adds the unrun verbs | S |
| 2 | internal/nsprint/verbflag/verbflag.go:526 | `convergence -h` quotes cmd/nova-check/main.go:73, a sentence, as a usage line: `nova-check convergence --state <file> again once the source moves, or nova-check dogfood record the` | end the excerpt at the usage block, not at the next line whose first words are the verb | S |
| 3 | cmd/nova-check/main.go:57 | the exit table string includes the setup recipe, so `version -h` and `dogfood -h` print `mkdir -p ./self/docs` under the exit codes | keep the setup in the banner only | S |
| 4 | docs/SPEC-CHECK.md:13 | the file is titled `nova-dev convergence` and says `help` prints `nova-dev convergence --repo ...` byte for byte; the binary is `nova-check` and prints no such line | rename it to `nova-check convergence` and quote the line help prints | S |
| 5 | docs/SPEC.md:353 | "Every listing here takes `--fail-max`" and the list omits `spelling`; every `-h` calls `--fail-max` the old spelling of `--max` | say `--max`, name `spelling`, and keep `--fail-max` as the alias | S |
| 6 | cmd/nova-check/dogfood.go:309 | `--authors` usage is wrapped in backticks, so `dogfood gate -h` prints the value type as `<<tool> <verb> = <who wrote it>>` | drop the backticks; the value is a file, and the line format belongs in the description | S |
| 7 | cmd/nova-check/spelling.go:25 | `-h` says "misspellings"; `sentance` is not reported and `teh` is. The spec heading already says known misspellings, American list | say "known common misspellings, American list" in the detail line | S |
| 8 | README.md:86 | "These are the Nova Tools 1.0.0 commands" and the install line names the v1.0.0 tag, at a 1.2.0 candidate | name the version the tree ships | S |
| 9 | internal/check/links.go:130 | `--file` and `--exclude` are joined to `--dir`. `--file ./self/docs/page.md` is `unreadable`; `--exclude ./self/sub` prints `excluded=0` while `--exclude sub` prints `excluded=1` | accept a path that starts with `--dir`, or say in `-h` that the prefix is inside `--dir` | S |
| 10 | `nova-check links --dir ./self` | a failed run writes the count line and the findings only on stderr; stdout is empty. docs/CLI.md:365 says so, help does not | state the stream contract in `help` | S |
| 11 | `nova-check links --dir ./empty` | `LINKS OK files=0` and `SPELLING OK files=0` at exit 0. `nocode` on the same tree adds `NOCODE NOTE classified NOTHING` | print that NOTE from links and spelling | S |
| 12 | `nova-check links` | a missing flag ends `run: nova-check help` (51 lines). An unknown flag already ends `run: nova-check links -h`, and both unknown flags are named | point the missing-flag refusal at `nova-check links -h` too | S |
| 13 | `nova-check convergence ... --dry-run` | `--dry-run` still runs `gh pr list`. With no `--gh` the real `gh` is called, and its failure refuses the run, so the local streams are not printed | on `--dry-run`, or when `gh` cannot start, mark LANDING and PRS absent and print the rest | M |
| 14 | internal/converge/streams.go:78 | `--repo` was given and the window had no batch: LANDING is `trend=absent`, listed in `absent=`, and the reason is `source=--repo\x20(no\x20integration\x20...)`. docs/SPEC-CHECK.md:41 keeps absent for a source that was not named | print `batches=0` with a readable reason, and reserve absent for an unnamed source | S |
| 15 | cmd/nova-check/dogfood.go:338 | `--allow-empty` says no receipts "is a refusal". The run prints `DOGFOOD-GATE FAILED:` at exit 1. An unrun verb prints `DOGFOOD GATE FAIL`, a third shape | use the REFUSED form at exit 2, or say exit 1 and one FAILED token | S |
| 16 | cmd/nova-check/spelling.go:65 | `spelling --json` sets `facts.dir` to the absolute path; `links --json` prints `--dir` as given | print `--dir` as given | S |
| 17 | cmd/nova-check/spelling.go:139 | `--write --dry-run` with two misspellings prints `SPELLING OK ... dry_run=true` and exits 0. `-h` does not say a planned write exits 0, and `dry_run` is the one underscore key | say the exit in `-h`, and spell the field `dry-run` | S |
| 18 | `nova-check links --file docs/anchor.md` | `[text](SEED-CORE.md#not-a-heading)` is `LINKS OK`. The spec strips the fragment; `links -h` does not say so | say in `links -h` that a `#fragment` is not checked | S |
| 19 | cmd/nova-check/main.go:55 | the banner still says checks over markdown records and repositories, beside hygiene, dogfood and convergence | name those three in the banner's first line | S |

## Good, keep

The setup block runs as printed, and the first failure of a check names the file and the line. Refusals name every missing flag at once, each with what to put there, and exit 2 stays distinct from a failed check's exit 1. `attest` refuses a `../` escape and a non-canonical entry. `hygiene` reports a key's shape and does not print the key, and names an out-of-path file against the glob it was given. `dogfood record` answers a verb that is not in the list with `did you mean`, and `--dry-run` writes nothing. `nocode` says when it classified nothing. With a fake `gh`, convergence marks an unnamed source absent and a `--dry-run` does not write `--state`. Unknown flags are all named, and `--json` still holds when it is written after one of them. On one merged stream, quickstart prints the per-check lines before `QUICKSTART FAILED`.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| two cap flags, `--fail-max` and `--max` (1.1.0 check-read) | CHANGED | every listing `-h` shows `--max`, and `--fail-max` as its old spelling; docs/SPEC.md:353 still says `--fail-max` |
| several unrelated jobs behind one name (1.1.0 check-read) | STILL THERE | cmd/nova-check/main.go:55 still says records and repositories |
| README says 1.0.0 (1.1.0 check-read) | STILL THERE | README.md:86 |
| green over zero files, links and spelling (1.1.0 check-use) | STILL THERE | `nova-check links --dir ./empty` prints `LINKS OK files=0 links=0 excluded=0 broken=0` at exit 0; nocode now adds a NOTE |
| `--exclude` relative to `--dir`, silently (1.1.0 check-use) | STILL THERE | `--exclude ./self/sub` prints `excluded=0`; `--exclude sub` prints `excluded=1` |
| dry-run convergence still runs gh (1.1.0 check-use) | STILL THERE | `--dry-run` with no `--gh` refuses with `gh pr list --state open` |
| stream contract not in help (1.1.0 check-use) | STILL THERE | help is silent; docs/CLI.md:365 states it |
| only the first unknown flag named (1.1.0 check-use) | FIXED | `links --bogus --alsobad` names `--bogus` and `--alsobad`, two lines |
| refusals point at the whole help (1.1.0 check-use) | CHANGED | an unknown flag ends `run: nova-check links -h`; a missing `--dir` still ends `run: nova-check help` |
| quickstart summary before its detail (1.1.0 check-use) | FIXED | with both streams on one descriptor, the LINKS and NOCODE lines print before `QUICKSTART FAILED` |
