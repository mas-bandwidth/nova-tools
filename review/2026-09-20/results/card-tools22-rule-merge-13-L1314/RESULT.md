RESULT tools22-rule-merge-13 sha=5298f6be12ea — does the code at this base do what docs/SPEC-MERGE.md rule 13 says?
CONFORMS cmd/nova-merge/verbs.go:86-197
SPEC docs/SPEC-MERGE.md:1927 rule 13
PKG internal/merge
ASK The quickstart verb must init a lane (with --repo and --base as required flags), then print status; the `### First run` section must also demonstrate adding an entry and printing status again — with every tool path passed as a flag and never given a default.

Deciding lines:
- `cmd/nova-merge/main.go:349-350`: `case "quickstart": return cmdInit(rest, stdout, stderr, deps, true)` — quickstart maps to init
- `cmd/nova-merge/verbs.go:86-88`: comment "quickstart is the same creation followed by a status"
- `cmd/nova-merge/verbs.go:89-197`: `cmdInit` validates all flags (--repo, --base, --lane-branch, --remote, etc.), creates the lane, prints `INIT OK`, then at line 194-197 calls `cmdStatus` when `quickstart=true`
- `cmd/nova-merge/verbs.go:194-197`: `if !quickstart { return 0 }; return cmdStatus(...)` — the status half of quickstart
- `cmd/nova-merge/pass.go:169-197`: `cmdStatus` opens the lane, folds records, prints `STATUS OK`
- `cmd/nova-merge/verbs.go:115-130`: every path is a flag, with `f.require` calls for --repo, --base, --lane-branch — no defaults
- `docs/CLI.md:1196-1207`: the `### First run` transcript shows quickstart (init+status), add, status — all with flags

GUARDED-BY
- `cmd/nova-merge/firstrun_test.go:315` `TestTheFirstRunPushesTheLaneBranchToTheRepository` — runs quickstart with all flags, asserts `joined=false` and the push
- `cmd/nova-merge/firstrun_test.go:234` `TestTESTSFirstRunIsWhatTheToolPrints` — executes the full `### First run` transcript (quickstart, add, status) line-for-line
- `cmd/nova-merge/contract_test.go:23` `TestEveryOtherVerbRefusesADirectoryThatIsNotALane` — demanded test 20: only init/quickstart creates; everything else refuses
- `cmd/nova-merge/contract_test.go:77` `TestAFlagIsRefusedOffTheVerbThatOwnsIt` — demanded test 20: --repo, --base, --lane-branch refused off verbs that do not own them

Greps run:
- `grep -rn "quickstart" --include='*.go' .` — found quickstart handler in main.go:349, cmdInit in verbs.go:89-197
- `grep -rn "demanded test 20\|TestDemanded\|Test20\|test_20" internal/merge/ --include='*_test.go'` — found reference in state_test.go:13 and contract_test.go:20,75
- `grep -rn "func cmdStatus" --include='*.go' cmd/nova-merge/` — found cmdStatus in pass.go:169
- `sed -n '1907,1953p' docs/SPEC-MERGE.md` — read rule 13 in context
- `read docs/CLI.md 1151-1241` — read the `### First run` section

Left owed: none
git status --short (inside repo/): nothing===FILE=== card-tools22-rule-merge-13/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-rule-merge-13	1	2026-09-20T20:27:46Z	2026-09-20T20:33:14Z	0	openrouter	deepseek/deepseek-v4-flash	117307	5238	0	1023488	6119	0.0123
