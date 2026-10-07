# nova-friend dogfood, 2026-10-06 (opencode-2)

Tool: nova-friend. Build: `nova-friend v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`.

Run cold, from the binary's own help (`-h`, `help`, `<verb> -h`) and the friend pages under `docs/`
(`docs/FRIENDS.md`, and the Watch, Identity and "What is weak" sections of `docs/SPEC-FRIEND.md`) only,
no source read for the tool's behaviour. Every verb ran at least once against one scratch store (a
Redis on the bench at `127.0.0.1:16717`, roster seeded with the names the tool's own examples use) and
one scratch directory tree, refusals included. `install`, `uninstall`, `ping-install` and
`ping-uninstall` also ran for real, on a host with no `launchd`, so their write path was exercised up
to the loader. `S` below is the job's scratch directory; the store is `127.0.0.1:16717`.

## 1. `check` reads a flag after a friend name as another friend name — URGENT

**Command:**

    nova-friend check --as ada --redis 127.0.0.1:16717 bob --json

**Printed (first 3 lines):**

    CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-
    CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-
    CHECK BUS friend=bob real_since=0 last_real=-

then the same five lines for `friend=--json`, ending
`CHECK OK friends=2 ok=0 broken=0 deaf=0 silent=0 down=2 untrue=0` at exit 1 — two friends, one of them
`--json`, and the plain lines, not JSON. `... bob --since 1h` gives `friends=3`. The same store with the
flag before the friend, `nova-friend check --as ada --redis 127.0.0.1:16717 --json bob`, prints one JSON
object for one friend, so position alone decides.

**Expected:** the usage line is `nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]`
— friends before flags — so a stranger typing it in the order the help prints expects one friend judged,
as JSON. Instead the trailing flags became friend names, the window and `--json` never applied, and
nothing refused: a wrong result, in the order the help itself teaches.

**Grade:** URGENT

## 2. `check --dry-run` and `resume --dry-run` are listed but never read — URGENT

**Command:**

    nova-friend check --dry-run --as ada --redis 127.0.0.1:16717 bob

**Printed (first 3 lines):**

    CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-
    CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-
    CHECK BUS friend=bob real_since=0 last_real=-

then `CHECK OK friends=1 ok=0 broken=0 deaf=0 silent=0 down=1 untrue=0` and

    CHECK FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

at exit 1. The same from `resume`:

    nova-friend resume --as bob --dir $S/bob --state-dir $S/bobstate --dry-run
    RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1, one line, no remedy.

**Expected:** both verbs list `--dry-run` in their `-h` flag list ("print what the verb would write and
write nothing"), and `check` writes nothing at all, so a dry run should exit 0 and say so. A reader
cannot tell whether the verb wrote, and the failure carries no `run:` line; on `check` the `CHECK OK`
summary on the same run makes it look green.

**Grade:** URGENT

## 3. `install`'s help says claude is refused; `install` and `run` accept it — URGENT

**Command:**

    nova-friend install --as bob --harness claude --config-dir $S/claude2 --dir $S/bob2 --state-dir $S/bobstate2 --redis 127.0.0.1:16717

**Printed (first 3 lines):**

    INSTALL FAILED plist=$S/home/Library/LaunchAgents/com.nova.friend-bob.plist: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH:
    INSTALL NOTE run as a background task, and re-run it with the cursor it printed each time it returns: nova-bus wait --as bob --after <cursor> --wake-file $S/bobstate2/bob.wake

and the same verb with `--dry-run` prints `INSTALL OK label=com.nova.friend-bob ... dry_run=true` and the
same NOTE at exit 0; `nova-friend run --as bob --harness claude --dir $S/bob --state-dir $S/bobstate --config-dir $S/cfg --redis 127.0.0.1:16717 --dry-run` prints `RUN DRY-RUN as=bob harness=claude ...` at
exit 0. The config directory and the plist were written before the loader failed.

**Expected:** the install help says "A harness with no deliver command (claude, the surveyed ones) is
refused before anything is written, the adapter card its remedy", but the same help paragraph documents
claude's passive NOTE, and the no-deliver refusal names claude among the harnesses that do have a
command (`... run the friend under a harness that has one: opencode, codex, claude, antigravity, dsh, gemini, grok, tmux`).
The help cannot both refuse claude and support it; the tool supports it.

**Grade:** URGENT

## 4. `run` and `host` accept a `--dir` that is a symlink (and `run` one that is missing) where `install` refuses — NEXT

**Command:**

    nova-friend run --as bob --harness opencode --dir $S/linkbob --state-dir $S/bobstate --redis 127.0.0.1:16717 --dry-run

**Printed (first 3 lines — the whole output):**

    RUN DRY-RUN as=bob harness=opencode dir=$S/linkbob state=$S/bobstate redis=127.0.0.1:16717; nothing was started

exit 0, where `$S/linkbob` is a symlink to `$S/bob`. `install` with that directory prints

    INSTALL REFUSED: not a real directory: $S/linkbob (dir) is symlink to $S/bob, want a directory; name the real path, install never replaces it; run: nova-friend help

exit 2; `host ... --dir $S/linkbob ... --dry-run` prints `HOST DRY-RUN ...` at exit 0, and
`run ... --dir $S/nope ... --dry-run` prints `RUN DRY-RUN ...` at exit 0 with no such directory.

**Expected:** the rule `docs/FRIENDS.md` states — the working directory must be a real directory, not a
symlink to one (a symlinked Codex root took no writes for ten hours on 2026-10-05) — is held by `install`
alone. `run` and `host` should refuse the same directory, or say why they do not.

**Grade:** NEXT

## 5. `refuse-go --name` accepts any command name — NEXT

**Command:**

    nova-friend refuse-go --name sh

**Printed (first 3 lines — the whole output):**

    REFUSE-GO REFUSED: sh is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && sh ...'; run: nova-friend help

exit 2. `--name go` and `--name gofmt` print the same shape naming the command, and `--json` carries the
same `why`.

**Expected:** the help says `--name go|gofmt`; any other name should be refused as not one of the two,
never answered as if it were a go command with a bench line for `sh`.

**Grade:** NEXT

## 6. the bare command and the unknown-verb refusal hide a verb behind "and 1 more" — NEXT

**Command:**

    nova-friend frobnicate

**Printed (first 3 lines — the whole output):**

    FRIEND REFUSED: "frobnicate" is no verb and no file; the verbs are run, beat, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, watch, status, refuse-go, resume, serve and 1 more, and a file is given by its path (./frobnicate); run: nova-friend help

exit 2; `nova-friend` with no argument prints the same list ending `and 1 more`.

**Expected:** the list is short enough to print whole — it names 16 and hides `version` (and `help`) — and
the standard says an unknown verb is answered with the names there are, not "and 1 more".

**Grade:** NEXT

## 7. `wall` is a live verb no help names — NEXT

**Command:**

    nova-friend wall -h

**Printed (first 3 lines):**

    Usage of wall:
      -config-dir string
        	the friend's CLAUDE_CONFIG_DIR, and the HOME inside the wall

exit 0, with the full flag list. The banner's usage block, the bare refusal's verb list and every
`<verb> -h` omit it, though `docs/SPEC-FRIEND.md` documents it (buds-in-the-wall-r.w5).

**Expected:** the banner names every live verb and a verb's `-h` prints the tool's own help, not Go's
default flag dump; a stranger cannot discover the one verb that shows what the wall does.

**Grade:** NEXT

## 8. `docs/SPEC-FRIEND.md` names `nova-friend screen`, which is not a verb — NEXT

**Command:**

    nova-friend screen -h

**Printed (first 3 lines — the whole output):**

    FRIEND REFUSED: "screen" is no verb and no file; the verbs are run, beat, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong, watch, status, refuse-go, resume, serve and 1 more, and a file is given by its path (./screen); run: nova-friend help

exit 2.

**Expected:** the page says "The last screen of a hosted friend is the pane's capture, the verb
`nova-friend screen`" (`docs/SPEC-FRIEND.md`, Hosted in tmux); either the verb exists or the page does
not name it.

**Grade:** NEXT

## 9. on a host with no `launchctl`, real `install` half-writes then fails with an empty cause, and `uninstall` says OK — NEXT

**Command:**

    nova-friend install --as bob --harness opencode --dir $S/bob --state-dir $S/bobstate --redis 127.0.0.1:16717

**Printed (first 3 lines — the whole output):**

    INSTALL FAILED plist=$S/home/Library/LaunchAgents/com.nova.friend-bob.plist: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH:

(one line, the cause ending in an empty field after the colon), exit 1, with the plist and
`$S/bob/opencode.json` left in place. Then

    nova-friend uninstall --as bob
    UNINSTALL OK label=com.nova.friend-bob plist=$S/home/Library/LaunchAgents/com.nova.friend-bob.plist
    UNINSTALL RAN command="launchctl bootout gui/1000/com.nova.friend-bob"

exit 0, though that bootout cannot run on this host.

**Expected:** a failure names its cause without a trailing empty field, and a `launchctl bootout` that
did not run is not reported as OK (nothing fails silently); on a host with no launchd the verb could
refuse before writing, as it does for a missing directory.

**Grade:** NEXT

## 10. `check`'s `-h` flags and the banner's usage line disagree — NEXT

**Command:**

    nova-friend check -h

**Printed (first 3 lines):**

    usage: nova-friend check [flags]
    from `nova-friend help`:
      nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]

while the flags list under it names `--config-dir`, `--dir`, `--dry-run`, `--harness`, `--model`,
`--redis`, `--settings`, `--state-dir`, `--to` and `--within`.

**Expected:** the one usage line a stranger reads first names every flag the verb takes; a reader who
wants the delivery check (`--harness`) or a store (`--redis`) cannot see how from the banner, which is
also where finding 1's wrong order is taught.

**Grade:** NEXT

READ 6/10 — the banner, the per-verb helps and the refusal grammar answer a cold reader truly for most
verbs, every `-h` exits 0 with its flags, and the refusals carry a runnable remedy; held down by the
`check` usage line and its parse, and by the two documented `--dry-run` paths that end FAILED.

USE 6/10 — the whole protocol ran cold for real against one scratch store and one scratch home (ping,
pong, wait-pong, `watch` including a wake-file wake, check, serve, run, install, uninstall, ping-install,
ping-uninstall, host in a real tmux pane, resume, refuse-go, status, version, help), and the refusals
named valid names and their remedies; held down by the wrong-result `check` parse in the order its own
help prints, the failed dry runs, and the install/uninstall half-write on a host without launchd.

urgent=3 next=7
