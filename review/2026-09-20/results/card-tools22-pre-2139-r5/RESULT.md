RESULT tools22-pre-2139-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2139 at head 4ae65b22845b: work: E01-F02 read bounds solver and unknown node refusal
PREREAD 2139 claims=3 proven=2 unproven=1 defects=0 high=0

PR 2139
HEAD 4ae65b22845b8523a2a3f6c76e131fb95379ff83
BASE emma/turn-unmet-reds-green
MERGE-BASE 31fabcdff9e79145ea78a52038e542673704bb11
BEHIND 0
FILES 12 production, 2 test
LINES +738 -113

1. Require max-bytes, max-depth, and max-nodes bounds on every file read; missing bounds refuse with "refusing to guess" (exit 2), exceeded bounds refuse naming the bound and file (exit 2)
2. Unknown node types must be refused at node-add and make-seed-state with "rule 1: unknown node type ..." message
3. Unknown keys on nodes must be preserved through state transitions and operations

PROVEN-BY lisp/nova-work/tests/replays-e01-f02-read-bounds.lisp:1 TestE01F02RequireMaxBytesMaxDepth — asserts missing/exceeded bounds refuse at exit 2
PROVEN-BY lisp/nova-work/tests/replays-e01-f02-read-bounds.lisp:230 TestE01F02PreserveUnknownKeysAndRefuse — asserts unknown node types refused, unknown keys preserved
UNPROVEN: session.lisp max-bytes/max-depth/max-nodes session fields — test asserts session stores bounds but does not verify bounds are enforced across all session read surfaces

DEFECTS none

QUESTIONS
1. Does the session-start function in session.lisp persist bounds to the session's cache file, or are bounds only in-memory?
2. What is the intended behavior when state-load receives bounds that are tighter than the manifest's member counts?
3. How should bounded-read-file handle a file that is valid under session bounds but the caller passes stricter explicit bounds?

Left owed
None: all production files in the diff read in full; new test file read in full; existing test file slice-09-state-export-replays.lisp read in full.

git status --short
git rev-parse HEAD
4ae65b22845b8523a2a3f6c76e131fb95379ff83
