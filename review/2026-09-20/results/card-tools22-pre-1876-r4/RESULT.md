RESULT tools22-pre-1876-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1876 at head 675c5d2dd240: docs/CLI.md: nova-merge land, the verb that admits things to a merge queue, has an entry (#1748
PREREAD 1876 claims=5 proven=4 unproven=1 defects=1 high=0

PR 1876, HEAD 675c5d2dd240d619669732de87935d625677dc63, BASE rowan/tools14-c2-cli-merge-holdflags, MERGE-BASE 40d4f6969e5398527c4da0501bca8c39c6988b4f, BEHIND 0, FILES 1 production, 1 test, LINES +162 -0

1. nova-merge land has a docs/CLI.md entry with its usage line and verdict grammar.
   PROVEN-BY internal/docs/cli_merge_land_test.go:17 TestTheCLIRferenceNamesEveryNovaMergeLandFlag
2. Every flag from land.go is named in the docs usage line.
   PROVEN-BY internal/docs/cli_merge_land_test.go:83
3. No flag in the docs usage line is unknown to land.go.
   PROVEN-BY internal/docs/cli_merge_land_test.go:100
4. The verdict tokens LAND OK and LAND REFUSED appear in the reference.
   PROVEN-BY internal/docs/cli_merge_land_test.go:124
5. The doc describes land's role (ONE caller, everything after batch, two green-ness checks, hold flags from batch, refusal shapes).
   UNPROVEN

DEFECT medium docs/CLI.md:1327 — LAND OK doc says checks=<required|waived> but land.go:274 outputs checks=g<n>/p<n>/r<n> (Checks.Field) — a tool or person reading the doc expects "required" or "waived" and gets a field-value pair — fix: align the placeholder with the actual output format, which is different from batch's

QUESTIONS
1. The doc text about land's role and hold flags — does SPEC-MERGE.md also cover the land verb, or is CLI.md the sole reference for the landing contract?
2. The existing land_test.go tests the receipt path but not the --reviewers/-lane hold-checking path specific to land. Is this gap covered by batch tests, or is it a known omission?

Left owed: Read every line of the two changed files. The diff is 162 lines total, all read.===FILE=== card-tools22-pre-1876-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1876-r1	1	2026-09-20T19:06:19Z	2026-09-20T19:11:19Z	0	openrouter	deepseek/deepseek-v4-flash	102925	7674	0	1495040	16234	0.0160
