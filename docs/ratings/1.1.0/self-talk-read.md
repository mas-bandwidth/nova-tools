# nova-self-talk READ rating, nova-tools 1.1.0

Rater: GLM
Build: bd7949b97aec
Score: 8.5/10
README: 8/10

## Reasons

README line (README.md:37): "flags sentences where a writer passes a standing verdict on themselves" — the same sentence as the banner's first line (cmd/nova-self-talk/main.go:30), read cold top to bottom, then the command reference and spec the README points to, then the source from main.

Confused first at cmd/nova-self-talk/main.go:61. The second class's name, INSTALLATION, is glossed with what it detects (a standing self-verdict built from neutral words) but never with why that word — a verdict installed like software — while every other noun here says what it is on arrival: STANDING, DATED, FORECLOSURE as a door stated shut. The count name installations= carries the same unchosen metaphor into every closing line.

Bored first at cmd/nova-self-talk/main.go:74. By the third stretch of the banner the same output contract arrives a third time: first the usage block, then the two classes, then a print-line inventory of every SELFTALK line; docs/CLI.md:278 says it again in prose and the spec section says it again as law. Three restatements of one contract is where a read turns to skimming.

Doubted first at README.md:37. "Findings are advisory and produce exit 1" reads self-contradictory — advisory, yet a failing exit. The banner answers it (cmd/nova-self-talk/main.go:106-107 and main.go:120-122: all three example runs exit 1, and that is the tool working) and the code keeps it (cmd/nova-self-talk/main.go:517-519), so the claim is true; the row's wording alone does not carry it.

Why 8.5: the promise and the code match. The dispatch, verbs and data are found in a minute (cmd/nova-self-talk/main.go:41-47 names the verbs, cmd/nova-self-talk/main.go:131 says the data is markdown files named on the command line, stdin as -); each file does one thing (dispatch, render and scan in main.go, the shapes and example verbs in verbs.go, version in version.go, the first class in internal/selftalk/selftalk.go, the second in internal/selftalk/installation.go); comments say why in present tense with the measurement behind every choice (internal/selftalk/installation.go:400-406 records why "best" was removed and why the noun sets are closed; internal/selftalk/selftalk.go:82-84 says why hard wraps collapse). The spec's law is argued in code, not asserted: the four suppressors run in the order the spec argues them (internal/selftalk/installation.go:85-137), the seam between the classes is kept by test (internal/selftalk/installation_test.go:52), and the prohibition property that makes scanning a rule document safe is pinned directly (internal/selftalk/installation_test.go:92). Tests teach the contract: the detector table is held to its own sentences (internal/selftalk/rules_test.go:48), the published transcript is executed against the binary (cmd/nova-self-talk/firstrun_test.go:148), the one-line escape guarantee is walked over the source (cmd/nova-self-talk/audit_test.go:12), and the caps and their MORE lines are pinned (cmd/nova-self-talk/bounded_test.go:40-61). The family shape holds where it matters: the verb-flag seam (cmd/nova-self-talk/main.go:202), the refusal grammar with a remedy a cold reader can act on (cmd/nova-self-talk/main.go:176-195), the exit table stated in the banner (cmd/nova-self-talk/main.go:106-107), and the per-verb help quoted from the banner by the shared seam.

What a 10 would need: the banner cut to its orientation and example (finding 1), the internal map row true to the package (finding 2), the numbered references resolved in words at the point of use (finding 3), the README row carrying its two claims without the reader needing the banner (finding 4), and one flattening walk instead of two (finding 5). The ideas and their proofs are already here; what is left is weight and a handful of unexplained names.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-self-talk/main.go:30 | the usage const runs 94 lines (main.go:30-123): after the standard's three-question opening (line 1, how it works: at main.go:32, first run: at main.go:37) the reader still meets a print-line inventory (main.go:74-91), a flags essay and the exit table before the example block at main.go:115, and the same contract is re-said at docs/CLI.md:278 and in the spec section, so it is read three times | keep the banner to the three questions, the usage block and the example block, and leave the print-line inventory to the per-verb help and docs/CLI.md | M |
| 2 | internal/docs/catalog.go:99 | the internal map's one-line purpose for this package is "agent self-talk journal stream", rendered into internal/AGENTS.md:58; the package keeps no journal — it classifies self-claims in prose (internal/selftalk/selftalk.go:1-3) — so a reader landing from the map arrives with the wrong tool in mind | reword the row to what the package is: a self-claim classifier for prose, STANDING/DATED and INSTALLATION (a doc fix outside this card's PATHS) | S |
| 3 | cmd/nova-self-talk/audit_test.go:43 | numbered references a stranger cannot resolve in one hop: a ticket number here (#4505), "ledger T5" at internal/selftalk/rules_test.go:134, "A5" at internal/selftalk/installation.go:366, "Specimen 8" at docs/SPEC.md:1817 and "Specimen 13" at internal/selftalk/installation_test.go:52; each opens a second document hunt mid-read | say the rule in words where it is cited, and keep any number in parentheses after the words | S |
| 4 | README.md:37 | the row's two tail clauses each cost a beat: "Reads the example without editing it" names no example (the built-in pages come from the example verb, cmd/nova-self-talk/main.go:37), and "advisory and produce exit 1" reads as a contradiction until the banner says exit 1 is the tool working (cmd/nova-self-talk/main.go:120-122) | write "writes its example pages with example; exit 1 means findings, not failure" | S |
| 5 | internal/selftalk/installation.go:220 | a second flattener beside internal/selftalk/selftalk.go:128: markdown stripping, whitespace collapse and the boundary rules are written twice in one package, with subtly different laws (a heading ends a sentence in one and opens a unit in the other); the comments justify the split (the second class needs paragraph-level quote state), but the pair is the heaviest duplicated idea in the package | extract the shared strip-and-collapse walk into one helper the two flatteners take a mode to | S |
| 6 | cmd/nova-self-talk/verbs.go:37 | shapes and example build their lines and their JSON in two branches (verbs.go:37-55 and verbs.go:130-138), while scan holds the standard's one value, two renderings and pins the pair with a test (cmd/nova-self-talk/verbs_test.go:183); nothing holds the same-run law for these two verbs | build the one Out for both verbs and render it twice, as scan does | S |

## Good, keep

- The detector table held to its own words: internal/selftalk/rules_test.go:48 runs every row's finds= and passes= sentences through the scan, so the help, the shapes listing and the detector cannot drift apart. This is the family's best contract pin; do not lose it.
- Every pattern carries its measured why: internal/selftalk/installation.go:400-406 and internal/selftalk/installation.go:465-469 record what was measured, what was removed and why the sets stay closed. A reader audits precision instead of trusting it.
- The honest limit on every run (cmd/nova-self-talk/main.go:151-156) and the one-line DATED count (cmd/nova-self-talk/main.go:583-585): the tool says what a green does not clear, and the welcome case costs one line, not six hundred.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| off the skeleton | STILL THERE | cmd/nova-self-talk/main.go:199 hand-dispatches the verbs and carries its own refuse and usage const beside the verb table the siblings use (cmd/nova-cairn/main.go:31); the stated reason — the first file may be no verb (cmd/nova-self-talk/main.go:49-51) — holds the dispatch |
| an 88-line banner | STILL THERE | the usage const is 94 lines now, cmd/nova-self-talk/main.go:30-123, restructured to the three-question opening; the wall stands, its front door improved |
| comments in capitals | STILL THERE | cmd/nova-self-talk/main.go:308 and internal/selftalk/selftalk.go:91; the emphasis turns out to be the family's own style (pkg/tool/tool.go:57), so it costs less than it did |
| a class name nobody glosses | FIXED | INSTALLATION is glossed at cmd/nova-self-talk/main.go:61-64, docs/CLI.md:278 and docs/SPEC.md:1779; what remains unexplained is the word's metaphor, not its meaning |
| jargon and a semantic overstatement | CHANGED | every token is glossed at first use (cmd/nova-self-talk/main.go:53-91); the overstatement left is the internal map row, internal/docs/catalog.go:99 |
| the README rated 6.5-7 and 8.4 | CHANGED | README: 8/10 now; the row matches the banner's first line and its first command runs as printed; the two tail clauses are finding 4 |
