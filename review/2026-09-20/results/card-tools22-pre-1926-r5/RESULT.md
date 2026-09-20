RESULT tools22-pre-1926-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1926 at head 3b8bd8f1b5f7: nova-swarm slots: lock the take path's read-count-write so concurrent takes never overgrant
PREREAD 1926 claims=6 proven=5 unproven=1 defects=0 high=0

PR 1926
HEAD 3b8bd8f1b5f7f1b56cd36c43c6d996271771ba57
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE dbfb27ccdd132fccdbd1698f097542be944e5eb5
BEHIND 14
FILES 1 production, 1 test
LINES +115 -0

CLAIMS

1. TakeSlotLeases acquires an exclusive flock(LOCK_EX) advisory lock on <store>/take.lock before reading or modifying any slot state, and releases it via defer on all exit paths. PROVEN-BY internal/swarm/slots.go:364-368 TakeSlotLeases calls takeSlotsLock(store, SlotsWait) immediately after input validation, with defer release() holding the lock through loadSlotShares, ReadDir, the held+k check, Mkdir/WriteFile loop, and all error returns.

2. takeSlotsLock polls every 15ms for up to 10 seconds (SlotsWait) before giving up and returning an error that propagates from TakeSlotLeases. UNPROVEN — the constants SlotsWait and lockPoll exist in lock.go but no test exercises the timeout path or verifies the poll interval.

3. The lock file lives at <store>/take.lock, outside the leases directory <store>/slots/, so no walk of <store>/slots/confusion ever mistakes it for a lease directory. PROVEN-BY-EXISTING internal/swarm/slots.go:306 filepath.Join(slotStoreDir(store), ...) where slotStoreDir = store + "/slots"; takeSlotsLock uses filepath.Join(store, "take.lock").

4. Concurrent TakeSlotLeases calls for the same store are serialized: two takers cannot simultaneously read the same pre-grant count, both pass held+k ≤ share, and both grant, causing oversell. PROVEN-BY internal/swarm/slot_take_race_test.go:44 TestConcurrentTakesNeverGrantMoreThanTheShare drives 64 goroutines against a store with capacity=8 and racer share=8, asserts exactly 8 granted and 56 refused.

5. All 56 refusal responses carry the identical final-state line "SLOTS REFUSED owner=racer want=1 held=8 share=8 free=0 holders=racer:8", proving the lock prevents inconsistent held/free scatter among refusals. PROVEN-BY internal/swarm/slot_take_race_test.go:57-60 each refused goroutine asserts its formatted refusal string equals the exact expected value verbatim.

6. Remove under reap of half-written directories (no parseable lease file) and expired-dead-pid leases still works inside the locked region because safepath.RemoveUnder is called while holding the lock. PROVEN-BY-EXISTING internal/swarm/slots.go:315,319 — these reaps happen between takeSlotsLock acquisition and defer release in the PR head.

DEFECTS none

QUESTIONS FOR THE REVIEWER

1. Why choose a global per-store flock instead of per-owner flocks? A global lock serializes even independent owners (alice taking does not block bob taking), but per-owner would allow more concurrency. Is the simplicity of one lock worth the throughput trade-off, or should there be a per-owner lock later?

2. What happens if a nova-swarm process crashes while holding take.lock? flock locks are released when the holder exits, so the next caller will succeed after retrying. But could a crash during WriteFile leave a partial lease directory that confuses subsequent reads until the next TakeSlotLeases invocation triggers reap?

3. The comment says "another nova-swarm holds %s" in the timeout error, implying inter-process contention. In practice, are multiple nova-swarm processes competing for the same store common enough that 10 seconds is necessary, or could this be shorter?

Left owed
I did not read the full diff of every file in the repository to verify no other callers were missed; I only checked for TakeSlotLeases callers with grep across all non-test .go files. I also did not run `go test` on internal/swarm or the new race test — only `go build` and `go vet` of both packages. Finally, I did not inspect whether the flock-based lock interacts differently with NFS vs local filesystems compared to the pool-level TakeLock used elsewhere in slot.go.

git status --short
git rev-parse HEAD

(empty)
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1926-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1926-r1	1	2026-09-20T19:25:16Z	2026-09-20T19:41:59Z	0	openrouter	qwen/qwen3.7-flash	248760	16461	0	3672960	9264	0.0922
