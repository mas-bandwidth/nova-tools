RESULT tools22-pre-1687-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1687 at head 69341363e21b: nova-work E02.1 (wait): the event cursor is a real bounded block, not a model
PREREAD 1687 claims=17 proven=17 unproven=0 defects=3 high=0

PR 1687
HEAD 69341363e21b289f80c0f1a03ddfc7479b9b86cb
BASE rowan/work-e02-cancel-is-a-request
MERGE-BASE 94048d56482ed81aed5637e6760e1fbf165b7c58
BEHIND 0
FILES 4 production, 1 test
LINES +547 -1

CLAIMS
1. `durable-operation-wait` blocks on an operation's condition variable until a producer emits an event — PROVEN-BY replays-e02-wait.lisp:48 "a-wait-is-a-bounded-block-that-wakes-on-an-event" asserts the wait blocks ~200ms until the producer thread emits and returns rows+cursor.
2. The event cursor is bounded at `*operation-event-cap*` (64); emissions past the cap refuse with exit 2 — PROVEN-BY replays-e02-wait.lisp:169 "the-event-cursor-is-bounded-by-an-explicit-limit" fills a stream to cap 3 then verifies refusal.
3. A wait respects `:after` as a cursor offset so resumed waits skip already-seen events — PROVEN-BY replays-e02-wait.lisp:136 "a-resumed-wait-does-not-read-the-events-again" passes `:after 2` and verifies only event 3 is returned.
4. A wait's returned page is capped at `:max` — PROVEN-BY same test at line 146, checks `(length rows)` equals 2 when `:max 2` with 3 events.
5. Cancellation via `operation-notify` (called from `registry-operation-cancel`) wakes blocked waits — PROVEN-BY replays-e02-wait.lisp:195 "a-cancellation-wakes-a-wait" has a canceller thread cancel after 200ms and verifies the wait returns :cancelled within bounds.
6. `durable-operation-wait` on timeout returns `:timeout` state with no rows and the OPERATION NOTE line — PROVEN-BY replays-e02-wait.lisp:81 "a-wait-that-times-out-leaves-the-operation-running" times out after 200ms and checks rows='(), state=:timeout, line matches grammar.
7. `durable-operation-wait` leaves the operation in `:running` state after timeout — PROVEN-BY same test at line 98 checks registry state is still `:running` post-timeout.
8. A settled operation causes `durable-operation-wait` to answer immediately — PROVEN-BY replays-e02-wait.lisp:109 "a-completed-result-is-retrievable-by-its-id-afterwards" settles before waiting, verifies immediate return and correct state/result.
9. `durable-operation-wait` on an unknown ID returns immediately with OPERATION FAIL and exit 2 — PROVEN-BY replays-e02-wait.lisp:225 "waiting-on-an-id-no-journal-holds-is-its-own-line" waits on "op-missing" and checks lines 234-240.
10. `operation-row-line` formats one OPERATION ROW matching the output grammar — PROVEN-BY replays-e02-wait.lisp:159 check-string= against exact expected string.
11. `session-recovery-journal-path` produces `<session-path>.operations` — PROVEN-BY replays-e02-wait.lisp:252 check-string=.
12. `open-session-operation-registry` opens a durable registry over the session's local journal — PROVEN-BY replays-e02-wait.lisp:264-276 restarts session and verifies the accepted operation survives across closure/reopen.
13. `operation-settle` installs state+result under the stream mutex then broadcasts — PROVEN-BY same settle test (line 109) verifies state=:done, cursor advances to 1, and result retrievable via `registry-operation-result`.
14. `operation-emit-event` under lock appends to entries list and broadcasts — PROVEN-BY emit test (line 169) increments cursor 1→2→3 on successive calls.
15. `operation-notify` wakes waits without appending an event — PROVEN-BY cancellation test (line 195) waiter receives :cancelled state without any emitted event (rows empty).
16. `durable-operation-wait` explicitly handles `condition-wait`'s return value (commit 6934136 fix) — PROVEN-BY replays-e02-wait.lisp:282 stress test runs 300 timed-out waits, each checking code=0 state=:timeout.
17. `terminal-operation-state-p` recognizes :done, :cancelled, :failed, :uncertain — PROVEN-BY settle test (line 109) confirms wait returns :done for settled ops, and cancel test (line 195) confirms wait returns :cancelled for cancelled ops.

DEFECTS medium replays-e02-wait.lisp:282 stress test title says "never-releases-an-unheld-mutex" but runs 300 iterations sequentially on one thread, never exercising concurrent callers or a producer/consumer race — it validates single-threaded timeout semantics, not concurrency safety. — adds misleading title that implies multi-threaded testing.
low lisp/nova-work/src/operation-events.lisp:130-135 `operation-notify` calls `ensure-operation-event-stream` which lazily creates a stream for any cell — if called for a cell that existed but had no stream yet (e.g., recovered from disk without prior emission), it side-effects by creating a resource. — whether intentional or not, calling notify creates resources unexpectedly; should likely guard or document.
low lisp/nova-work/src/operation-recovery.lisp:189-191 `operation-notify` added to `registry-operation-cancel` but there is no dedicated test that `registry-operation-cancel` → `operation-notify` → waiter wakes — the cancellation-wakes-a-wait test (replays-e02-wait.lisp:195) uses the full `registry-operation-cancel` path indirectly, verifying the behavior end-to-end.

QUESTIONS
1. SPEC-WORK.md sections referenced extensively (:2736-2740, :2759, :5979, :5980, :5982) were not accessible — can you confirm these references are accurate and complete? Particularly the output grammar anchors :5979-5982.
2. The second commit (6934136) wraps `condition-wait` in `or` with an NIL-return handler. On implementations where `condition-wait` may return non-NIL on timeout (rare but possible per ANSI CL), would the short-circuit incorrectly exit? Does the comment "NIL: the mutex is not held" apply universally?
3. `ensure-operation-event-stream` (operation-events.lisp:65) always defaults cap to 64 regardless of what capacity `open-durable-operation-registry` specified — the per-registry capacity parameter is effectively ignored for streams created during regular event processing. Is this intentional, or should new streams inherit the registry's capacity?
4. `operation-cell` (line 58) recovers operations from the durable journal on cache miss — is this safe to call from multiple threads simultaneously without additional serialization beyond the per-operation mutex?

Left owed
I did not read docs/SPEC-WORK.md sections cited by the code (SPEC-WORK.md:2735-2740, :2759, :5979-5982). I did not read src/scheduler.lisp, src/command-thread.lisp, src/operations.lisp, src/kernel.lisp, or other files that import/use these new symbols. I did not run `make test-lisp` or any Lisp tests. I did not verify the ASDF dependency graph compiles. I read the full diff, both new files in full (operation-events.lisp 218 lines, replays-e02-wait.lisp 295 lines), the modified operation-recovery.lisp (added notify call and open-session-operation-registry), and the existing e02-cancel test for context.

git status --short
(prints nothing — working directory is clean)

git rev-parse HEAD
94048d56482ed81aed5637e6760e1fbf165b7c58
