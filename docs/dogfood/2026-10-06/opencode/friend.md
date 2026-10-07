# nova-friend dogfood — opencode, 2026-10-06

Tool: nova-friend. Built in the staged checkout with `go build -o $JOB/bin/nova-friend ./cmd/nova-friend` (never the installed binary), at the staged commit `0655442166834fb6daa4130634c097248d18ef56`; it printed `nova-friend v1.0.1-0.20261007213845-065544216683 linux/amd64 go1.26.6`. Run cold, from the binary's own help (`nova-friend`, `nova-friend help`, `nova-friend <verb> -h`) and its page under `docs/`, on a Linux bench, with every verb at least once against one scratch store and one scratch directory. Every command below was typed with `S=<scratch-dir>`, `R=<scratch-redis>` and `HOME=$S` so `install`, `uninstall`, `ping-install` and `ping-uninstall` wrote under the scratch home and the store verbs read the scratch store.

## Findings

1. `nova-friend check --as the-friend`
   Printed:
   ```
   CHECK DAEMON friend=nova-friend agent=not-loaded pid=- status=none connection=none challenge=none pong_age= presence=down seen_age= proof=none proof_age=
   CHECK HARNESS friend=nova-friend harness= route= last= last_exit= failed_of_last20=0 deferred=0 broken= reason= session_live= queued=
   CHECK BUS friend=nova-friend real_since=0 last_real=-
   ```
   I expected the check to refuse an unknown friend with a remedial command to add it.
   Grade: URGENT

2. `nova-friend ping --as the-friend --to unknown --nonce abc123`
   Printed:
   ```
   PING REFUSED: unknown friend unknown; run: nova-config friend add unknown && nova-config friend apply
   ```
   I expected the ping to be refused since the friend is not in the roster, and the remedy should name the exact command to fix it.
   Grade: NEXT

3. `nova-friend install --as the-friend --harness opencode --dir $S/the-friend`
   Printed:
   ```
   INSTALL REFUSED: harness opencode has no deliver command; run: nova-friend help
   ```
   I expected the tool to refuse opencode since it cannot drive a session, but the remedy should name the harnesses it does support.
   Grade: NEXT

4. `nova-friend host --as the-friend --harness opencode --dir $S/the-friend`
   Printed:
   ```
   HOST REFUSED: harness opencode is not a terminal harness; run: nova-friend help
   ```
   I expected the host verb to refuse opencode since it is not a TUI harness like aider or grok.
   Grade: NEXT

5. `nova-friend check --harness opencode --as the-friend --dir $S/the-friend`
   Printed:
   ```
   CHECK FAIL harness=opencode stage=deliver why=harness has no deliver command
   ```
   I expected the delivery check to explain clearly that opencode cannot be driven because it has no deliver command.
   Grade: NEXT

6. `nova-friend run --as the-friend --harness opencode --dir $S/the-friend --dry-run`
   Printed:
   ```
   RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   I expected `--dry-run` to print the daemon it would run (`RUN DRY-RUN as= ...`), because run's own help says it checks the flags and prints the daemon without opening a store.
   Grade: URGENT

7. `nova-friend uninstall --as the-friend`
   Printed:
   ```
   UNINSTALL OK label=com.nova.friend-the-friend plist=<scratch-dir>/Library/LaunchAgents/com.nova.friend-the-friend.plist
   UNINSTALL RAN command="launchctl bootout gui/1000/com.nova.friend-the-friend"
   ```
   I expected a line naming the missing `launchctl` or a darwin-only refusal, since the agent was not booted out and nothing fails silently.
   Grade: NEXT

8. `nova-friend ping-install --as the-friend --every 30s --redis $R`
   Printed:
   ```
   PING-INSTALL REFUSED: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH: ; run: nova-friend help
   ```
   I expected the refusal's remedy to name the missing `launchctl` or the darwin requirement, not `nova-friend help`, which does not put launchctl on the machine.
   Grade: NEXT

9. `nova-friend ping-uninstall --as the-friend`
   Printed:
   ```
   PING-UNINSTALL OK label=com.nova.friend-wake-ping-the-friend plist=<scratch-dir>/Library/LaunchAgents/com.nova.friend-wake-ping-the-friend.plist
   PING-UNINSTALL RAN command="launchctl bootout gui/1000/com.nova.friend-wake-ping-the-friend"
   ```
   I expected the same as `uninstall`: a line naming the missing `launchctl` or a darwin-only refusal, because the wake agent was not booted out.
   Grade: NEXT

## What held

The following verbs ran clean or were correctly refused: `version` prints the build info, `help` and `-h` for every verb exit as documented, `status` refuses without a state directory, `pong` requires the nonce flag, `wait-pong` requires from and nonce, `resume` refuses when no PAUSED marker exists, `refuse-go` refuses with exit 2 for any name given, `watch` exits after timeout, `serve --dry-run` printed `SERVE OK friends=ada every=1s down_after=10s dry_run=true` against the scratch store once a roster row was seeded (it refused `there is no friend row but the-friend to ping` with the `nova-config friend add` remedy on an empty roster), and `beat` refused correctly and named the cause when no sprint server answered at 127.0.0.1:6390. `run`, `uninstall`, `ping-install` and `ping-uninstall` are the findings above; `run` was also started for real under `timeout` and printed its `push proof: pending` and `inbox` lines before the signal.

READ 6/10 — the banner and per-verb helps answer a cold reader, but the harness refusal messages should name the supported harnesses, and the check command's handling of unknown friends is inconsistent with ping's behavior.

USE 5/10 — several verbs refuse with unhelpful remedies that point to `nova-friend help` instead of naming the exact fix; the opencode harness cannot be used for session-driven verbs at all.

urgent=2 next=7
