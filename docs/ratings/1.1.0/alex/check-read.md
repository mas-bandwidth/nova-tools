# nova-check READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8.5/10
README: 8/10

## Reasons

Read cold, nothing run. The README's line for the tool: "checks over markdown records and repositories, each finding named by file and line" — and the banner's first line is that same sentence, so a stranger leaves the front door knowing what the tool is for in three lines. The first place I was confused is README.md:34: the row's first command points at the tool's own testdata, which only exists in a source checkout, and the cell does not say so. The first place I was bored is README.md:24, where the table's first data cell carries flag spellings a newcomer cannot yet assess. The first place I doubted a claim is README.md:48: "These are the Nova Tools 1.0.0 commands", in a tree whose docs, notes and ratings say 1.1.0 — a newcomer following it installs the older release and then reads the head reference.

What earns the score: the banner answers all three questions and its example runs as printed; the refusal grammar is the strongest I have met in this family — one line, the door named, every missing flag reported in one run, each with a hint saying what the flag wants (cmd/nova-check/main.go:306); every verb answers -h at exit 0; listings are capped with a MORE line that carries the paste-back command; and the spec states for every verb what it asserts, when it says NO, when it refuses, and what it deliberately does not check — the spots I checked bear out (the links scanner's stated limits match the code, internal/check/links.go:263). The tests teach the contract rather than restate it: the banner's examples are run, the documented transcript is executed line by line, and the claim that an OK line means every check passed is pinned by a test (cmd/nova-check/firstrun_test.go:278).

What costs it: the spec prose leans on a private vocabulary — "one line's own self repo" (docs/SPEC.md:332), the door, the pool, the pit-stop ledger — that a stranger must decode before the verbs read plainly, and the doc surface is heavy, about 1,500 lines over two spec files for eleven verbs; two cap spellings coexist (--fail-max everywhere, --max on hygiene); floors cannot be tried cold because the fixture ships no source file to compare against (docs/TESTS.md:269); and a code comment names two callers that exist nowhere in the tree (cmd/nova-check/hygiene.go:5). A 10 would need plain nouns at the front door, one cap spelling, a runnable floors pair, and every claim in the tree pointing at something real.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:332 | the prose leans on a private vocabulary — one line's own self repo, the door, the pool, the pit-stop ledger — that a stranger must decode before the verbs read plainly, over a spec surface of about 1,500 lines in two files | plain nouns in the tool-facing sections, or one glossary line under the banner naming each seed-bound noun | M |
| 2 | cmd/nova-check/hygiene.go:43 | hygiene caps findings with --max while every other listing verb uses --fail-max, so a reader who learned one ceiling meets a second spelling on the verb a branch gate calls | one spelling across the binary, keeping the other as an alias if the family asks for it | S |
| 3 | docs/TESTS.md:269 | the floors verb cannot be tried cold: the fixture ships the door file but no source to compare it against, and the verb's nouns are the seed's own | a two-file example pair under testdata, or one line in the reference saying who this verb is for | M |
| 4 | cmd/nova-check/hygiene.go:5 | the file comment claims three callers, two of which exist nowhere in the tree, so a reader hunts for binaries that are not there | name the callers that exist, without absent names | S |
| 5 | README.md:48 | the trial section pins the 1.0.0 release in a tree whose docs and notes say 1.1.0, so a newcomer installs older binaries and then reads the head reference | name the current release, or say the latest one | S |
| 6 | README.md:34 | the row's first command points at the tool's own testdata, which only exists in a source checkout, and the cell does not say so | say the trial needs a checkout, or offer the two-line tree the banner already shows | S |
| 7 | cmd/nova-check/main.go:124 | the setup lines sit above the example: label and five note lines follow it, so the block a stranger pastes is neither self-contained nor final | move the mkdir and printf lines inside the example block and the notes above it | S |

## Good, keep

The refusal discipline: every missing flag named in one run, each with a hint saying what it wants, and the door named in one line (cmd/nova-check/main.go:306).
The deliberately-does-not-check sections of the spec, which state each verb's limits as load-bearing prose the code bears out.
The capped listing whose MORE line carries the paste-back command, with the count line printing on failure too.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| several unrelated tools behind one name | CHANGED | docs/SPEC.md:329 states one binary, ten record-layer checks, and names the grouping and the three pointed elsewhere |
| a private vocabulary | STILL THERE | docs/SPEC.md:332 opens the tool's spec with one line's own self repo; the door at docs/SPEC.md:1110; the pit-stop ledger at docs/SPEC-CHECK.md:236 |
| two cap flags | STILL THERE | cmd/nova-check/main.go:116 documents --fail-max; cmd/nova-check/hygiene.go:43 uses --max |
| a doc that says a shipped verb does not exist | FIXED | docs/CLI.md:24 documents spelling; cmd/nova-check/spelling.go:15 implements it |
| the newcomer path and vocabulary assume one self-repository workflow | CHANGED | the newcomer path is quickstart over any directory (cmd/nova-check/main.go:37) and docs/TESTS.md:269 says plainly why floors needs a pair you own; the vocabulary half remains |
