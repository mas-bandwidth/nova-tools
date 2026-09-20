RESULT tools22-rule-toolwork-7 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 7 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:947 rule 7
PKG internal/docs
ASK The code must ensure every `$ ` line in fenced blocks of README.md, docs/USAGE.md, docs/CLI.md's `### First run` sections, and docs/nova-swarm-quickstart.md, and every `example:` line from `help`, is either executed by a test through onboarding.Compare or listed with its reason in `internal/ci/testdata/unexecuted_examples.txt`, and the list must only shrink.

Nothing in the tree implements this rule:
- `internal/ci/testdata/unexecuted_examples.txt` does not exist
- No test named `unexecuted-examples-only-shrink` exists (grep for `unexecuted` in `*_test.go` found nothing aside from unrelated comments in mutate_test.go and main_test.go)
- `grep -rn 'unexecuted' --include='*.go' . | grep -v SPEC-TOOLWORK.md | grep -v SPEC-TEST.md | grep -v mutate_test | grep -v main_test` returns nothing
- No mechanism counts or tracks `$ ` lines from those documents against an allowlist
- The work plan explicitly lists this as future builder work at T23 (#1667): "the unexecuted-examples count and its shrink-only list" under §7 rules 5, 7

The comparator exists (`internal/onboarding/transcript.go:627 Compare`) but is only used for `docs/TESTS.md` transcripts, not for the documents rule 7 covers. The red test `unexecuted-examples-only-shrink` has no corresponding Go function anywhere in the tree, confirming nothing enforces this yet.

Left owed