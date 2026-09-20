RESULT tools22-pre-989-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#989 at head da0250aaf1c6: CARD-8376 nova-tools #896 done with its red test first: nova-pulse triage --dedupe asks whether
PREREAD 989 claims=11 proven=7 unproven=4 defects=3 high=0

PR 989
HEAD da0250aaf1c656dc659fb7f2b8939106e35e85db
BASE dev
MERGE-BASE 485050e30543e816f4adcc6328fe717bcd1f1248
BEHIND 16
FILES 4 production, 1 test
LINES +301 -9

The merge base is 485050e3, which is OLDER than this card's base 5298f6be12ea (it is an
ancestor of it); the head is a merge of origin/dev into work and sits 16 commits behind dev.

CLAIMS

1. `triage --dedupe --issues <file>` reads the open-issues file (one `number<TAB>title` per
   line) before the card is cut.
   PROVEN-BY internal/pulse/triage_dedupe_test.go:88 TestTriageDedupeLinksASameClassIssue —
   the fixture's issue 812 is parsed out of the file and linked.
2. The verb sends internal/decide exactly one call.
   PROVEN-BY internal/pulse/triage_dedupe_test.go:85 TestTriageDedupeLinksASameClassIssue —
   asserts dec.asked == 1.
3. The state of that call is the new case's title and evidence.
   PROVEN-BY internal/pulse/triage_dedupe_test.go:95,98 TestTriageDedupeLinksASameClassIssue —
   asserts the state carries "card-892" and the evidence line.
4. The state is escaped and capped as the packet escapes evidence.
   UNPROVEN — the fixture evidence is plain and short, so nothing in the tests exercises a
   line that needs escaping or exceeds the 400-byte cap; only the code (triage.go:404) witnesses it.
5. The one question is named same_class.
   PROVEN-BY internal/pulse/triage_dedupe_test.go:76 TestTriageDedupeLinksASameClassIssue —
   the fake returns no answers unless qs["same_class"] exists, and the test requires a link.
6. The question's choices are the open issue numbers plus none.
   UNPROVEN — no test asserts the Choice options; the fake answers "812" without validating
   that 812 is an offered option, so the tests would pass even if the options were wrong.
7. A same_class choice naming an open issue at or above the floor puts `LINKS #<n> (same
   class, conf=<c>)` on the card and `dedupe=#<n>` on the TRIAGE OK line.
   PROVEN-BY internal/pulse/triage_dedupe_test.go:88,91 TestTriageDedupeLinksASameClassIssue.
8. On a `none` answer the card is cut unchanged and the TRIAGE OK line reads `dedupe=?`.
   PROVEN-BY internal/pulse/triage_dedupe_test.go:113,116 TestTriageDedupeNoneFilesNormally.
9. On a provider error the card is cut unchanged and the TRIAGE OK line reads `dedupe=?`.
   UNPROVEN — no test makes the decider return an error; the branch is code-only (triage.go:438).
10. Below the floor the card is cut exactly as today and the TRIAGE OK line reads `dedupe=?`.
    PROVEN-BY internal/pulse/triage_dedupe_test.go:133,136,139 TestTriageDedupeBelowFloorChangesNothing.
11. An unreadable `--issues` file is a refusal, never an empty list.
    UNPROVEN — ReadIssues/link() return an error that Triage refuses (triage.go:429,470), but
    no test covers a failing ReadIssues or a nil decider.

DEFECTS

DEFECT medium internal/pulse/triage.go:404 — dedupeState caps each evidence line at 400 bytes but has no total bound, so a case with many evidence lines sends the provider a state far larger than the PacketMax-bounded card it sits beside, while the comment at line 401-403 claims "the state is a bounded packet and never a transcript" — it contradicts its own doc and the packet's cost ceiling, and sameClassQuestions (triage.go:388) grows the question payload with every issue number plus its uncapped title, so one dedupe call can cost far more than the triage packet it advises — cap the state's total the way Card() drops lines to PacketMax, and bound the issue options.
DEFECT medium cmd/nova-pulse/run.go:120 + internal/pulse/triage.go:434 — link() silently promotes an explicit `--floor 0` to DefaultDedupeFloor (0.9) via `if floor <= 0`, while the flag validation accepts 0 as "between 0 and 1" and the twin `--decide` path uses the caller's 0 directly, so the same accepted flag value means two different things with no warning — refuse 0 for --dedupe with a message, or let 0 mean "link at any confidence".
DEFECT low docs/CLI.md — the new `--dedupe` and `--issues` flags have no docs/CLI.md entry (the whole `nova-pulse triage` verb is absent from docs/CLI.md, a gap this PR extends); only SPEC-PULSE rule 21 (added here) and the main.go usage line document them — a reader of CLI.md cannot find the flag — add a nova-pulse triage section to docs/CLI.md.

QUESTIONS

1. The IssueRow comment says "The loop writes the file from the API; this verb only reads it", but nothing in this PR or the tree writes a `number<TAB>title` issues file — OpenIssues/GHWork (internal/pulse/wire.go:412,823) fetch issues in memory for refill and never emit this TSV. Which loop/verb produces the `--issues` file, and is anything guaranteeing the listed issues are still open when the decision runs?
2. `--dedupe` with no key (JEV_API_KEY unset, decide.New fails) and an unreadable `--issues` file both hard-refuse the whole triage (exit 2, no card), while SPEC-PULSE rule 21's fallback list — `none`, provider error, below floor — promises "card cut unchanged". Is hard refusal on those two conditions the intended UX, or should they degrade to "cut unchanged, dedupe=?" too?
3. The LINKS line is inserted into the model's card body between the CASE line and the evidence (triage.go:215-217), so the triage route reads the dedupe decision before it reads the evidence, and the verdict question is asked of the same provider that just answered the dedupe question. Is that placement deliberate so the route weighs the link, and is a verdict changed by the LINKS line considered acceptable?

Left owed

Read in full at the PR head: internal/pulse/triage.go, cmd/nova-pulse/run.go, internal/pulse/triage_dedupe_test.go (new), internal/pulse/triage_test.go (existing), internal/decide/decide.go. The diff is small (5 files, +301 -9) and the production and test changes were read completely. I read the SPEC-PULSE.md, docs/CLI.md, internal/pulse/wire.go, gate.go and internal/oneline/oneline.go only in the sections the diff or the new code touched; I did not read SPEC-DECIDE.md in full, and I did not read the unchanged bodies of cmd/nova-pulse/main.go or the decide package's other files (answers.go, decisions.go, registry.go). go build and go vet of internal/pulse and cmd/nova-pulse pass at the head; no test run was performed (none expected).

git status --short
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-989-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-989-r1	1	2026-09-20T19:28:47Z	2026-09-20T19:40:20Z	0	opencode	deepseek-v4-flash	57245	33052	0	1786624	0	0.0673
