# nova-check READ rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8/10
README: 8.5/10

## Reasons

The README earns its mark alone: one line per tool, a pasteable first run for
each, an honest "it may not help if" limit, and a clear "one tool is a fine
number" posture. The usage guide keeps that honesty end to end, and the
command reference opens nova-check with a real `First run` transcript.

The first place I was confused: docs/CLI.md:13 names a `kernel` budget and
docs/CLI.md:17 a `floors` check before either word is defined; the newcomer
path (README, then usage guide, then command reference) never explains the
self-repo nouns the tool's own spec assumes. That vocabulary is the biggest
cost to the score: a stranger who does not already run the seed-and-door
workflow reads the verbs but not the point of them.

The first place I was bored: AGENTS.md:23 opens a long "before any design"
paragraph of abstract principles before any tool-specific prose, and the same
material is restated in docs/SPEC.md's conventions.

The first claim I doubted: docs/SPEC-CHECK.md:10 says `help` prints the
convergence line "byte for byte", but the banner I read wraps that synopsis
and lists optional flags; the USE rating checks the real output.

The code does what the docs say. Each verb states Asserts, Says NO, Refuses
and Deliberately does not check, and cmd/nova-check/main.go carries the same
contract in its comments. The no-guessing law (every path from a flag, every
missing flag named in one run), the one-line output grammar, the `--fail-max`
cap with a MORE line, and the count line printed on failure too are all
present and cite the spec. Names are mostly plain (links, attest, kernel,
nocode, spelling), but the self-repo cluster (floors, corpus, door, seed)
stays private. One family with the other tools is only partly true: it shares
pkg/bounded and pkg/oneline and the refusal shape, but it hand-rolls
its dispatch and help in an 877-line main.go instead of the pkg/tool
skeleton the standard names, which is the main weight. The tests are thorough
and teach the contract, each spec section naming the red tests that pin it.

A 10 would need the self-repo nouns defined on the first-run path, the eleven
verbs either explained as one tool or split, and the hand-rolled dispatch
moved onto pkg/tool.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:13 | the newcomer meets kernel, floors, corpus, door and seed with no definition on the first-run path | open the command reference section with a three-line glossary of the self-repo nouns | S |
| 2 | README.md:34 | the row says "broken links and other problems" but the binary is eleven verbs spanning hygiene, dogfood and convergence | name the wider scope in the row, or narrow it to what a first run meets | S |
| 3 | cmd/nova-check/main.go:877 | an 877-line main.go hand-rolls dispatch, help and refusals that pkg/tool provides by construction | move dispatch onto pkg/tool and keep only the verb bodies | L |
| 4 | docs/SPEC-CHECK.md:10 | the spec claims help prints the convergence line byte for byte, which the banner does not | state which help form prints the line, and quote the banner as it prints | S |
| 5 | docs/SPEC.md:327 | the spec is split, record-layer verbs here and convergence in SPEC-CHECK.md, so one tool has two contracts | link the two at the top of each, or merge them under one heading | M |

## Good, keep
The per-verb contract shape — Asserts / Says NO / Refuses / Deliberately does
not check — is the clearest statement of a tool's promise I have read; keep it.
The honest "it may not help if" lines and the no-guessing refusals that name
every missing flag at once must not be lost.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| several unrelated tools behind one name | STILL THERE | docs/CLI.md:7-25 lists eleven verbs from link checks to a convergence metric under one binary |
| a private vocabulary | STILL THERE | docs/CLI.md:13 uses kernel and docs/CLI.md:17 floors with no definition on the first-run path |
| two cap flags | CHANGED | docs/SPEC.md:201-206 names one fail-max ceiling default 20 for the listing verbs, while hygiene keeps its own max |
| a doc that says a shipped verb does not exist | FIXED | cmd/nova-check/main.go dispatches every verb the reference lists |
| newcomer path assumes one self-repository workflow | STILL THERE | docs/CLI.md:17 and docs/CLI.md:18 assume a seed, door and ledger workflow a stranger does not have |
