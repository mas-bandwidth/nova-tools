# nova-friend dogfood — opencode, 2026-10-06

Tool: nova-friend. Build: `nova-friend v1.0.1-0.20261007212807-38890605d34d`. Run cold, from the binary's own help (`nova-friend`, `nova-friend help`, `nova-friend <verb> -h`) on a Linux bench, with every verb at least once against one scratch directory. Every command below was typed with `S=<scratch-dir>` and `HOME=$S` so `install`, `uninstall`, `ping-install` and `ping-uninstall` wrote under the scratch home.

## Findings

1. `nova-friend check --as boss`
   Printed:
   ```
   CHECK DAEMON friend=boss agent=not-loaded pid=- status=none connection=none challenge=none pong_age= presence=down seen_age= proof=none proof_age=
   CHECK HARNESS friend=boss harness= route= last= last_exit= failed_of_last20=0 deferred=0 broken= reason= session_live= queued=
   CHECK BUS friend=boss real_since=0 last_real=-
   ```
   I expected the check to refuse an unknown friend with a remedial command to add it, since `--as boss` names a friend that was never added to the roster.
   Grade: URGENT

2. `nova-friend ping --as boss --to alice --nonce abc123`
   Printed:
   ```
   PING REFUSED: unknown friend alice; run: nova-config friend add alice && nova-config friend apply
   ```
   I expected the ping to be refused since alice is not in the roster, and the remedy should name the exact command to fix it.
   Grade: NEXT

3. `nova-friend install --as boss --harness opencode --dir $S/boss`
   Printed:
   ```
   INSTALL REFUSED: harness opencode has no deliver command; run: nova-friend help
   ```
   I expected the tool to refuse opencode since it cannot drive a session, but the remedy should name the harnesses it does support.
   Grade: NEXT

4. `nova-friend host --as boss --harness opencode --dir $S/boss`
   Printed:
   ```
   HOST REFUSED: harness opencode is not a terminal harness; run: nova-friend help
   ```
   I expected the host verb to refuse opencode since it is not a TUI harness like aider or grok.
   Grade: NEXT

5. `nova-friend check --harness opencode --as boss --dir $S/boss`
   Printed:
   ```
   CHECK FAIL harness=opencode stage=deliver why=harness has no deliver command
   ```
   I expected the delivery check to explain clearly that opencode cannot be driven because it has no deliver command.
   Grade: NEXT

## What held

The following verbs ran clean or were correctly refused: `version` prints the build info, `help` and `-h` for every verb exit as documented, `status` refuses without a state directory, `pong` requires the nonce flag, `wait-pong` requires from and nonce, `resume` refuses when no PAUSED marker exists, `refuse-go` refuses with exit 2 for any name given, `watch` exits after timeout.

READ 6/10 — the banner and per-verb helps answer a cold reader, but the harness refusal messages should name the supported harnesses, and the check command's handling of unknown friends is inconsistent with ping's behavior.

USE 5/10 — several verbs refuse with unhelpful remedies that point to `nova-friend help` instead of naming the exact fix; the opencode harness cannot be used for session-driven verbs at all.

urgent=1 next=4
