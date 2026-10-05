Verdict: HOLD

PASS line (verbatim): "a server install and a rollback each done by the verbs, gated by the land self-test."

Blocker: Rowan's release reason opening the install window is absent from `nova-sprint log --card accept-installs`, so the window was not opened, and no install or rollback was run. The card's MEASURE step requires a HOLD in that case. The sprint server answered this attempt. Attempts 1-4 held because the verbs could not run (exit 126) and 127.0.0.1:6390 refused; both blockers are cleared now. The remaining blocker is the release itself.

Window: not opened. Reads were taken from Mon Oct 5 02:11:28 UTC 2026 to Mon Oct  5 02:12:57 UTC 2026.
Build measured: nova-sprint v1.2.0-dev.heldtakes darwin/arm64 go1.26.6 at 02:11:28 UTC, then nova-sprint v1.2.0-dev.busauth darwin/arm64 go1.26.6 at 02:12:09 UTC. The installed client changed between two reads 41 s apart, which this card did not do. Some other actor installed a build during the attempt.
Host: studio.local. Attempt 5, generation 3, friend rowan-mas (a bud of Rowan; Claude Code, claude-opus-5-5).

Commands run (all read-only, each with NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan, output filtered by grep -v SECRETS):
- date -u; hostname
- nova-sprint --version (twice, exit 0 both)
- nova-sprint log --card accept-installs (4 calls: 3 exit 0, 1 refused with: Post "http://127.0.0.1:6390/verbs": EOF)
- nova-sprint help | grep -iE 'install|rollback|self-test' (no match, exit 1)
- nova-update help | grep -iE 'install|rollback|self-test' (lists `nova-update release <cut|build|install|adopt|pull>`; no rollback verb or land self-test named in the help)
- curl -s http://127.0.0.1:7390/api/sprint (http=200, 82714 bytes)

Per criterion:
- Server install done by the verb, gated by the land self-test: not measured (window closed). 0 installs run.
- Rollback done by the verb, gated by the land self-test: not measured (window closed). 0 rollbacks run.
- Server back on the original build at the end: not applicable. This card changed nothing.

Raw lines deciding the hold:
- The card log has 100 lines. 0 lines match install window / window open / released by rowan / release reason outside the earlier attempts' HOLD reports. Rowan's own lines are only `15:09:06 accept-installs added to sprint-next by rowan, waiting for what it needs` and `19:11:40 accept-installs changed by rowan: brief_attempt=0`.
- `19:14:21 accept-installs is ready: what it needs has landed` is a dependency release by the machine. It is not a release reason that opens the install window.

Not measured: install, rollback, the land self-test result lines, proof that a failing self-test refuses, unanswering time, and the post-run build. Two further findings came up. First, `nova-sprint help` names no install or rollback verb. Second, `nova-update help` names `release install` but no rollback; a rollback-by-verb path has to be named before this card can pass.
To unblock: Rowan logs a release reason on accept-installs saying the install window is open and names the server build to install. If no rollback verb exists, that reason also names the rollback verb.
