RESULT tools22-pre-1635-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1635 at head d4676d3954ed: nova-swarm: a native budget nothing can observe is refused before anything is made (rule 13d, #
PREREAD 1635 claims=2 proven=2 unproven=0 defects=0 high=0

PR 1635
HEAD d4676d3954ed9952b4ddc43c8b27382f75c6a25f
BASE cap-slice1-tokens-flag
MERGE-BASE 2c3748deab751ca2d73e7aa97033fb2bb445eb7b
BEHIND 0
FILES 5 production, 4 test
LINES +413 -1

CLAIMS
1. A native budget nothing can observe is refused before anything is made (rule 13d)
   PROVEN-BY cmd/nova-swarm/native_budget_source_test.go:77 TestNativeRefusesABudgetNothingCanObserve - asserts that numeric --tokens with usage:none or no sqlite3 on PATH is refused at exit 2 with NATIVE REFUSED line, no directories created

2. --usage-interval flag is accepted with floor of 1s and ceiling of deadline (exclusive)
   PROVEN-BY cmd/nova-swarm/native_budget_source_test.go:149 TestNativeUsageIntervalFloorAndCeiling - asserts that intervals under 1s or >= deadline are refused at exit 2, no directories created

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The commit message references issue #1545 and rule 13d - what is the full text of rule 13d in the spec?
2. The test budgetWorker helper writes a fake key file - are there any security implications of hardcoding "a fake key" in tests?
3. The NativeBudgetSourceRefusal function has complex branching for cardBudgetFields - is this correctly handling the case where worker is nil but tokens is numeric?

Left owed
- Read docs/TESTS.md fully (added new test slice documentation)
- Read docs/CLI.md fully (added --usage-interval documentation)
- No production files left unread; test coverage appears complete
git status --short
git rev-parse HEAD d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1635-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1635-r2	1	2026-09-20T19:14:19Z	2026-09-20T19:17:16Z	0	inception	mercury-2.5	237841	505	0	53947	3791	0.0104
