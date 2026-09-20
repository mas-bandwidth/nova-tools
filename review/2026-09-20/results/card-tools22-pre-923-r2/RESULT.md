RESULT tools22-pre-923-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#923 at head fd5cd82006fd: CARD-8426 nova-tools #607 fixed with its red test first: Benches: remote deadline/idle (SIGTERM
KIND: transcript-test
DEADLINE: 2100
LEG: go
PATHS: the files the pull request changes, and nothing else
FILES: 0
TEST: none
MODE: read
TURNS: 35
SOURCE: mas-bandwidth/nova-tools#923
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

PREREAD 923 claims=3 proven=3 unproven=0 defects=0 high=0

PR 923
HEAD fd5cd82006fdc5306464cba21f618980432d29b7
BASE dev
MERGE-BASE 31e351956a59a63e3da1b323abecb63da7b92cdd
BEHIND 39
FILES 2 production, 1 test
LINES +398 -19

1. Remote cards at the batch deadline get a full kill sequence: SIGTERM to the ssh child's local process group, then `ssh <host> pkill -TERM -g <pgid>` using the pgid from the native's `RUN pgid=` line, then `-KILL` after `remoteKillDelay` (5s).
   PROVEN-BY internal/swarm/benchdeadline_test.go:71 TestBatchRemoteDeadlineKillsGroup — asserts the fake ssh received pkill -TERM -g 424242 and pkill -KILL -g 424242, and the card scores ABSTAIN reason=deadline.

2. The remote idle watch measures the native log's byte size via `ssh stat -c %s <path>`, polling at most once per `--idle/3` cadence. An unreachable bench (ssh 255) leaves the card unknown until the deadline, then scores `ABSTAIN reason=bench-unreachable`.
   PROVEN-BY internal/swarm/benchdeadline_test.go:107 TestBatchRemoteIdleWatchReadsSize — asserts the idle poll used stat -c %s, was called at most 3 times (under --idle/3 cadence), and the unreachable card scores bench-unreachable.

3. The BATCH packet gains `benches=<n>` when `--bench` is used, and one `BENCH <name>` line per named bench before the first card line; each BENCH line reports slots, done, abstain, and usage token sums (in/out are `-` when no usage.tsv was pulled).
   PROVEN-BY internal/swarm/benchdeadline_test.go:153 TestBatchLineHasBenchLines — asserts the BATCH line has benches=2, two BENCH lines appear before any card line, and done/abstain counts are per-bench.

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. SPEC-SWARM "Benches" defines the remote kill protocol: does the `remoteKillDelay` constant (5s) match the spec's timeout, or is it a pragmatic choice awaiting a spec update?
2. When the native run never prints `RUN pgid=` (empty pgid), killRemote only SIGTERM the local ssh group. Is this the intended fallback, or should it be a hard error during card launch?
3. The BENCH output uses `-` for in/out tokens when no usage.tsv was pulled. If only some cards on a bench have usage.tsv, does the bench line aggregate correctly or does it need a spec clarification?

Left owed
None — all 3 production files and 1 test file were read fully.

git status --short
git rev-parse HEAD
fd5cd82006fdc5306464cba21f618980432d29b7
