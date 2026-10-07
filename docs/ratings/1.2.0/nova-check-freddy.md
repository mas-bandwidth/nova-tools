# nova-check READ and USE rating, nova-tools 1.2.0

Rater: inception/mercury-2.5, harness opencode
Build: bd3adc9201ea0a3a86998f44a26b1cd865b91ac2
READ: 7.5/10
USE: 8/10

## Reasons
READ. I read `nova-check help` and all 15 verbs' `-h`, then `nova-check` in docs/SPEC.md and docs/SPEC-CHECK.md. The top help is honest about state and writes: it says which verbs keep state (`dogfood record`, `spelling --write`, `convergence --state`), gives a setup and an example that run as printed, and states one exit-code law for every verb. Every verb's `-h` lists its own flags with what each one wants. The first place of confusion is cmd/nova-check/main.go:95, where `dogfood gate` "exit[s] 1 with the verbs no non-author has run". Without `--require-all` it exits 0 with an unrun verb in the list (`verbs=2 by-nonauthor=1`), and only the flag's own `-h` line says so. The first place of doubting a claim is docs/SPEC-CHECK.md:1 and :13. The file is titled `nova-dev convergence`, and it says `help` prints a `nova-dev convergence` line "byte for byte", but the binary is `nova-check` and prints no such line. The first place of boredom is the verb `-h` excerpt. internal/nsprint/verbflag/verbflag.go:313 re-indents the help's right-hand column flush left, so a description reads as broken lines of run-on prose. `dogfood -h` also picks up the last line of the convergence block ("nova-check dogfood record the finding") as one of its own. Smaller costs: docs/SPEC.md:353 still says every listing takes `--fail-max` while help calls it the old spelling of `--max`; the help's `--max` paragraph leaves out `hygiene` and `dogfood`, which take `--max` too; `hygiene -h` does not mark `--identity` as required, though the verb refuses without it; `version` refuses "takes no flags and no arguments" while it takes `--json`.

USE. Used for real on a throwaway directory inside the job directory. No live store, no server and no network: `convergence` was pointed at a fake `gh` through `--gh`. I ran links, spelling, kernel, and quickstart. That covered a small self tree with broken links and a markdown file with misspellings. Every check found what was planted, and each finding named its file and line. Exit 2 against exit 1 held everywhere. Refusals named every missing flag at once with what each wants. `--json` was one object on stdout each time.

The score is held at 8 by traps that cost a turn without a word of warning. `--exclude` and `--file` are relative to `--dir`, so `links --dir ./self --exclude ./self/sub` excludes nothing (`excluded=0`), and `--file ./self/README.md` reports `self/README.md: unreadable` as a broken link. `links` and `spelling` print OK over zero files where `nocode` now prints a NOTE. A failed run puts every line, the count line too, on stderr, so stdout is empty. docs/CLI.md says so, but `help` does not. Only the first unknown flag is named. Refusals point at the 150-line global help, not `help <verb>`.

A 10 needs:
- `dogfood gate`'s help line to state the default (open edges only) and what `--require-all` adds;
- the `-h` excerpt to keep the help's column, and to stop at the verb's own block;
- docs/SPEC-CHECK.md to name `nova-check convergence`, and docs/SPEC.md to say `--max`;
- `--exclude` and `--file` to accept a path under `--dir` as given, or the help to state the base;
- a NOTE on zero files for `links` and `spelling`, and the stream contract in `help`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/main.go:95 | help says `dogfood gate` exits 1 "with the verbs no non-author has run", but without `--require-all` it exits 0 with an unrun verb | say the default gate is open edges only, and that `--require-all` adds the unrun verbs | S |
| 2 | internal/nsprint/verbflag/verbflag.go:509 | Excerpt takes the convergence block's last line as a `dogfood` line | end a verb's excerpt at the next usage entry | S |
| 3 | internal/nsprint/verbflag/verbflag.go:313 | every `-h` excerpt re-indents the help's right-hand description column flush left | keep the help's own indentation, or join a description's lines | S |
| 4 | docs/SPEC-CHECK.md:13 | the spec is titled `nova-dev convergence` and says `help` prints `nova-dev convergence` byte for byte | rename to `nova-check convergence` and quote the line help really prints | S |
| 5 | docs/SPEC.md:353 | "Every listing here takes `--fail-max <n>`" and omits `spelling` | say `--max`, name `spelling`, and give `--fail-max` as the alias | S |
| 6 | cmd/nova-check/main.go:140 | the `--max` paragraph omits `hygiene` and `dogfood` | list every verb that takes `--max`, with hygiene's meaning | S |
| 7 | cmd/nova-check/hygiene.go:41 | `hygiene -h` does not mark `--identity` required | add "(required)" to the flag's usage | S |
| 8 | cmd/nova-check/version.go:44 | `version -h` refuses "takes no flags and no arguments", but `--json` is a flag | say "takes no arguments; its one flag is --json" | S |
| 9 | cmd/nova-check/main.go:567 | `--exclude` and `--file` are relative to `--dir`; `--exclude ./self/sub` excludes nothing | accept a path that starts with `--dir` as that subtree or file | S |
| 10 | cmd/nova-check/main.go:32 | a failed run puts every line on stderr and leaves stdout empty; `help` does not say so | state the stream contract in `help` | S |
| 11 | `nova-check links --dir ./empty` | `LINKS OK files=0 links=0` and `SPELLING OK files=0` at exit 0, a green over zero files | print the NOTE from links and spelling too | S |
| 12 | `nova-check links --bogus --alsobad` | only the first unknown flag is named | collect and name every unknown flag in one refusal | S |
| 13 | `nova-check links` (every refusal) | refusals end `run: nova-check help`, the whole 150-line help | point every verb's refusal at `nova-check help <verb>` | S |
| 14 | cmd/nova-check/spelling.go:127 | `--write --dry-run` with a misspelling found prints `SPELLING OK` and exits 0; `dry_run=true` is the one underscore key | print `SPELLING FAILED` at exit 1, and spell the field `dry-run` | S |
| 15 | README.md:54 | still "These are the Nova Tools 1.0.0 commands" and installs v1.0.0 | point at the current release | S |

## Good, keep
Refusals that name every missing flag at once, each with a line on what it wants.
One exit law everywhere: 0 pass, 1 check failed, 2 could not run.
Each finding names its file and line, and the count line prints on pass and on fail.
`--json` returns one object on stdout each time.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| two cap flags, `--fail-max` and `--max` | FIXED | every listing verb's `-h` shows `--max`, and `--fail-max` as its old spelling |
| several unrelated jobs behind one name | STILL THERE | cmd/nova-check/main.go:32 still says records and repositories, beside hygiene, dogfood and convergence |
| README says 1.0.0 | STILL THERE | README.md:54 |
| green over zero files, links and spelling | STILL THERE | `nova-check links --dir ./empty` prints `LINKS OK files=0 links=0` at exit 0 |
| `--exclude` relative to `--dir`, silently | STILL THERE | `--exclude ./self/sub` prints `excluded=0`, `--exclude sub` prints `excluded=1` |
| refusals point at the whole help | STILL THERE | `nova-check links` ends `run: nova-check help` |
| only the first unknown flag named | STILL THERE | `links --bogus --alsobad` names `--bogus` only |
