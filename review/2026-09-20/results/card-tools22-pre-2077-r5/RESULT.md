RESULT tools22-pre-2077-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2077 at head 559cacf627ad: nova-pulse launch: a launch without a bench slot lease is refused (#1903)
PREREAD 2077 claims=9 proven=8 unproven=1 defects=2 high=0
PR 2077, HEAD 559cacf627add9744853fa98355a1e470f011b7f, BASE dev, MERGE-BASE a3abdd4ad6dd0a0427f131ad6b71a9e10f07308e, BEHIND 3, FILES 16 production, 10 test, LINES +516 -78

1. `nova-pulse launch` refuses (exit 2) when --slots-store or --owner is missing, printing swarm.NoSlotsStoreRefusal, and starts no batch.
   PROVEN-BY internal/pulse/launch_slot_lease_test.go:45 TestLaunchWithoutASlotsStoreRefuses
2. `nova-pulse launch` with --slots-store and --owner passes both flags through to the nova-swarm batch argv and succeeds (exit 0, PULSE OK).
   PROVEN-BY internal/pulse/launch_slot_lease_test.go:97 TestLaunchWithSlotsStorePassesFlagsToBatch
3. The cmd/nova-pulse launch CLI verb also refuses without --slots-store/--owner.
   PROVEN-BY cmd/nova-pulse/launch_slot_lease_test.go:20 TestLaunchCmdWithoutSlotsStoreRefuses
4. The cmd/nova-pulse launch CLI verb with --slots-store/--owner passes them to the batch argv.
   PROVEN-BY cmd/nova-pulse/launch_slot_lease_test.go:53 TestLaunchCmdWithSlotsStoreStillWorks
5. `nova-swarm batch --runner` naming `nova-native-runner.sh` refuses without the lease, the same way runnerless batch does.
   PROVEN-BY internal/swarm/batch_native_runner_lease_test.go:24 TestBatchNativeRunnerWithoutASlotsStoreRefuses
6. `nova-swarm batch` with the native runner and a store forwards the store and owner as NOVA_SWARM_SLOTS_STORE and NOVA_SWARM_SLOT_OWNER env vars.
   PROVEN-BY internal/swarm/batch_native_runner_lease_test.go:77 TestBatchNativeRunnerWithSlotsStoreStillRuns
7. A batch with a --runner that is not nova-native-runner.sh is not held to the lease requirement.
   PROVEN-BY-EXISTING Internal/swarm batch tests with custom fake runners (e.g. TestBatchDistributes, etc.) pass without --slots-store/--owner.
8. `nova-pulse harvest`'s relaunch re-reads the lease from the pulse record file and passes it to the launch subprocess.
   PROVEN-BY internal/pulse/review1818_test.go:356 TestRelaunchPassesTheWidthAndDeadlineThePulseRanWith — asserts --slots-store and --owner in the relaunch argv.
9. The pulse record (`pulses/<id>.tsv`) stores the slots-store and owner alongside the width and deadline.
   UNPROVEN No test calls record() and then verifies the file content; the format is exercised only through the test helper writePulseTable.

DEFECT low cmd/nova-pulse/launch_slot_lease_test.go:17 — Comment says "so does the flag parser, so a caller who never reaches Launch still sees native's one line" but --slots-store and --owner are registered with default "" and the flag parser does NOT refuse them. — Matters because a reader trusts the comment about where refusal happens. — Fix: correct the comment, or add flag-level Required validation.

DEFECT low internal/pulse/review1818_test.go:356 — Asserts only substring presence for "--slots-store" and "--owner" in the relaunch argv, not their values. A regression that passes empty or wrong values would stay green. — Matters because the test would not catch a value-dropping regression. — Fix: assert the full "--slots-store <path>" and "--owner <name>" strings with their expected values.

1. Should the lease check in launch.go also gate the --max processing (which runs before the lease check and does emit a PULSE NOTE line to stderr before returning), or is it intentional that a caller can get a --max note without the lease flags?
2. The launchesNative function in batch.go uses basename matching on the runner path; was a symlink or wrapper script named differently (e.g. a user's `run-cards.sh` that internally execs nova-native-runner.sh) considered, and is it intentionally not recognized as native?
3. The pulse record format now has 6 tab-separated fields; what happens to pulse records created in the window between #1819 (which added fields 3-4) and this PR (fields 5-6) — they are silently skipped by readPulseShape's len(p)<6 guard, which is correct, but is there a mechanism to identify or recover them?
Left owed I read every production and test file the diff touches. I did not read docs/CLI.md, docs/SPEC-PULSE.md, docs/SPEC-SWARM.md, docs/TESTS.md, docs/spec-pulse/02-the-rules-numbered.md in full — only the diff hunks — because the structure of these docs is prose and the git diff shows the exact inserted and removed lines, which is sufficient.
git status --short: (nothing)
git rev-parse HEAD: 559cacf627add9744853fa98355a1e470f011b7f===FILE=== card-tools22-pre-2077-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2077-r4	1	2026-09-20T19:34:12Z	2026-09-20T19:47:53Z	0	openrouter	deepseek/deepseek-v4-flash	187756	6084	0	479488	14047	0.0115
