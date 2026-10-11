# nova-check READ and USE rating, nova-tools 1.2.0

Rater: claude-opus-5-5 (Claude Code)
Build: 8699d1ab235f
READ: 7.5/10
USE: 8/10

## Reasons
READ. `nova-check help` and all twelve verbs' `-h` were read cold, then `## nova-check` in docs/SPEC.md and docs/SPEC-CHECK.md. The top help is honest about state and writes: it says which verbs keep state (`dogfood record`, `spelling --write`, `convergence --state`), gives a setup and an example that run as printed, and states one exit-code law for every verb. Every verb's `-h` lists its own flags with what each one wants. The first place of confusion is cmd/nova-check/main.go:95, where `dogfood gate` "exit[s] 1 with the verbs no non-author has run". Without `--require-all` it exits 0 with an unrun verb in the list (`verbs=2 by-nonauthor=1`), and only the flag's own `-h` line says so. The first place of doubting a claim is docs/SPEC-CHECK.md:1 and :13. The file is titled `nova-dev convergence`, and it says `help` prints a `nova-dev convergence` line "byte for byte", but the binary is `nova-check` and prints no such line. The first place of boredom is the verb `-h` excerpt. pkg/nsprint/verbflag/verbflag.go:313 re-indents the help's right-hand column flush left, so a description reads as broken lines of run-on prose. `dogfood -h` also picks up the last line of the convergence block ("nova-check dogfood record the finding") as one of its own. Smaller costs: docs/SPEC.md:353 still says every listing takes `--fail-max` while help calls it the old spelling of `--max`; the help's `--max` paragraph leaves out `hygiene` and `dogfood`, which take `--max` too; `hygiene -h` does not mark `--identity` as required, though the verb refuses without it; `version` refuses "takes no flags and no arguments" while it takes `--json`.

USE. Used for real on a Linux bench in a throwaway directory inside the job directory. No live store, no server and no network: `convergence` was pointed at a fake `gh` through `--gh`. All twelve verbs were run. That covered a small self tree with two broken links, an anchor link, a shebang file, a `.py` file and a `.github/workflows/` file; an empty directory; an attest manifest with a missing entry and a `../` escape; a corpus ledger with one lost fragment and a raised floor; spelling with, `--write --dry-run` and `--write`; a throwaway git repo for `hygiene` (wrong identity, out-of-path, an AWS-shaped key) and `nocode --staged` (a staged `.sh`, then a non-root `--dir`); and dogfood `ledger`, `record` (with `--dry-run`, then real, then a verb not in the list) and `gate`, with and without `--allow-empty`. Every check found what was planted, and each finding named its file and line. Exit 2 against exit 1 held everywhere. Refusals named every missing flag at once with what each wants (`attest`, `corpus`, five at once on `convergence`). `attest` caught the `../` escape. `hygiene` reported the key without printing it. `dogfood record` answered a wrong verb with "did you mean". `--json` was one object on stdout each time.

The score is held at 8 by traps that cost a turn without a word of warning. `--exclude` and `--file` are relative to `--dir`, so `links --dir ./self --exclude ./self/sub` excludes nothing (`excluded=0`), and `--file ./self/README.md` reports `self/README.md: unreadable` as a broken link. `links` and `spelling` print OK over zero files where `nocode` now prints a NOTE. A failed run puts every line, the count line too, on stderr, so stdout is empty. docs/CLI.md says so, but `help` does not. Only the first unknown flag is named. Refusals point at the 150-line global help, not `help <verb>`. Under `--dry-run`, `convergence` still runs `gh` and refuses when it cannot. LANDING prints `trend=absent` with `source=--repo` when `--repo` was given and the window just had no batch. docs/SPEC-CHECK.md:41 keeps absent for a source that was not named.

A 10 needs:
- `dogfood gate`'s help line to state the default (open edges only) and what `--require-all` adds;
- the `-h` excerpt to keep the help's column, and to stop at the verb's own block;
- docs/SPEC-CHECK.md to name `nova-check convergence`, and docs/SPEC.md to say `--max`;
- `--exclude` and `--file` to accept a path under `--dir` as given, or the help to state the base;
- a NOTE on zero files for `links` and `spelling`, and the stream contract in `help`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/main.go:95 | help says `dogfood gate` exits 1 "with the verbs no non-author has run", but without `--require-all` it exits 0 with an unrun verb (`DOGFOOD GATE OK verbs=2 by-nonauthor=1 ... require-all=no`) | say the default gate is open edges only, and that `--require-all` adds the unrun verbs | S |
| 2 | pkg/nsprint/verbflag/verbflag.go:509 | Excerpt takes the convergence block's last line, `nova-check dogfood record the finding`, as a `dogfood` line, so `dogfood -h` prints a fragment from another verb | end a verb's excerpt at the next usage entry, not at the next line starting with the verb | S |
| 3 | pkg/nsprint/verbflag/verbflag.go:313 | every `-h` excerpt re-indents the help's right-hand description column flush left, so each description reads as short broken lines under the usage | keep the help's own indentation, or join a description's lines into one | S |
| 4 | docs/SPEC-CHECK.md:13 | the spec is titled `nova-dev convergence` and says `help` prints `nova-dev convergence --repo ...` byte for byte; the binary is `nova-check` and prints no such line | rename to `nova-check convergence` and quote the line help really prints | S |
| 5 | docs/SPEC.md:353 | "Every listing here takes `--fail-max <n>`" and omits `spelling`; help and every `-h` call `--fail-max` the old spelling of `--max` | say `--max`, name `spelling`, and give `--fail-max` as the alias | S |
| 6 | cmd/nova-check/main.go:140 | the `--max` paragraph names quickstart, attest, links, nocode, corpus and spelling, but `hygiene` and `dogfood` take `--max` too, and hygiene's means "finding lines to print" | list every verb that takes `--max`, with hygiene's meaning | S |
| 7 | cmd/nova-check/hygiene.go:41 | `hygiene -h` does not mark `--identity` required, though the verb refuses without it and `--repo`, `--base`, `--head` are marked | add "(required)" to the flag's usage | S |
| 8 | cmd/nova-check/version.go:44 | `version extra` refuses "takes no flags and no arguments", but `--json` is a flag of version | say "takes no arguments; its one flag is --json" | S |
| 9 | cmd/nova-check/main.go:567 | `--exclude` (and `links --file`) are relative to `--dir`; `--exclude ./self/sub` excludes nothing (`excluded=0`) and `--file ./self/README.md` is reported as a broken link `self/README.md: unreadable` | accept a path that starts with `--dir` as that subtree or file, or refuse a prefix that matches nothing | S |
| 10 | cmd/nova-check/main.go:32 | a failed run puts every line, the count line too, on stderr and leaves stdout empty; docs/CLI.md:45 says so, `help` does not | state the stream contract in `help`, and print the closing count line on stdout | S |
| 11 | `nova-check links --dir ./empty` | `LINKS OK files=0 links=0` and `SPELLING OK files=0` at exit 0, a green over zero files; `nocode` prints a NOTE for the same tree | print the NOTE from links and spelling too | S |
| 12 | `nova-check links --bogus --alsobad` | only the first unknown flag is named | collect and name every unknown flag in one refusal | S |
| 13 | `nova-check links` (every refusal) | refusals end `run: nova-check help`, the whole 150-line help; `dogfood record --closes` already points at `nova-check dogfood record -h` | point every verb's refusal at `nova-check help <verb>` | S |
| 14 | `nova-check convergence ... --dry-run` | `--dry-run` still runs `gh pr list`, and with no gh it refuses, so an offline run cannot take the reading | with `--dry-run`, or when gh is missing, mark LANDING and PRS absent instead | M |
| 15 | internal/converge/streams.go:78 | LANDING prints `trend=absent source=--repo` and lands in `absent=` when `--repo` was given and the window merely held no batch; docs/SPEC-CHECK.md:41 keeps absent for a source not named | print `now=0` with `batches=0`, or a separate `trend=empty`, and keep absent for an unnamed source | S |
| 16 | cmd/nova-check/dogfood.go:330 | `--allow-empty -h` says no receipts "is a refusal", but the run prints `nova-check dogfood gate FAIL:` at exit 1, in neither the refusal form nor the `DOGFOOD GATE FAILED` form | make it exit 2 with the REFUSED form, or say exit 1 and use the FAILED line | S |
| 17 | cmd/nova-check/dogfood.go:260 | `dogfood gate -h` renders the `--authors` value type as `<<tool> <verb> = <who wrote it>>` | name the value `<file>` and keep the line format in the description | S |
| 18 | cmd/nova-check/main.go:131 | help says "check markdown or prose for misspellings", but the check is a fixed list of common American misspellings: `sentance` passes, `teh` is caught | say "known common misspellings (American list)" in help, as the spec's heading does | S |
| 19 | cmd/nova-check/spelling.go:127 | `--write --dry-run` with a misspelling found prints `SPELLING OK ... misspellings=1` and exits 0; `dry_run=true` is the one underscore key among hyphenated ones | print `SPELLING FAILED` at exit 1 (nothing was fixed), and spell the field `dry-run` | S |
| 20 | `nova-check spelling --dir ./self --json` | `facts.dir` is absolute in spelling and as given in links | print `--dir` as given in every verb | S |
| 21 | README.md:54 | still "These are the Nova Tools 1.0.0 commands" and installs v1.0.0 | point at the current release | S |

## Good, keep
Refusals that name every missing flag at once, each with a line on what it wants and why there is no default (`corpus --min-anchors`, `kernel` divisor, `convergence --since`).
One exit law everywhere: 0 pass, 1 check failed, 2 could not run.
Each finding names its file and line, and the count line prints on pass and on fail, with the MORE line naming the flag that lifts the cap.
`attest` refusing a `../` escape, and `hygiene` reporting a key's shape without printing the key.
`dogfood record` answering a verb not in the list with "did you mean", and `--dry-run` that writes nothing.
`convergence` marking an unnamed source ABSENT with the flag that would feed it, never zero.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| two cap flags, `--fail-max` and `--max` (1.1.0 check-read) | FIXED | every listing verb's `-h` shows `--max`, and `--fail-max` as its old spelling; docs/SPEC.md:353 still says `--fail-max` |
| several unrelated jobs behind one name (1.1.0 check-read) | STILL THERE | cmd/nova-check/main.go:32 still says records and repositories, beside hygiene, dogfood and convergence |
| README says 1.0.0 (1.1.0 check-read) | STILL THERE | README.md:54 |
| green over zero files, links and spelling (1.1.0 check-use) | STILL THERE | `nova-check links --dir ./empty` prints `LINKS OK files=0 links=0 excluded=0` at exit 0 |
| `--exclude` relative to `--dir`, silently (1.1.0 check-use) | STILL THERE | `--exclude ./self/sub` prints `excluded=0`, `--exclude sub` prints `excluded=1` |
| dry-run convergence still runs gh (1.1.0 check-use) | STILL THERE | `--dry-run --gh /nonexistent/gh` refuses with `gh pr list --state open: fork/exec ...` |
| stream contract not in help (1.1.0 check-use) | STILL THERE | help is silent; docs/CLI.md:45 states it |
| refusals point at the whole help (1.1.0 check-use) | STILL THERE | `nova-check links` ends `run: nova-check help` |
| only the first unknown flag named (1.1.0 check-use) | STILL THERE | `links --bogus --alsobad` names `--bogus` only |
| quickstart summary before its detail (1.1.0 check-use) | FIXED | with both streams on one descriptor, every LINKS and NOCODE line prints before `QUICKSTART FAILED` |
