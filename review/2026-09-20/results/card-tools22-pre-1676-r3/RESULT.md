RESULT tools22-pre-1676-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1676 at head 94048d56482e: nova-work E02.1 (cancel): a cancellation is a request on the durable journal
PREREAD 1676 claims=13 proven=13 unproven=0 defects=0 high=0

PR 1676, HEAD 94048d56482ed81aed5637e6760e1fbf165b7c58, BASE rowan/work-e02-durable-accept, MERGE-BASE 61248cb63b8c1d47148dec0d94ac8142005cbb68, BEHIND 0, FILES 3 production, 1 test, LINES +429 -9

1. A cancellation is a request of its own with its own acknowledgement and final disposition, deduplicated on the same bounded durable journal — PROVEN-BY replays-e02-cancel.lisp:75 cancel-is-a-request-not-an-erasure-on-the-journal — cancel record on disk, acknowledged, replayed twice cancels once
2. A cancel record is told from an accept record by its :record tag — PROVEN-BY replays-e02-cancel.lisp:265 cancel-record-p accept — asserts the record under the colliding request id is still the accept record
3. A cancellation erases no accepted mutation: the operation's accept record stays on the journal — PROVEN-BY replays-e02-cancel.lisp:108 accept-record-not-erased — checks accept record kind and request still match
4. A cancel replayed twice cancels once: same request id answers the recorded disposition and writes nothing — PROVEN-BY replays-e02-cancel.lisp:118 replay-cancels-once — journal length unchanged, replayed flag set
5. A different request id is a fresh acknowledgement of the operation's final disposition, not a second cancellation — PROVEN-BY replays-e02-cancel.lisp:137 fresh-request-id-re-acknowledges — fresh request acknowledged, exits 0, returns same disposition
6. An uncertain external effect is never claimed cancelled — PROVEN-BY replays-e02-cancel.lisp:177 an-uncertain-external-effect-is-never-claimed-cancelled — disposition is :uncertain, not :cancelled
7. A cancellation survives a restart — PROVEN-BY replays-e02-cancel.lisp:218 a-cancellation-survives-a-restart — cancelled operation still :cancelled after reopen, un-cancelled op still :queued
8. The dedup predicate is the journal's, not an unbounded resident map — PROVEN-BY replays-e02-cancel.lisp:232 restart-dedup-from-journal — replay after restart writes no second record
9. Cancelling an id no journal holds has its own line (OPERATION FAIL no such operation, exit 2, nothing written) — PROVEN-BY replays-e02-cancel.lisp:253 cancelling-an-id-no-journal-holds-is-its-own-line — checks refusal line, exit 2, journal unchanged
10. A cancel request id reused with a changed target operation refuses — PROVEN-BY replays-e02-cancel.lisp:299 changed-target-refuses — refusal with kernel's reason text, exit 2, no record, target state unchanged
11. A cancel request id reused with a changed actor refuses — PROVEN-BY replays-e02-cancel.lisp:313 changed-actor-refuses — refusal with kernel's reason text, exit 2, no record
12. A stamp-only retry (same semantic payload, later timestamp) is a replay, not a new payload — PROVEN-BY replays-e02-cancel.lisp:324 stamp-only-retry-replays — exit 0, replayed flag, no new record
13. A cancel request id colliding with another record KIND on the same journal is a conflict — PROVEN-BY replays-e02-cancel.lisp:339 cross-record-kind-collision-refuses — refusal, exit 2, no record, accept record under colliding id unchanged

DEFECTS none

1. The identity predicate for cancel replay compares operation id AND author, but excludes :stamp. Is there any caller that would need a different identity scope, or is every call with the same request id, target, and author always the same cancellation regardless of observation time?
2. registry-apply-cancel-record silently returns NIL when the cancel's target operation is not yet in the registry. During reconciliation the journal order guarantees accepts precede their cancels, but is there any path (compaction, manual intervention, cross-session interleaving) where a cancel's accept could arrive after the cancel in the journal iteration order?
3. The "reused with a different payload" refusal line carries the operation id (not the colliding request id) in its id= field. Is that line parsed by any tool that expects the request id there, or is it purely a human-readable diagnostic?

Left owed: read every line of every changed file.

(nothing)
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1676-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1676-r2	1	2026-09-20T19:19:47Z	2026-09-20T19:28:20Z	0	openrouter	deepseek/deepseek-v4-flash	120346	5611	0	680448	15945	0.0107
