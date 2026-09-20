tools22-pre-867-r1
PREREAD 867 claims=19 proven=18 unproven=1 defects=1 high=1
PR 867, HEAD 40b6a90de3bc72fc40c3840c7b15862d3989b488, BASE dev, MERGE-BASE 7e7f185203, BEHIND 99, FILES 6 production, 3 test, LINES +2078 -14

1. `fleet add <name>` stands a machine up: installs Go toolchain, fetches runner tarball once, configures N runners with per-runner tokens carried over stdin (never logged), starts N services, writes machine row to fleet.tsv and runner rows to runner-services.tsv.
   PROVEN-BY internal/pulse/fleet_machines_test.go:54 TestFleetAddWritesTheRowsAndNeverLogsTheToken — asserts one FLEET line, token on stdin only, rows written, correct counts

2. `fleet add` run twice on one machine leaves one row per machine and per runner, not two.
   PROVEN-BY internal/pulse/fleet_machines_test.go:190 TestFleetAddIsRunTwiceOnOneMachine — asserts one machine and one runner after two calls

3. `fleet add` refuses with exit 2 and "FLEET REFUSED" when name, host, repo, queue, runners count, or service kind is invalid.
   PROVEN-BY internal/pulse/fleet_machines_test.go:219 TestFleetAddRefusesRatherThanGuess — table-driven test of six bad inputs, each asserting exit 2 and REFUSED

4. `fleet restart <runner>` restarts via systemctl for systemd kind, svc.sh stop/start for svc kind, or pkill/supervised loop for runsh kind.
   PROVEN-BY internal/pulse/fleet_machines_test.go:249 TestFleetRestartChoosesTheMechanismPerServiceKind — tests all three kinds with expected commands and forbidden cross-kind commands

5. `fleet restart` refuses with exit 2 when the runner has no row in runner-services.tsv.
   PROVEN-BY internal/pulse/fleet_machines_test.go:324 TestFleetRestartRefusesARunnerWithNoRow — asserts exit 2, no commands run, refusal naming RunnerServicesFile

6. `fleet probe <name>` reports toolchain version, key file presence (names only, never contents), and card-shaped job result under the cap.
   PROVEN-BY internal/pulse/fleet_machines_test.go:347 TestFleetProbeReportsTheThreeFacts — asserts PROBE line with go, keys/missing count, job=pass, secs, cap; asserts key names not paths; asserts non-zero exit when key missing

7. `fleet probe` refuses a machine with no row in fleet.tsv.
   PROVEN-BY internal/pulse/fleet_machines_test.go:393 TestFleetProbeRefusesAMachineWithNoRow — asserts exit 2, refusal naming FleetFile

8. `fleet add --dry-run` touches nothing and prints the plan.
   PROVEN-BY internal/pulse/fleet_machines_test.go:408 TestFleetAddDryRunTouchesNothing — asserts zero shell runs, zero tokens minted, no fleet.tsv written

9. `fleet phantoms` finds runners the API reports busy that no in-progress job names, and with --force-cancel cancels only the runs still holding their phantom jobs.
   PROVEN-BY internal/pulse/phantoms_test.go:80 TestPhantomsFindsTheBusyRunnerWithNoRunAndCancelsExactlyItsRuns — asserts exactly run 200 cancelled, correct PHANTOMS line counts

10. `fleet phantoms` without --force-cancel is a report that changes nothing.
    PROVEN-BY internal/pulse/phantoms_test.go:127 TestPhantomsWithoutForceCancelChangesNothing — asserts zero cancels, force=false in line

11. `fleet phantoms` does not call a working bench (busy on in-progress jobs) a phantom.
    PROVEN-BY internal/pulse/phantoms_test.go:146 TestPhantomsNeverCallsAWorkingBenchAPhantom — asserts phantom=0, zero cancels

12. `fleet phantoms` retries API calls once; if the second try also fails, it reports partial results.
    PROVEN-BY internal/pulse/phantoms_test.go:179 TestPhantomsRetriesOnceThenReportsWhatWasScanned — asserts one retry succeeds, two retries yield complete=false

13. `fleet phantoms` refuses rather than cancel on a guess when in-progress runs cannot be read twice.
    PROVEN-BY internal/pulse/phantoms_test.go:210 TestPhantomsRefusesRatherThanCancelOnAGuess — asserts exit 2 with REFUSED, zero cancels; asserts partial when live run jobs unreadable

14. `fleet phantoms` refuses without --repo.
    PROVEN-BY internal/pulse/phantoms_test.go:245 TestPhantomsRefusesWithoutARepo — asserts exit 2, refusal naming --repo

15. The fleet sub-verb dispatcher in fleet.go dispatches add, restart, probe, phantoms, survey, suspend, wake, reboot, secrets and refuses unknown sub-verbs.
    PROVEN-BY cmd/nova-pulse/fleet_test.go:18 TestFleetVerbDispatchesAndRefuses — table-driven tests of dispatch, unknown, and missing positional cases

16. The new fleet sub-verbs appear in help output.
    PROVEN-BY cmd/nova-pulse/fleet_test.go:60 TestFleetIsInTheHelp — asserts "nova-pulse fleet   add", "fleet   restart", "fleet   probe", "fleet   phantoms" in help

17. `fleet add --dry-run` from the CLI prints one FLEET line and writes no files.
    PROVEN-BY cmd/nova-pulse/fleet_test.go:38 TestFleetAddDryRunPrintsOneLine — asserts one line with dry-run=true and no fleet.tsv written

18. `fleet phantoms` takes no positional arguments.
    PROVEN-BY cmd/nova-pulse/fleet_test.go:28 (test case "phantoms with a stray argument") — asserts exit 2 with "takes no positional arguments"

19. ServiceRestarter.Restart (rule E2 reaper) and FleetRestart use the same restart mechanism via the shared restartCommand.
    UNPROVEN — no test asserts the two paths produce identical commands for a given RunnerService; the existing reaper test in runners_test.go uses fakeRestarter that records only names, not commands

DEFECT high internal/pulse/fleet_machines.go:restartCommand — shellQuote wraps tilde-prefixed target paths for the svc and runsh restart commands, preventing shell tilde expansion. For a runner added with the default pattern `~/runner-nova-tools-%d`, the svc restart produces `'~/runner-nova-tools-1'/svc.sh stop; '~/runner-nova-tools-1'/svc.sh start` — the tilde inside single quotes is literal, so the command looks for a `~` directory instead of $HOME. The runsh path has the same problem in pkill, cd, and the start loop. test: TestFleetRestartChoosesTheMechanismPerServiceKind uses full paths (`/Users/glenn/...`), not tilde paths, so it does not catch this. This means runners added with the default `~/runner-nova-tools-%d` pattern cannot be restarted by either fleet restart or the reaper. Fix: expand `~/` to `"$HOME"/` before shell-quoting, following the pattern already established in probeKeys: `if rest, ok := strings.CutPrefix(k, "~/"); ok { path = "$HOME"/` + shellQuote(rest) }`.

1. The `restartCommand` for "svc" changed `&&` to `;` between stop and start (old reaper code used `svc.Target + "/svc.sh stop && " + svc.Target + "/svc.sh start"`, new shared code uses `;`). `;` means start runs regardless of stop exit code; `&&` meant start only if stop succeeded. The reaper's behavior silently changed. Is this deliberate — do you want the runner to always attempt a start even when the stop flaked?

2. The `restartCommand` for "systemd" added `sudo -n` (old reaper used plain `systemctl restart`). If the reaper runs on machines where systemctl does not require sudo (e.g., user-level systemd), this will now fail with "sudo: unknown user." The fleet verb's SSH shell already uses `ssh bash -s` and the environment there may have sudo. Was this change to unify the reaper and fleet verb paths — and if so, are all machines where the reaper runs guaranteed to have passwordless sudo for systemctl?

3. The `fleet add` sub-verb has flags for --go, --runner-version, --user, --cores, --ram-gb, --os, --runner-dir, --card-slots, --network that are optional. Which of these do you expect to be required for a well-formed fleet.tsv row, and which are informational? (The `ReadFleet` function parses all 11 fields, but only Name and Host and ServiceKind are used downstream in restart/probe.)

Left owed: I read all 9 files in full. The diff is +2078 -14 lines; the new files are fleet_machines.go (855 lines), fleet_machines_test.go (401), phantoms.go (278), phantoms_test.go (253), fleet_machines.go cmd (151), fleet_test.go (81). I read every production file and every test file cover to cover. Nothing was left unread.

git status --short
?? nova-pulse
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-867-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-867-r1	1	2026-09-20T19:28:42Z	2026-09-20T19:46:21Z	0	openrouter	deepseek/deepseek-v4-flash	247725	8406	0	1497856	22730	0.0217
