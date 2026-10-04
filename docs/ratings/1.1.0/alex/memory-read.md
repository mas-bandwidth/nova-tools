# nova-memory READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8.5/10
README: 8/10

## Reasons

Read cold, running nothing. The README row (README.md:32) says what the tool
is for in one sentence — search your own markdown notes, and check a draft
against what they already say — and the banner's first line
(cmd/nova-memory/main.go:44) is that sentence word for word, the family rule
kept. The section (docs/CLI.md:332-416) opens with a real quickstart
transcript, then both working verbs by hand, and the spec
(docs/SPEC.md:2327-2812) is the best prose here: it opens with the problem
(O(n*m) consolidation and the silent failure), states which verbs assert and
which only report, and closes with a STATUS section that says plainly what is
proven and what is not (docs/SPEC.md:2769-2786) — calibrated honesty of a
kind I rarely meet. Comments say why, in present tense, with the measurement
that bought each decision (internal/memindex/memindex.go:14-41), and the tests
teach the contract: the first-run transcript is executed line for line as a
sequence with the defect that made it one named
(cmd/nova-memory/firstrun_test.go:121-137), and a refusal's hint sentence is
pinned by test (cmd/nova-memory/firstrun_test.go:25-74).

Reading impressions from the README, before any code: first confused at
README.md:26 (the sprint row's "a sprint of work cards, dealt to a fleet of
workers and read before they land" — four concepts of the house vocabulary in
one sentence, none defined at that point on the page); first bored at
README.md:61-69 (three commands of git ceremony for a bus trial, on the way to
a memory tool that needs none of them); first doubt at README.md:48-50 (the
trial block pins the 1.0.0 release inside a 1.1.0 tree, so a cold reader
cannot tell whether the row's command matches the head without opening the
release notes).

What it costs: the refusal lines predate the family's one grammar — no
REFUSED word, an unknown verb answered without the verbs there are — and sit
in the tree as shrink-only ledger debt, although the tool already builds its
JSON envelope on the shared skeleton (cmd/nova-memory/retrieval.go:9), so the
spec's parenthetical excuse for the older shape (docs/SPEC.md:230-232) no
longer describes this tool. The usage block states an effect for `version`
only, while the reference's copy of the same block carries one for every verb —
two texts for one banner. Search and check render refusal text into a captured
builder and re-wrap it as JSON, where the results, by contrast, hold one
value and render twice from it. A 10 needs the refusal grammar moved onto the
shared shape, the per-verb effect lines in the binary's own banner, refusals
collected as reasons once, and the seven verbs' repeated flag plumbing behind
one prelude.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:200 | refusal lines print the pre-family shape `nova-memory <verb>: <reason>; run: nova-memory help` with no REFUSED status word, and an unknown verb or flag is not answered with the names there are; carried as shrink-only debt (internal/ci/testdata/toolanswers/cmd/nova-memory.txt:3) although the tool already imports the shared skeleton for its JSON envelope (cmd/nova-memory/retrieval.go:9), and docs/SPEC.md:230-232 excuses the older shape only for tools not on that skeleton | print refusals through the shared shape — REFUSED word, verbs and flags named — clearing the three ledger rows | M |
| 2 | cmd/nova-memory/main.go:56 | the usage block states an effect for `version` only; the six working verbs carry none, while the reference's copy of the same block does (docs/CLI.md:335-347), so the binary's help and the doc disagree on what a verb's line says, and the ledger holds 8 verbs whose -h states no effect | carry the per-verb effect line from docs/CLI.md into the banner, one clause per verb | S |
| 3 | cmd/nova-memory/main.go:900 | search and check render refusal text into a captured builder first and re-wrap the accumulated text as the JSON envelope's why, so a JSON consumer parses text lines with the door sentence repeated inside `why` beside `remedy`; the results hold one value and render twice from it (cmd/nova-memory/retrieval.go:35) | collect the reasons once as a list and render the text and JSON refusals from it | S |
| 4 | internal/memindex/memindex.go:297 | the frontmatter fence is chunked and indexed as content, so a query over metadata words returns receipts whose snippet is the `---\nname: ...\n---` fence while the name= and type= fields already carry that text | skip the frontmatter block when chunking, keeping the fields | S |
| 5 | cmd/nova-memory/main.go:925 | the seven verbs each re-state the parse, required-flag, value-check and build sequence by hand — the `bad := !ok` accumulation appears in six verb bodies — so the every-problem-at-once law lives in copied repetition rather than one prelude a new verb inherits | one shared verb prelude holding the law, the verbs stating only their own flags and checks | M |

## Good, keep

The spec's STATUS section (docs/SPEC.md:2769-2786): run-proven on one line,
value unproven in general, and eval shipped as the honest answer — measure
rather than believe, including about the paragraph that says so.

The quickstart design (cmd/nova-memory/main.go:609-711): every step's command
line printed above the output it produced, and a closing note saying the
choices were made for you this once and nobody makes them again.

The calibration line (internal/memindex/channels.go:212-236 and
cmd/nova-memory/main.go:176-186): a live negative band on every retrieval run,
with the channel that surfaced each hit named beside its score, and the
determinism of every ordering pinned by test (cmd/nova-memory/main_test.go:445).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,394-line main.go that hand-prints every verb twice | CHANGED | cmd/nova-memory/main.go is 1,322 lines with the retrieval rendering extracted (cmd/nova-memory/retrieval.go:12-92), and per-verb -h is derived from the one banner through the shared seam (cmd/nova-memory/main.go:210); the seven verbs' flag plumbing still repeats by hand in the one file |
| main.go scrapes its own stderr for --json | CHANGED | results are one value rendered twice (cmd/nova-memory/retrieval.go:35-69); only the refusal path still builds text first into a builder and re-wraps it (cmd/nova-memory/main.go:900-915) |
| the universal unrelated/noise calibration claim is not supported | FIXED | the claim is scoped to the corpus it was measured on (cmd/nova-memory/main.go:87-88; docs/SPEC.md:2520-2523: "that is evidence, not taste, and the same measurement is available to you on yours") |
| the README's trial block pinned to the 1.0.0 release inside a 1.1.0 tree | STILL THERE | README.md:48-53: "Install one binary from the [1.0.0 release]... go install ...@v1.0.0", while the tree around it says 1.1.0 |
