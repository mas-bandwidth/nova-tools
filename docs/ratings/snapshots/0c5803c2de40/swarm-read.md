# nova-swarm READ rating, current baseline 0c5803c2de40

Rater: opencode/qwen3.8-flash
Build: 0c5803c2de40
Score: 6.5/10
README: 7/10

## Reasons

Source read: the full SHA 0c5803c2de406c1b0b2b0841f579c9bf73406b1c, the staged HEAD at the start of this read; every line below is that snapshot. Nothing was run: this is a cold read of README.md, AGENTS.md (the embedded standard), docs/CLI.md:724-931, docs/SPEC-SWARM.md, docs/nova-swarm-quickstart.md, docs/USAGE.md:217-235, then cmd/nova-swarm/ from main.go, then the pkg/swarm packages its imports reach (worker, templates, lintheader, contract, slots, nativebudget, stage) and the tests that name the contract.

What a visitor gets today: ten verbs whose help answers what the tool does, how it works and how to start (cmd/nova-swarm/main.go:34-42), a bare command that names its door in one line (cmd/nova-swarm/main.go:204), refusals that say what a flag WANTS and refuse to guess (cmd/nova-swarm/main.go:316-321), an exit table quoted per verb (cmd/nova-swarm/verbhelp.go:23-36), a verdict a run must earn (`INCOMPLETE` with a `why=` instead of a free `OK`, cmd/nova-swarm/main.go:898-911), a key that is read as data and never sourced (cmd/nova-swarm/main.go:93-98), and a spec that opens with the failure behind each rule (docs/SPEC-SWARM.md:9-21). 27,963 test lines over 22,006 production lines in cmd/nova-swarm plus pkg/swarm, with test names that state the contract.

What it costs: the reader cannot follow or safely change the one verb that matters, because `native` lives in a single 1,188-line function (cmd/nova-swarm/native.go:262-1449). The normative spec then promises enforcement that the code does not have: docs/SPEC-SWARM.md:228-238 says a fourth model call is refused at admission and that a `TURNS:` line bounds an explore card, and nothing in cmd/nova-swarm or pkg/swarm reads either; the three red tests the section names at docs/SPEC-SWARM.md:250-258 do not exist, and the test that stands in their place asserts only that the document contains those phrases (pkg/swarm/pipeline_doc_test.go:23-39). The interface is still read as a wall: one 16,783-character line in the spec (docs/SPEC-SWARM.md:76), 453-character usage lines in the banner (cmd/nova-swarm/main.go:52), and batch, card and ledger numbers printed at a stranger — `[batch 1: 25 of 67 findings ...]` is inside the template the tool prints (pkg/swarm/templates.go:27, shown as the first-run transcript at docs/CLI.md:824). Refusal grammar splits in two: `refuse` prints the house line with its remedy (cmd/nova-swarm/main.go:166-179) while the flag collector prints `nova-swarm native: <problem>` with neither word nor remedy (cmd/nova-swarm/main.go:335-340), which is what docs/TESTS.md:458 shows. Family drift is real: the tool does not sit on `pkg/tool` (cmd/nova-swarm/main.go:245-340 re-homes flags, dispatch and help), so no verb here accepts `--json`, the one flag every other tool answers with (pkg/tool/tool.go:6-8).

First place I was confused: README.md:29 — the row tells me the first command prints "the read-pr card template"; "card" is used by two rows (README.md:26, README.md:29) and never defined in the file, so I cannot tell whether `read-pr` is a verb, a task or a name.

First place I got bored: README.md:21-42 — seventeen rows of raw HTML table, each cell a setup paragraph, README.md:29 alone running 416 characters; the text form of this page is a wall and it repeats docs/USAGE.md.

First claim I doubted: README.md:10 — "leaving more time and tokens for the work that needs thought" carries no number and no pointer; the measured figures exist (docs/SPEC-SWARM.md:414-417) and are not linked from the sentence that asks for belief.

A 10 needs: `nativeRun` cut into the phases its own comments already name; the pipeline section either implemented or removed from the spec, with its named red tests present; the long spec lines rewrapped as lists; ticket and batch tokens out of printed text and file names; the flag collector speaking the one refusal grammar; and the tool standing on `pkg/tool` so `--json` comes by construction.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-swarm/native.go:262 | one function of 1,188 lines holds the whole launch: 16 numbered phases, 44 refusals, 476 comment lines; no reader follows it end to end or edits it safely | cut each numbered phase into its own function in file order, keeping the refusal order a test already pins | L |
| 2 | docs/SPEC-SWARM.md:252 | the section names three red tests it says were seen red; none exists in the tree, and the only test here compares the document's own phrases (pkg/swarm/pipeline_doc_test.go:34) | write the three tests against the fake harness, or drop the section and the phrases its doc test pins | M |
| 3 | docs/SPEC-SWARM.md:228 | admission refuses a fourth model call and a `TURNS:` line bounds an explore card: neither is read anywhere in the code; the header parser treats `MODE:` as an unknown key to read past (pkg/swarm/lintheader_test.go:276) | implement the two refusals in the lint and the runner, or name the tool that enforces them and cite its code | M |
| 4 | pkg/tool/tool.go:7 | the tool is outside the shared skeleton, so it has no `--json` on any verb and its own dispatch, banner and help code (cmd/nova-swarm/main.go:245-340): a reader who learns one tool does not learn this one | move the ten verbs onto `pkg/tool` verb by verb, `version` first, so both renderings come from one value | L |
| 5 | docs/SPEC-SWARM.md:76 | the member verb is one sentence of 16,783 characters; the same shape at docs/SPEC-SWARM.md:56, cmd/nova-swarm/main.go:56 and docs/CLI.md:745: the contract is unreadable in any terminal | break each into a list of one line per rule, keeping every claim | S |
| 6 | cmd/nova-swarm/main.go:335 | the collector prints `nova-swarm native: --tokens is required ...` with no status word and no `run:` remedy, while main.go:166 promises the house grammar and docs/TESTS.md:458 records the difference | send every collected problem through the same one-line grammar with the verb's help as the remedy | S |
| 7 | pkg/swarm/templates.go:27 | the template the tool prints carries `[batch 1: ...]` and `[batch 3: ...]`: figures from runs a visitor cannot look up, inside the text a worker is handed | move the evidence to the spec's failure table and print the condition alone | S |
| 8 | cmd/nova-swarm/main.go:653 | comments and file names speak in tickets: `CARD-8349` here, `CARD-8390` at cmd/nova-swarm/native.go:290, `13d` at main.go:647, `tool ledger X2` at main.go:305, files such as pkg/swarm/d6_budget_test.go, pkg/swarm/read74_brevity_test.go and pkg/swarm/eff87_cross_tool_spec_test.go | name the rule the token stands for; keep the ticket only where a reader can open it | M |
| 9 | cmd/nova-swarm/main.go:798 | comments narrate the past failure at length ("This line said `NATIVE OK` for every run that reached it"), where the standard asks present tense; pkg/swarm/templates.go:12 does the same in the file that prints the template | state the rule now, and keep the story in the spec's failure table | S |
| 10 | docs/SPEC-SWARM.md:366 | the spec cites `bin/child-clone.sh:111`, and no `bin` directory exists at this snapshot; pkg/swarm/lintheader.go:32 cites internal/pulse/cardheader.go, and internal/pulse is deleted; pkg/swarm/pipeline_doc_test.go:16 cites a package that is not in this tree and a SPEC-DECIDE.md that is not in docs | point each citation at a file in this tree, or say the commit its history stands at | S |
| 11 | cmd/nova-swarm/main.go:55 | the banner's `native` line names 16 of the verb's 26 flags: `--no-wall` and `--sandbox` (main.go:555-556), which decide containment, appear only in prose and in `-h` | list every flag the verb accepts, or say in the line that `-h` holds the rest of them | S |
| 12 | cmd/nova-swarm/main.go:565 | `--slots-store` and `--owner` are registered and read by nothing, and the legacy `--auth` and `--config` shape is still advertised in the banner (main.go:96): dead surface a cold reader must disbelieve | refuse the two empty flags with the dealer's verb as the remedy, and date the legacy shape's removal in the changelog | S |

## Good, keep

- The refusal voice: a missing flag is named with what it wants and the run says every problem at once (cmd/nova-swarm/main.go:316-340), and `--tokens` is never inferred from a provider (cmd/nova-swarm/main.go:342-365).
- The verdict must be earned: `INCOMPLETE` with `why=` keeps every other field byte for byte, so a card that produced nothing never reads as delivered (cmd/nova-swarm/main.go:798-839).
- The spec's opening table, failure then rule (docs/SPEC-SWARM.md:9-21), and the doctor that refuses to launch under a shadowed binary (cmd/nova-swarm/doctor.go:440-461).

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| a 1,195-line function | STILL THERE | cmd/nova-swarm/native.go:262 opens it and its brace closes at cmd/nova-swarm/native.go:1449: 1,188 lines |
| 142 ticket numbers in non-test code | STILL THERE | cmd/nova-swarm/main.go:653 `CARD-8349`, cmd/nova-swarm/main.go:647 `13d`, pkg/swarm/templates.go:27 `[batch 1: ...]` |
| a 1,900-character usage line | CHANGED | cmd/nova-swarm/main.go:52 is the banner's longest line at 453 characters, while docs/SPEC-SWARM.md:76 now holds 16,783 on one line |
| legacy paths still named | STILL THERE | cmd/nova-swarm/main.go:96 names the legacy `--auth` shape in the banner; cmd/nova-swarm/main.go:565 registers two flags read by nothing |
| an intimidating, historically narrated interface | STILL THERE | docs/SPEC-SWARM.md:7 opens with the failures, cmd/nova-swarm/main.go:798 narrates a past run, pkg/swarm/templates.go:12 tells the batches |
| README 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:29: an honest prerequisite line and a runnable first command, with "card" still undefined anywhere in README.md |
