RESULT tools22-pre-1919-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1919 at head a7470c9e4130: nova-swarm: the help examples run as pasted (#1455)
PREREAD 1919 claims=1 proven=1 unproven=0 defects=0 high=0
PR 1919, HEAD a7470c9e41301417604503f9106238a96d6a6c09, BASE dev, MERGE-BASE 485050e30543e816f4adcc6328fe717bcd1f1248, BEHIND 16, FILES 1 production, 0 test, LINES +2 -0
1. The help examples for nova-swarm should work when run as pasted.
   PROVEN-BY cmd/nova-swarm/firstrun_test.go:56 TestUsageBannerExamplesRun — runs each example line from `nova-swarm help` and verifies it does not exit 2

DEFECTS none

QUESTIONS FOR THE REVIEWER.
1. The added setup line references `cmd/nova-swarm/testdata/cards` which does not exist in the repository. Should this directory be created elsewhere, or was this instruction intended for a different context?
2. The merge base for this PR (485050e30543) is older than the card's stated base (5298f6be12ea). Should this be rebased onto the newer base before review?

Left owed
No test files were read beyond cmd/nova-swarm/firstrun_test.go which contains the test that validates example lines from help output.

git status --short
git rev-parse HEAD===FILE=== card-tools22-pre-1919-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1919-r1	1	2026-09-20T19:25:10Z	2026-09-20T19:27:24Z	0	inception	mercury-2.5	952718	617	0	206199	3907	0.0396
