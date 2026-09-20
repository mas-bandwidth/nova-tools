RESULT tools22-rule-toolwork-7-L947 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-TOOLWORK.md:947 rule 7
PKG internal/docs
ASK Every `$ ` line in fenced blocks of README.md, docs/USAGE.md, docs/CLI.md's `### First run` sections, and docs/nova-swarm-quickstart.md, and every `example:` line from every `help` output, must be either executed by a test through the transcript comparator or listed with a reason in `internal/ci/testdata/unexecuted_examples.txt`, which is shrink-only; a new unexecuted example fails the class test on the PR that adds it.
Verdict ABSENT:
  - File `internal/ci/testdata/unexecuted_examples.txt` does not exist
  - Function `onboarding.CompareTranscript` does not exist anywhere in Go sources
  - No test named `unexecuted-examples-only-shrink` or `TestEveryTranscriptIsExecutedLineForLine` exists
  - No code aggregates `$ ` lines across README.md, USAGE.md, CLI.md, or nova-swarm-quickstart.md
  - No code aggregates `example:` lines across all tools against a shrink-only list
  - SPEC-TOOLWORK.md itself confirms this is planned work: T23 (line 998, «builder: no kind yet») names "the unexecuted-examples count and its shrink-only list" as a future item
Grep searches run: `grep -rn "unexecuted" --include='*.go' .`; `grep -rn "unexecuted_examples" .`; `grep -rn "CompareTranscript" --include='*.go' .`; `grep -rn "TestEveryTranscriptIsExecuted" --include='*.go' .`; `ls internal/ci/testdata/` — none found.
Left owed: implementation of rule 7 (§7), provisioned as T23.