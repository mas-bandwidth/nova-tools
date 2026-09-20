RESULT tools22-pre-2044-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2044 at head 40e5d020b30a: roadmap: the sexp holds every nova-work criterion as a record; ROADMAP.md is a checked view
PREREAD 2044 claims=1 proven=0 unproven=1 defects=0

PR 2044
HEAD 40e5d020b30a7a025a65fb233aa4c29470494134
BASE dev
MERGE-BASE a3abdd4ad6dd0a0427f131ad6b71a9e10f07308e
BEHIND 3
FILES 2 production, 0 test
LINES +307 -64

1. Make docs/roadmaps/nova-work.sexp the primary source of truth for every acceptance criterion and add a build-failing parity check comparing the sexp's per-criterion verified counts and state sequence to ROADMAP.md

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. How do we ensure the sexp's per-criterion sequence order matches the exact sequence of checkboxes in ROADMAP.md's detail blocks when new criteria are added?
2. Should the parity script also verify that each criterion ID appears exactly once in both the sexp and the markdown file?
3. What is the process for updating the sexp when criteria states change (verified→unverified or vice versa)?
4. Does the build system invoke roadmap-parity.sh automatically during CI or only on manual invocation?

Left owed
None — read both modified files in full.

git status --short
git rev-parse HEAD 40e5d020b30a7a025a65fb233aa4c29470494134===FILE=== card-tools22-pre-2044-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2044-r4	1	2026-09-20T19:33:15Z	2026-09-20T19:42:48Z	0	inception	mercury-2.5	251554	543	0	88157	2670	0.0109
