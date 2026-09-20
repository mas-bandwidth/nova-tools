RESULT tools22-pre-1475-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1475 at head ff6c60fadbaf: docs: make first-run setup explicit
PREREAD 1475 claims=3 proven=0 unproven=3 defects=0 high=0
PR 1475
HEAD ff6c60fadbaf91aa7f33a29bf46975d9c04dc19d
BASE dev
MERGE-BASE 39aa05f13a7fa054487ac88a9ecd98929b762b7b
BEHIND 67
FILES 0 production, 2 test
LINES +22 -5

CLAIMS
1. docs: make first-run setup explicit — Clarify first-run commands in TESTS.md by adding missing mkdir and plan setup steps
2. docs: clarify plan expansion input — Explain that plan expand requires nodes to declare :output in addition to :id and :kind
3. first-run guidance concise and track tool defects — Refine first-run documentation for nova-tokens, nova-work, and related tools

DEFECTS none

QUESTIONS
1. The example-gold.tsv comment change from "six rows over six files" to "seven rows over the fixture corpus" — has the fixture corpus actually changed since the last update, or is this stale documentation?
2. For nova-self-talk: the note about floors check states a core and source pair is needed — should this guidance also appear in the nova-self section documentation?
3. The cut/pool examples reference #1503 for running from checkout root — does that issue exist and what does it contain?
4. nova-tokens fold first-run shows mkdir -p ./out but no other tools show output directory setup — is this inconsistent or intentional?

Left owed
I did not read the full nova-work firstrun_test.go or other test files mentioned in documentation since the diff only changes docs/TESTS.md and cmd/nova-memory/testdata/example-gold.tsv

git status --short (job root)
HEAD: repo not a git working tree (HEAD ambiguous)
