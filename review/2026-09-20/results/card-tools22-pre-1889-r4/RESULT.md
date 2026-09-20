RESULT tools22-pre-1889-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1889 at head 522352392cad: jobs: a repeated need is one edge in the seeded graph (#1788)
PREREAD 1889 claims=4 proven=4 unproven=0 defects=1 high=0

PR 1889
HEAD 522352392cadae5d06df8950bc2fecd7ead02854
BASE dev
MERGE-BASE 4a93fd75a3b030ee36c4a16fcd55f71aa4379549
BEHIND 15
FILES 1 production, 1 test
LINES +74 -0

CLAIMS

1. A node whose Needs names the same id more than once seeds as one forward edge, not one per repetition.
   PROVEN-BY internal/jobs/duplicateneeds_test.go:18 TestARepeatedNeedIsOneEdge — seeds a with Needs [b b] and asserts Edges() == 1.
2. A repeated need writes one reverse (blocks) edge, not two.
   PROVEN-BY internal/jobs/duplicateneeds_test.go:24 TestARepeatedNeedIsOneEdge — after a needs [b b] asserts Blocks("b") == [a].
3. The first occurrence of a repeated need keeps its position in seed order.
   PROVEN-BY internal/jobs/duplicateneeds_test.go:41 TestARepeatedNeedIsOneEdge — a needs [b c b] asserts Needs(a) == [b c] and Edges() == 2 (line 38).
4. A repeated unresolvable need still refuses on the first occurrence with the same rule-2 message.
   PROVEN-BY internal/jobs/duplicateneeds_test.go:66 TestARepeatedNeedIsOneEdge — seeds a needs [z z] and asserts the exact refusal "rule 2: a needs z which does not exist".

DEFECTS

DEFECT low cmd/nova-work/main.go:183 — the dedup lives only inside Seed (internal/jobs/jobs.go:70), so the writers addNeeds/MarshalNodes still persist the raw duplicate strings, and a repeated `dependencies --node a --needs b` keeps appending "b" to the :deps file while the same command's `edges=` (main.go:191) stays capped at 1 — the printed count and the file the command itself wrote diverge, and the file accumulates duplicate need entries with nothing ever normalizing it — dedupe in addNeeds/MarshalNodes (or have the write path reflect Seed's dedup) so the persisted file and the counted graph agree.

QUESTIONS

1. The dedup is validator-side only; is the divergence between the on-disk :deps file (keeps the duplicate strings) and the seeded graph (deduped count) intended, or should the write path normalize too?
2. The dedup keys on the trimmed dep, so "b" and " b " in one Needs list collapse to one edge while distinct ids "b"/"B" stay two; is the whitespace-variant collapse intended, or should rule 2 refuse two spellings that trim to the same id?
3. What is the concrete "four-fold receipt" behind the subtest at line 44 — a real :deps file in the tree where one job's Needs named the same id four times? Was refusing duplicates as a malformed graph considered instead of deduping?
4. `edges=` in `DEPENDENCIES OK` and `PLAN OK` now means unique forward edges rather than raw need entries; does anything parse those counts expecting the raw entry total?

Left owed: nothing material. The whole PR is two files, both read in full, plus the touched consumers (cmd/nova-work, internal/worklang). I ran no tests (the card runs none); I did `go build` and `go vet` on internal/jobs, cmd/nova-work and internal/worklang — all clean. The merge base 4a93fd75 predates the card's BASE 5298f6be12ea (the PR branched before that integration); the head is 15 commits behind dev and is not yet on dev.

git status --short
(nothing)
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1889-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1889-r1	1	2026-09-20T19:06:22Z	2026-09-20T19:09:49Z	0	opencode	deepseek-v4-flash	31444	19558	0	631040	0	0.0275
