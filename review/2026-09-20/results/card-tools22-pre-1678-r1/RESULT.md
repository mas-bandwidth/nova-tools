RESULT tools22-pre-1678-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1678 at head 1e7eb881cec2: nova-work E07.2: the durable long-operation record and the recovery reconciliation
PREREAD 1678 claims=9 proven=9 unproven=0 defects=0 high=0
PR 1678
HEAD 1e7eb881cec2ac5d9c582886bc45defab3c54af3
BASE rowan/work-e06e07
MERGE-BASE 39eecd6660d7946aa08967d601a1607327f92c17
BEHIND 0
FILES 2 production, 1 test
LINES +615 -3

CLAIMS
1. Long operations return an operation id and state immediately, with the id durable before being printed. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:39 "operation-id-is-durable-before-it-is-printed"
2. Accept record is written to journal and made durable before the verb replies. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:39 "operation-id-is-durable-before-it-is-printed"
3. A crash between accepting work and acknowledging it never leaves a caller holding an id the restart never heard of. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:62 "operation-id-is-durable-before-it-is-printed"
4. An id the journal doesn't hold returns "OPERATION FAIL id=<id> op=- state=-: no such operation" at exit 2. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:106 "an-id-no-journal-holds-has-a-line-of-its-own"
5. Cancellation is a request with its own acknowledgement and final disposition, deduplicated like other requests. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:133 "a-cancel-replayed-twice-cancels-once"
6. Cancellation cannot erase an accepted mutation or undo an external effect. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:154 "a-cancel-replayed-twice-cancels-once"
7. Recovery reconciles interrupted operation ids and their external outcomes before anything is retried. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:175 "recovery-reconciles-interrupted-operations"
8. Reconciliation records that the restart accounted for the id, but does not move its state. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:193 "recovery-reconciles-interrupted-operations"
9. External outcome is marked uncertain unless the owning engine had already recorded a known one. PROVEN-BY lisp/nova-work/tests/replays-operation-records.lisp:197 "recovery-reconciles-interrupted-operations"

DEFECTS none

QUESTIONS
1. Where is read-restricted defined (used in %parse-operation-line at operation-records.lisp:83)?
2. How does journal-accept behave if the journal is unavailable during %operation-write?

Left owed
None - all changed files were read

git status --short
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1678-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1678-r2	1	2026-09-20T19:14:22Z	2026-09-20T19:16:07Z	0	inception	mercury-2.5	227946	374	0	9458	3817	0.0098
