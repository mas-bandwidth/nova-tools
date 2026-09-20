RESULT tools22-pre-2135-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2135 at head d76aeaaa533d: nova-tokens: pin fold-pool usage in help so reverting 2efa3f52 goes red (#2024)
PREREAD 2135 claims=2 proven=2 unproven=0 defects=0 high=0

PR 2135, HEAD d76aeaaa533d286c9b139d88da795a405992d602, BASE dev, MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730, BEHIND 1, FILES 0 production, 1 test, LINES +11 -0

The diff against the merge base is a single file: cmd/nova-tokens/help_test.go, one new test,
11 lines, no production change. The commit's claim is that a revert of 2efa3f52 — which added
the `nova-tokens fold-pool --pool <dir> --ledger <file> [--since <stamp>]` line to the usage
banner in main.go — now turns the tree red, where before it stayed green because the test that
landed with 2efa3f52 (spec_drift_363_test.go) only asserts against docs/SPEC-TOKENS.md.

1. A revert of 2efa3f52 (removing the fold-pool usage line from cmd/nova-tokens/main.go's
   banner) now makes the nova-tokens suite go red.
   PROVEN-BY cmd/nova-tokens/help_test.go:21 TestHelpListsFoldPoolForm — invokes run("help")
   and wantContains the exact string "nova-tokens fold-pool --pool <dir> --ledger <file>
   [--since <stamp>]", which run("help") prints only from the usage const at main.go:59; delete
   that line and the assertion fails. Ran the test at this head: passes.

2. The nova-tokens help banner names the fold-pool usage form.
   PROVEN-BY cmd/nova-tokens/help_test.go:24 — same wantContains; the banner output is asserted
   through the real run("help") path, not by reading docs or the source.

DEFECTS none

QUESTIONS FOR THE REVIEWER

1. The pin sits on the usage banner text only. A revert that kept the banner line but removed
   the `case "fold-pool"` dispatch at main.go:167 would stay green here — was the banner line
   the actual observed loss in #2024, or should a dispatch-level pin also exist?

2. Reverting 2efa3f52 as a whole also removes spec_drift_363_test.go and the SPEC-TOKENS.md
   promises this PR's comment credits as the old (insufficient) coverage. Is the intent that
   this new test fully replaces that spec-drift guard for fold-pool, or are both expected to
   coexist?

3. Is exact-string pinning of banner lines the established convention here (TestHelpListsSwarm-
   RootForm does the same), and is a future wording change to the banner expected to require
   touching this test, or is a looser anchor preferred?

Left owed — I read the full diff (11 lines, one test file), the touched production const and
usage banner in cmd/nova-tokens/main.go, and the 2efa3f52 diff (main.go + spec_drift_363_test.go)
that this PR guards. I did not read the other 17 files shown by `git diff 5298f6be12ea..pr2135`;
those are dev's landed work since the merge base (the integration-16as commit), not this PR's
changes, and the merge-base diff (used here) touches only help_test.go.

git status --short:
(empty)

git rev-parse HEAD:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-2135-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2135-r1	1	2026-09-20T19:28:08Z	2026-09-20T19:36:05Z	0	opencode	deepseek-v4-flash	21983	17463	0	476416	0	0.0213
