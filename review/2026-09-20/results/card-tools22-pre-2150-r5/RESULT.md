RESULT tools22-pre-2150-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2150 at head 5233434fb754: Reap leftover supervise that ignores TERM (#1598)
PREREAD 2150 claims=7 proven=3 unproven=3 defects=0 high=0
PR 2150, HEAD 5233434fb754972ebf424935a6fdd44807d51e45, BASE dev, MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730, BEHIND 1, FILES 6 production, 5 test, LINES +308 -8

CLAIMS

1. A leftover, stopped supervise that ignores TERM is reaped by cleanup (SIGCONT then terminate-wait-kill).
   PROVEN-BY supervise_term_test.go:22 TestALeftoverSuperviseIsReaped — spawns an orphaned stopped supervise in a subtest that returns without noting the pid, asserts pid gone via waitGoneOrFail.

2. SIGTERM to a supervisor ends both the supervisor process and the harness process group it released.
   PROVEN-BY supervise_term_test.go:33 TestSuperviseEndsOnTERM — sends SIGTERM to a live supervise, waits for both supPID and jobPgid to be gone.

3. A supervisor exits when its pool root directory is removed (so a finished test cannot leave a supervisor alive).
   PROVEN-BY supervise_term_test.go:47 TestSuperviseEndsWhenItsPoolRootDisappears — removes the pool root and waits for the supervisor PID to be gone.

4. A test that notes a supervisor in its t.Cleanup waits until the supervisor's pid is confirmed gone (swarm.Reap + wait loop instead of KillGroup).
   UNPROVEN — noteSupervisor is used by recovery tests but none asserts the wait-loop behaviour specifically; the new supervise_term_test.go tests use separate helpers (orphanStoppedSupervise, orphanLiveSupervise).

5. A test binary with leftover children at exit is reported as failed and the children are reaped.
   UNPROVEN — TestMain in swarm_test.go calls leftoverChildPIDs after m.Run() and forces exit 1 if any are found, but no test exercises this path.

6. SPEC-SWARM rule 18 and the supervise description now list SIGTERM and pool-root-disappearance as job-ending conditions.
   UNPROVEN — documentation change; the behaviour itself is covered by claims 2 and 3, but no test verifies that the docs text is accurate.

7. A stopped supervise that ignores TERM because it was orphaned before identify (the CI leak shape) is the specific scenario the reap fixes.
   PROVEN-BY supervise_term_test.go:61 orphanStoppedSupervise — uses NOVA_SWARM_PAUSEPOINT=before-identify, PAUSE_AFTER_ORPHAN, and KILLPOINT=after-spawn to reproduce CI leftovers.

DEFECTS none

QUESTIONS

1. Do the non-unix dispatch_light and deadline_light also need the pool-root-gone check, or is that scenario only relevant when a supervisor runs in its own process group?

2. The `leftoverChildPIDs` function uses the short `comm=` field (truncated to 15 chars on Linux) to filter out the `ps` command; are there any legitimate test children whose comm matches "ps" (e.g., "psql", "psftp") that could be falsely ignored?

3. `reapLeftoverSupervise` walks both the bench dir (for supervisor.pid) and pool slots (for sf.Pid/Pgid/JobPgid) — is the pool-slots walk needed beyond what the bench-dir walk catches, or is it covering a supervisor that never wrote its pid file?

Left owed: I read all files in full. The diff is 11 files, 308 lines — I read every line of every changed file, and read the surrounding context of each change.

git status --short
(working tree clean)
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-2150-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2150-r1	1	2026-09-20T19:28:18Z	2026-09-20T19:42:11Z	0	openrouter	deepseek/deepseek-v4-flash	184646	9069	0	871424	13417	0.0144
