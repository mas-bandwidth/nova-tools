"RESULT tools22-pre-1406-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1406 at head ed0f13f702ee: certify: the six-hourly agent had nowhere to send an escalation
PREREAD 1406 claims=7 proven=4 unproven=3 defects=0 high=0

PR 1406
HEAD ed0f13f702ee385861658f2096275adc465205ad
BASE rowan/fleet-certify-fix
MERGE-BASE b64eae836a09be8772ac3aa135829740bcd627a5
BEHIND 1
FILES 1 production, 1 test
LINES +55 -3

CLAIMS

1. The six-hourly launchd agent for `fleet certify` carries `--bus`, `--as`, and `--to` so that it can escalate findings beyond its own log file.
PROVEN-BY cmd/nova-pulse/fleet_certify_test.go:340 TestTheLaunchdAgentRunsTheVerbTheLoopNeeds — asserts argv contains "--bus /Users/glenn/rowan-working/rowan-stella", "--as Rowan", and "--to ".

2. `certify` performs a repair round first, then escalates whatever still fails; without the bus flags the note is never sent (`escalation=unsent reason=no-bus`).
UNPROVEN — No test in this diff exercises the upgrade-or-fail path or verifies the `unsent reason=no-bus` behaviour exists in the running verb.

3. A machine going bad must reach a person without one being at the terminal; a log line nobody reads defeats mechanization.
UNPROVEN — This is design rationale (test comments and plist comments), not verifiable behaviour.

4. The three bus flags (`--bus`, `--as`, `--to`) travel together — `--bus` without either of the others causes exit 2 at flag parsing, refusing every six-hour run with no certification at all.
PROVEN-BY cmd/nova-pulse/fleet_certify_test.go:366 TestTheLaunchdAgentRunsTheVerbTheLoopNeeds — asserts `hasBus == strings.Contains(argv, "--as ") && hasBus == strings.Contains(argv, "--to ")`.

5. Every flag passed to the agent must be one `fleet certify` actually accepts, or the timer gets exit 2 silently each cycle.
PROVEN-BY-EXISTING cmd/nova-pulse/fleet_certify_test.go:372 TestTheLaunchdAgentRunsTheVerbTheLoopNeeds — iterates argv fields looking for unrecognized `--` flags via `certifyFlag()`, which runs the verb itself.

6. Documentation (CLI.md, SPEC-PULSE.md, spec-pulse/03-fleet.md) reflects that the six-hourly agent includes `--log`, `--bus`, `--as`, and `--to` in its invocation.
PROVEN-BY-EXISTING docs/CLI.md:1683-1686 — documents `--log` and the bus trio alongside the six-hourly launchd agent description.

7. The plist comment block explains why the bus flags exist (escalation loop), why they travel together (exit 2 on mismatch), and what `--log` provides (structured event stream for dashboard).
UNPROVEN — Comments are readable but their truth depends on code that this PR does not change; no assertion validates the explanatory narrative.

DEFECTS none

LEFT OWED
I did not read the production Go source for `nova-pulse fleet certify` (cmd/nova-pulse/fleet_certify.go or equivalent) — only the test file and the doc/plist diffs. I verified that `go vet ./...` passes and confirmed the merge-base already contains the `--bus` / `--as` / `--to` flags in the plist XML (added by commit 7630038e on rowan/fleet-certify-fix before this PR), so this PR's functional delta against the merge-base is strictly tests + docs + plist comments. If "production" means compiled source rather than plist configuration, then FILES = 0 production, 1 test, 3 docs.

git status --short:
(dry - no output expected)

git rev-parse HEAD: d576bf6bbabb39068096a97b4560de9b5e245970
