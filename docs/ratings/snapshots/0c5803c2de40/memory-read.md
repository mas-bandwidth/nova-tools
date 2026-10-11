# nova-memory READ rating, current baseline 0c5803c2de40

Rater: qwen3.8-flash (the worker on this card, flash tier; a cold read, nothing run)
Build: 0c5803c2de40
Score: 8/10
README: 7.5/10

## Reasons

The source under review is snapshot 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the staged checkout's HEAD is that commit itself, so every file:line below reads at the rated source. This is a reading-only rating: no binary and no test runs before the gate, and the transcripts, timings and scores printed in the docs are read as claims, not reproduced.

What earns the 8: the README's three-line sentence for the tool — search your own markdown notes, and check a draft against what they already say (README.md:32) — is what the code does, and the banner's line 1 is that same sentence (cmd/nova-memory/main.go:44). The entry point, the eight verbs and the data are found in a minute: one quickstart line (docs/CLI.md:355), a usage banner that answers what, how it works and how to use it, and a fixture corpus with its own handbook. The names belong to a stranger: receipts, the calibration band, class as top-level directory — no ticket, date, person or rule number. Comments say why, in present tense, and cite the measurement that bought the decision (internal/memindex/memindex.go:25 for the normalizer order, internal/memindex/verify.go:61 for the masking narrowings). The tests teach the contract: one pins the README's sentence to the banner (cmd/nova-memory/firstrun_test.go:138), one pins every defined flag into the usage text (cmd/nova-memory/firstrun_test.go:629), one requires byte-identical reruns (cmd/nova-memory/main_test.go:445), one locks probe and schema version to move together (cmd/nova-memory/main_test.go:237). Claims hold up under reading: check prints its own never-exits-1 sentence in its output (cmd/nova-memory/main.go:1001), and the spec says plainly what is proven and what is not (docs/SPEC.md:2769).

The gap to 9 is weight and family, findings 1 to 3: the binary is one 1,286-line hand-written dispatch (cmd/nova-memory/main.go:207) that re-owns the banner, the help door, the refusal line, the exit table and the unknown-name answer that the skeleton gives by construction — the standard's own words (docs/STANDARD.md, embedded in AGENTS.md, "When building a tool"): start from pkg/tool, never from another tool's main.go. The refusal-to-JSON capture is copied verbatim between the two verbs that accept --json, and four verbs accept no --json at all though the family rule says every verb does. To reach 10: put the verbs on the skeleton or write the exception and its reason into the spec; share one capture helper; give stats, verify, eval, boot and quickstart the same value in JSON; tighten boot's promise from reads to what the code stats (docs/SPEC.md:2493 against cmd/nova-memory/main.go:817); and let the CAL line describe its band as the operational definition it is rather than a measured noise law (cmd/nova-memory/main.go:50).

The first three stumbling points, written before any code opened, as a visitor arriving cold:

- First confusion — README.md:32: the nova-memory row promises to check a draft against what they already say, and its setup column ends by printing the chosen query, matching sources and checks. Draft and checks name nothing until docs/CLI.md opens them; the row asks for trust two nouns before it earns one.
- First boredom — README.md:24: the table's first row spends three long sentences of setup before the one command worth copying, and every row is one dense HTML line; the scroll onward passes a bus and git trial setup (README.md:61-69) that a reader who came for the notes tool does not need.
- First claim doubted — README.md:9: leaving more time and tokens for the work that needs thought is an outcome no file measures, and the tool's own honest STATUS admits value is unproven in general (docs/SPEC.md:2776) — the README's economy sentence is the one place the set oversells what its specs refuse to claim.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:207 | all eight verbs dispatch through a hand-written switch: banner, help door, refusal line, exit table and the unknown-name answer are re-implemented rather than inherited, and one 1,286-line file owns every verb body plus the per-platform shell quoting | move the verbs onto the pkg/tool skeleton, which the standard says gives those by construction (docs/STANDARD.md, "When building a tool"), or record the exception and its reason in docs/SPEC.md | L |
| 2 | cmd/nova-memory/main.go:864 | the 16-line stderr-capture that turns a refusal into a --json envelope is copied verbatim into cmdCheck at main.go:924, and the captured multi-line text (reason plus hint lines) lands in one JSON reason field holding embedded newlines | one shared helper both verbs call, carrying structured problems into the envelope rather than preformatted text: the standard names a copy a bug (docs/STANDARD.md section 7) | M |
| 3 | cmd/nova-memory/main.go:716 | stats, and likewise boot (main.go:753), verify (main.go:1012), eval (main.go:1138) and quickstart (main.go:609), accept no --json, though the family rule promises every verb does (AGENTS.md section 2): a caller scripting verify or eval parses typed lines | render the same values as the one result envelope, or write the exception and why line-only is right into docs/SPEC.md | M |
| 4 | cmd/nova-memory/main.go:817 | boot validates a pinned entry with os.Lstat only; the spec promises boot reads exactly those files (docs/SPEC.md:2493), so a pinned file of nonsense counts as loaded and the byte total is a directory entry's claim, not a read's | say stats in docs/SPEC.md:2493 and the banner, or open the files and read the bytes the total names | S |
| 5 | cmd/nova-memory/main.go:50 | the banner states that a hit scoring at or below the CAL line is no better than noise, but the band is one fixed probe sentence (main.go:181): one unrelated sample defines a threshold, it does not measure noise, and docs/SPEC.md:2572 repeats the generalization | phrase the CAL line as the operational band it is: what the probe scores on your corpus, and what the tool then decides against | S |
| 6 | cmd/nova-memory/main.go:477 | the quickstart demo-word picker carries a hand-written 22-line function-word table inside the control file, and its own comment warns this must never become a stopword list — data living inside logic invites exactly the drift the comment fears | keep the table but move it to embedded data beside its comment, or derive the demo words from the index's document-frequency shape without a list | S |

## Good, keep

- quickstart prints the exact argv each step ran, through the same dispatch a shell reaches, with per-platform pasting pinned by test (cmd/nova-memory/main.go:569, cmd/nova-memory/firstrun_test.go:569): a transcript that cannot teach an invocation that does not work.
- the CAL band is measured live on the caller's own corpus every run, and one test pins probe and schema version to move together (cmd/nova-memory/main.go:181, cmd/nova-memory/main_test.go:237).
- docs/SPEC.md:2769 states what is proven and what is not, and ships the eval harness so a reader measures instead of believes: that honesty is the tool's best feature and the reason its claims survive a cold read.

## Compared with earlier ratings

The earlier line read after the Score line stands; it is a READ on 2026-10-02 at 1aac13259 (one rater 7: a 1,394-line main.go that hand-prints every verb twice and scrapes its own stderr for --json; another 7.9: the universal unrelated/noise calibration claim is not supported; README then 6.5 to 7 from one rater, 8.4 from another).

| earlier | now | evidence |
|---|---|---|
| main.go is 1,394 lines and hand-prints every verb twice | CHANGED | 1,286 lines at cmd/nova-memory/main.go:1; the verbs' text is printed from one shared render function (cmd/nova-memory/retrieval.go:35), the flags are still described twice (banner main.go:84-125 against the flag-set usages at main.go:881) but one test now pins banner and flags together (cmd/nova-memory/firstrun_test.go:629) |
| the --json path scrapes its own stderr | CHANGED | the refusal text is captured in-process into a builder and wrapped into the envelope without reparsing (cmd/nova-memory/main.go:864, cmd/nova-memory/main.go:872), though the capture block is now duplicated (finding 2) and its JSON reason holds embedded newlines |
| the universal unrelated/noise calibration claim is not supported | CHANGED | the band is measured per run on the caller's own corpus (cmd/nova-memory/main.go:176), which grounds it far better than a remembered number; one fixed probe sentence still speaks for all noise (cmd/nova-memory/main.go:50) |
| README 6.5 to 7 from one rater, 8.4 from another | CHANGED | 7.5 here: the nova-memory row's sentence is now the banner's line 1, pinned by test (README.md:32, cmd/nova-memory/main.go:44, cmd/nova-memory/firstrun_test.go:138), while the table density and the unmeasured economy sentence remain (README.md:9, README.md:24) |
