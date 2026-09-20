RESULT tools22-pre-1872-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1872 at head 968a1d50fd68: docs/CLI.md: nova-swarm lint, the verb the synopsis never listed, held against the flags it reg
PREREAD 1872 claims=2 proven=0 unproven=1 defects=0 high=0
PR 1872
HEAD 968a1d50fd683781b227975cb53fb25d98fc1241
BASE rowan/tools15-c3-1835-cli-land
MERGE-BASE b342bc91a6e09a310c31ea012464dad8598f78ef
BEHIND 0
FILES 1 production, 1 test
LINES +112 +0

CLAIMS
1. docs/CLI.md now documents the `nova-swarm lint` verb with flags `--card`, `--rules`, `--typed`, `--trust`, and `--max`
2. docs/CLI.md's synopsis for `nova-swarm lint` names every flag found in cmd/nova-swarm/lint.go

UNPROVEN: internal/docs/cli_swarm_lint_flags_test.go:55 (expects 4 flags, cmdLint registers only 1: card)

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. What is the relationship between `maxFlag(f.fs)` in cmdLint and the `--max` flag documented in CLI.md?
2. Why does the test assert there should be at least 4 flags when cmdLint appears to register only one directly?

Left owed
- cmd/nova-swarm/lint.go: full read (to understand flag registrations)
- docs/CLI.md: full read (to verify synopsis line)
- internal/docs/cli_swarm_lint_flags_test.go: full read (test that this PR adds)
- internal/swarm/lintheader.go: not read (referenced in comments but outside diff scope)

git status --short
git rev-parse HEAD d576bf6bbabb39068096a97b4560de9b5e245970
