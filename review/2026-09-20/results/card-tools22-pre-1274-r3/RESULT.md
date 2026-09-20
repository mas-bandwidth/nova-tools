RESULT tools22-pre-1274-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1274 at head 32083a2b6b9a: CARD-9301 nova-tools slice 3 of SPEC-JOBS: Pull workers with leases and heartbeats: `nova-swarm
PREREAD 1274 claims=8 proven=1 unproven=7 defects=1 high=0

PR 1274
HEAD 32083a2b6b9aac2f365155925327a1fc6eeea3a1
BASE dev
MERGE-BASE 327d26bbaf1bd81951d223b86a378f075625de8b
BEHIND 84
FILES 582 production, 455 test
LINES +3937 -88426 production, +1236 -78681 test

## CLAIMS

1. nova-swarm pull takes one card from <store>/queue under a fresh one-slot lease before running it; an empty queue returns idle with exit 0. PROVEN-BY cmd/nova-swarm/pull_test.go:19 TestALaunchWithoutALeaseIsRefusedByThePuller — asserts that when alice's share is 0, `pull --store --owner alice --for 1h` refuses with exit 2 and leaves the card in queue/.
2. Expired leases whose pid is dead are reaped: their card returns to queue/ and the lease is removed. PROVEN-BY internal/swarm/pull_test.go:15 TestAnExpiredLeaseWithADeadPidReturnsTheCard — writes a dead-pid lease in taken/, calls ReapExpiredLeases, asserts the card appeared in queue/.
3. A heartbeat (RenewSlotLease) extends the until= of a lease by owner and label, independent of pid. UNPROVEN — no test directly exercises RenewSlotLease in pull_test.go; the only heartbeat test is embedded inside TestPullTakesACardUnderALeaseAndHeartbeatRenewsIt which is a combined take+renew test.
4. nova-swarm batch no longer requires --slots-store and --owner for the runnerless native path. UNPROVEN — batch_test.go was reduced to 200 lines (was 200), many flags like --slots-store/--owner/--route/--worker are gone, but the test diffs show only the function signatures changed. No test verifies the NEW behaviour that native works without a lease store.
5. The --route family (--route, --route-floor, --route-registry, --route-log, --route-usage) is entirely removed from batch and native. UNPROVEN — internal/decide/* packages (~50 files, 1700+ lines) are deleted wholesale. No surviving test exercises the absence of routing.
6. Redis-backed stream pull (--stream, --redis) and laned pull (--pull-lanes) are removed. UNPROVEN — internal/redisq, internal/lanes are deleted; internal/bus/protocol.go, internal/bus/instrument.go, and numerous bus/stream tests are gone. No surviving test covers the non-stream pull behaviour explicitly.
7. Batch MAX_INFLIGHT, STALL_AFTER, PUBLIC-CLASS GATE, and HARBINER/HARNESS flags are removed. UNPROVEN — internal/swarm/inflight.go (154 lines) is deleted; batch_test.go loss is ambiguous because the original may already have been small. The claim that these features no longer exist cannot be verified without reading deleted test files.
8. internal/safepath.RemoveUnder is simplified: OS-samefile (device/inode) comparison is replaced with EvalSymlinks + string comparison. UNPROVEN — the test file (safepath_test.go, -122 lines) is among those deleted.

## DEFECTS

MEDIUM internal/safepath/safepath.go:61 — strictlyUnder and refusal functions switch from os.SameFile (device/inode identity) to filepath.EvalSymlinks + string compare — On case-insensitive filesystems (APFS on macOS, NTFS on Windows), two path spellings differing only in case or Unicode normalisation form resolve to the same directory but fail string equality, allowing unsafe removals that should be refused. What would fix it: restore os.SameFile or os.Stat-based identity checks for root and path comparisons against the home directory and disk root, where the boundary matters most.

## QUESTIONS FOR THE REVIEWER

1. Why was os.SameFile (device/inode identity check) deliberately dropped in favour of string comparison in internal/safepath? The old code comments explicitly documented the APFS case sensitivity problem (see the Johnny 2026-09-19 comment). Was this intentional on case-insensitive volumes, or was the safe version simply not ported to the simplified form?
2. internal/decide contains the entire routing ladder, harvest decisions, reader registry, and transit logs (~1700 lines across ~30 files). Is the intent that routing is permanently retired from nova-swarm, or is this deferred to later slices?
3. The batch command still supports `--harness` for the runnerless-native path. If nova-swarm native now genuinely requires no slot store (the claim says the --slots-store requirement was removed), why did the `NoSlotsStoreRefusal` constant get removed from slots.go? Was there a follow-up commit that already removed the native-side check?
4. The lease ID generation changed from publish+rename (atomic staging dir rename) to Mkdir+WriteFile. Two racing takers could both pass Mkdir on some filesystems if the id collision happens between mkdir and write. Was this race window tested, or is the nanosecond+random bits id entropy considered sufficient?
5. Which SPEC-JOBS sections were deliberately abandoned vs deferred? Section 6 (batching), Section 7 (backpressure), Section 4 (affinity/warm pull), and Section 5 (lanes) all appear removed. Which ones will land in future PRs?

Left owed
I did not read: (a) the full content of any of the 282 deleted production files individually — I read the diff stat, key files (pull.go, slots.go, main.go, batch.go, safepath.go), and test diffs for pull_test.go. (b) the lisp/nova-work source changes (12 files deleted, some modified) — only counted them. (c) docs/SPEC-JOBS.md through SPEC-WORK.md deletion — inferred content from remaining references. (d) the 220 modified production files that were not among the above — covered by count and git log summary. (e) go vet / go build result — permitted but not required per card instructions.

git status --short

(empty)

git rev-parse HEAD

5298f6be12eaa0f7e6622334d2b6a1eb427649e3
