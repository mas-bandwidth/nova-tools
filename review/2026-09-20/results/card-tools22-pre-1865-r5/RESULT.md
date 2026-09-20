RESULT tools22-pre-1865-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1865 at head dc3fad10a9ab: nova-wake: the --advance-cursor refusal names the door, and its sibling site is dead code (#145
PREREAD 1865 claims=3 proven=1 unproven=2 defects=1 high=0

PR 1865, HEAD dc3fad10a9ab9467993bf9c4f432fdfc7cf834b5, BASE dev, MERGE-BASE 0878987f9d44eb8c7a9ad01a2180b949d46476fe, BEHIND 22, FILES 1 production, 1 test, LINES +71 -2

CLAIMS
1. An `--advance-cursor` invocation naming neither `--remote` nor `--branch` prints a refusal whose last sentence is the door (`; run: nova-wake help`).
   PROVEN-BY cmd/nova-wake/advancedoor_test.go:95 TestTheAdvanceCursorRefusalNamesTheDoor — runs a `watch --advance-cursor` without `--remote`/`--branch` and asserts the stderr ends with the door literal.
2. An `--advance-cursor` invocation naming no `--as` prints a refusal whose last sentence is the door.
   UNPROVEN — the changed string carries the door, but the code path at main.go:791 is claimed dead code, and the existing test (main_test.go:850) only checks exit=2, never the message content. No test exercises this path.
3. The sibling site at main.go:791 (the `--as` refusal) is dead code — the required-flag collector at lines 757-767 answers first with the generic printer, which already carries the door.
   UNPROVEN — the test comment asserts unreachability, which is logically correct (every path to line 791 is caught by the collector), but no test demonstrates this.

DEFECTS
DEFECT low cmd/nova-wake/main.go:791 — the `--advance-cursor without --as` refusal string now carries `; run: nova-wake help`, but this code path is unreachable — the required-flag collector returns first via `p.print()`. The change is harmless (dead code modified for consistency), but it is unnecessary noise in the diff. The existing test at main_test.go:850 would not go red if the `; run: nova-wake help` were dropped from this string.

QUESTIONS
1. Is there a reason to retain line 791 as a separate check rather than removing it, given the collector already covers the `--as` case? The dead-code comment in the test explains it, but removing the branch would make the unreachability structural rather than claimed.
2. The test reads the door literal from `func refuse(` in main.go by string position. How does this team feel about fragility from source-formatting changes (e.g. a blank line inserted before `func refuse(` would shift the index, or a reformat of the `\n` escape)?

Left owed — all files read. The diff is small (2 files, +71 -2), and both files were read in full.

git status --short: (nothing)
git rev-parse HEAD: dc3fad10a9ab9467993bf9c4f432fdfc7cf834b5===FILE=== card-tools22-pre-1865-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1865-r2	1	2026-09-20T19:24:05Z	2026-09-20T19:36:23Z	0	openrouter	deepseek/deepseek-v4-flash	117563	9722	0	1457664	11343	0.0160
