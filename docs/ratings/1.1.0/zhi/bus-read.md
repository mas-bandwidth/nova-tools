# nova-bus READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 2c02b2aa2042
Score: 7/10
README: 8/10

## Reasons

The first place I was confused: docs/SPEC-BUS.md:7 says "specified, not implemented; no code" and then names verbs (`wait --on-note`, `receipt --verdict`) the banner never lists, while the live spec is the nova-bus section of docs/SPEC.md; three SPEC-BUS*.md files and SPEC.md split one tool's contract, and a cold reader has to work out which is normative. The first place I was bored: cmd/nova-bus/main.go:1618, `inboxListing`, a 467-line function inside a 3823-line main.go that holds the dispatch and most of the ten verbs. The first claim I doubted: docs/CLI.md:420 says the tool "pushes with fetch, rebase and retry so no rejected push ever reaches a person", but the retry is bounded (`--attempts`, default 25) and a send that exhausts it exits 1, so the prose promises more than the bound delivers.

Everything else is strong. The tool says what it is in one line — README.md:24 and the banner's first line (cmd/nova-bus/main.go:61) both read "notes between AIs, over a git repository" — and the code does that: `run` is the entry point, `busVerbs` names the ten verbs, and the data (a participants.json roster, one from-<slug> lane per sender, one markdown note per file, a per-reader CURSOR) is explained in the banner in present tense. The names a stranger understands on first read: lane, note, cursor, receipt, roster, thread. The "why" comments are present-tense and teach the contract — cmd/nova-bus/main.go:71-74 states the exit table, and cmd/nova-bus/version.go:1-20 explains why the version is read from the build rather than maintained. The pkg/bus package is one file per idea (address, cursor, git, lock, note, repair, since). The tests are extensive and functional, one file per verb, and they execute the exact first-run transcript in docs/CLI.md.

What keeps it from a 10: the code does not build on the shared pkg/tool skeleton the sibling binaries use — it hand-rolls dispatch, flags and refusal printing over oneline and verbflag, so it is only a half-member of the family; main.go is a 3823-line monolith where the earlier tree kept one file per verb; retired flags (`--beat`, `--beat-lease`) are still declared and ignored and `--quiet-beats` "changes nothing"; and a `REFUSED` status word is printed at exit 1 (cmd/nova-bus/main.go:698 and :1685) where the siblings and SPEC-BUS.md's own grammar say a refusal is exit 2.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-bus/main.go:1618 | `inboxListing` is 467 lines, the whole read half of one verb in one function | split it into named steps at its existing boundaries (scope, cursor check, page, open list) | L |
| 2 | cmd/nova-bus/main.go:1 | main.go is 3823 lines holding the dispatch and most verbs; not one-file-one-thing | move each verb to its own file as draft.go and reply.go already are, or adopt pkg/tool | L |
| 3 | cmd/nova-bus/main.go:698 | `REFUSED` is printed at exit 1 (lock held, stale cursor) where siblings and SPEC-BUS.md say a refusal is exit 2 | return 2 for these refusals, or rename the status word to match the exit code | S |
| 4 | cmd/nova-bus/main.go:2639 | retired `--beat` and `--beat-lease` are still declared and ignored, and `--quiet-beats` "changes nothing" | delete the retired flags and their defaults now that callers have moved off them | S |
| 5 | docs/SPEC-BUS.md:7 | a second spec says "specified, not implemented" yet names verbs the banner lacks, splitting the contract across four files | mark SPEC-BUS.md as a proposal and point to the SPEC.md nova-bus section as normative | S |

## Good, keep

The present-tense "why" comments that name the failure each verb closes and the data it reads — they are what make a cold read fast. The one-line identity shared by README and banner. The functional tests that execute the first-run transcript exactly as written.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 467-line function | STILL THERE | cmd/nova-bus/main.go:1618 |
| 16-parameter calls | CHANGED | cmd/nova-bus/main.go:2171 `advanceCursorTo` now takes 13 parameters |
| war-story comments | STILL THERE | cmd/nova-bus/main.go:1 |
| REFUSED at exit 1 | STILL THERE | cmd/nova-bus/main.go:698 |
| retired flags still declared | STILL THERE | cmd/nova-bus/main.go:2639 |
| the prose promises more than bounded retries deliver | STILL THERE | docs/CLI.md:420 |
