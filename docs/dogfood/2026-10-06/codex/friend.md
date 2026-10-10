# nova-friend dogfood, 2026-10-06 (codex)

Tool: nova-friend. Build: `nova-friend v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`.
Run cold, from the binary's own help (`nova-friend`, `nova-friend help`, `nova-friend <verb> -h`)
and its pages under `docs/` (`docs/SPEC-FRIEND.md`, `docs/FRIENDS.md`) only, on a Linux bench, with
every verb at least once against one scratch store and one scratch directory. The store was a
`redis-server` on the bench at `127.0.0.1:16399`, started for this pass; the roster was seeded with
`friends ada bob`. Every command below was typed with

    B=$HOME/zhi-bench/dogfood-codex-friend-b.w1~15.g5
    S=$B/scratch
    R=127.0.0.1:16399
    HOME=$S/home

so `install`, `uninstall`, `ping-install` and `ping-uninstall` wrote under the scratch home and
touched no real home. The first pass against the empty store is what a real `nova-config` roster
would supply: `nova-friend ping --as ada --to bob` refuses every unknown name in one run, naming the
`nova-config friend add` line and `apply`, and `wait-pong` answers `WAIT-PONG NONE ... within 2s` at
exit 1; both are correct and were the first real use. The `cmd/nova-friend` and `pkg/friend`
trees are byte-identical at the build's commit and at this branch's base (`git diff --stat` between
them is empty), so the findings hold at the tip.

## 1. `check` keeps the flag tokens as friend names, so one extra flag invents two friends — URGENT

**Command:**

    nova-friend check --as ada bob --redis $R

**Printed:**

    CHECK DAEMON friend=bob agent=not-loaded pid=- status=ok connection=connected challenge=challenged pong_age=1m28s presence=<retired> seen_age=33s proof=pending proof_age=33s
    CHECK HARNESS friend=bob harness=dsh route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=-
    CHECK BUS friend=bob real_since=0 last_real=-

then, after bob's five rows, two more friends, `friend=--redis` and `friend=127.0.0.1:16399`, each
five rows down by presence, and the summary `CHECK OK friends=3 ok=1 broken=0 deaf=0 silent=0 down=2 untrue=0`, exit 1.

**Expected:** `--redis` is in check's own flag list (`nova-friend check -h`: `--redis <string> the bus store's Redis address`), so it should be parsed as the flag it is and only `bob` checked
(`friends=1`, exit 0); or a shape the verb cannot run should be refused the way `version extra` is
(`VERSION REFUSED: takes no positional arguments, got "extra" (flags come before arguments)`). Here
the flag was read and its two tokens were kept as positional friends, so a coordinator running the
natural `check --as ada bob --redis <addr>` is told two friends that do not exist are down. check's
usage line does not list `--redis` at all (`nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]`), which is what sends a reader to the end of the
line in the first place. The same parse answers an unknown name (`check --as ada carol --redis $R`
lists `carol` down, where `ping` refuses an unknown name) and buries it under the same two phantoms.

**Grade:** URGENT

## 2. `resume --dry-run` fails with the skeleton's self-check when there is nothing to clear — URGENT

**Command:**

    nova-friend resume --as bob --dir $S/work/bob --dry-run

with the daemon's state directory present and no `PAUSED` marker in it.

**Printed:**

    RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. The same verb with a marker present prints `RESUME OK cleared=would pause=out\x20of\x20funds dry_run=true` at exit 0, and the real run prints `RESUME OK cleared=none`,
also exit 0.

**Expected:** the no-op path should print `RESUME OK cleared=none dry_run=true`, the twin of the
marker path, because `--dry-run` is documented in resume's own help (`nova-friend resume --as <me> [--dir <d>] [--state-dir <d>] [--dry-run]`, "prints what the verb would write and write nothing").
Asking "is anything paused?" is the one question `--dry-run` is for, and on that path the verb exits
1 with a failure line that names the skeleton's internal guard, not the state it read.

**Grade:** URGENT

## 3. `ping --wake --to-friends --dry-run` fails the same way, on the wake loop's only safe form — URGENT

**Command:**

    nova-friend ping --as ada --wake --to-friends --redis $R --server 127.0.0.1:6390 --dry-run

**Printed:**

    PING FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. The single-target form on the same store is fine: `nova-friend ping --as ada --to bob --nonce abc123 --redis $R --dry-run` prints `PING OK nonce=abc123 to=bob dry_run=true` at exit 0.

**Expected:** `ping`'s usage line carries `[--dry-run]` outside the `(--to <friend> | --wake --to-friends)` group, and its flags list says "print what the verb would write and write nothing",
so the wake-loop form should print the pass it would run (or refuse the shape), not fail the
skeleton's unimplemented-dry-run guard. The `--to-friends` form is the one a coordinator installs
with `ping-install`; its dry run is not usable.

**Grade:** URGENT

## 4. `uninstall` and `ping-uninstall` say they ran a `launchctl` that is not there, at exit 0 — NEXT

**Command:**

    nova-friend uninstall --as bob

**Printed:**

    UNINSTALL OK label=com.nova.friend-bob plist=/home/<user>/zhi-bench/dogfood-codex-friend-b.w1~15.g5/scratch/home/Library/LaunchAgents/com.nova.friend-bob.plist
    UNINSTALL RAN command="launchctl bootout gui/1000/com.nova.friend-bob"

exit 0, and the plist was removed. `command -v launchctl` prints nothing on the bench, so the
bootout could not have run. `nova-friend ping-uninstall --as ada` prints the same two shapes
(`PING-UNINSTALL OK ...`, `PING-UNINSTALL RAN command="launchctl bootout ..."`) at exit 0.

**Expected:** a line naming the missing `launchctl` (the agent was not booted out), or a
darwin-only refusal, because the standard is that nothing fails silently: the removal of a file on
disk is reported, and the removal of the loaded agent is not. A reader who deletes a friend's plist
on a machine off macOS is told the agent was booted out when no bootout happened.

**Grade:** NEXT

## 5. `install` writes the plist and then fails on the missing `launchctl`, leaving it behind — NEXT

**Command:**

    nova-friend install --as bob --harness opencode --dir $S/work/bob --redis $R

**Printed:**

    INSTALL FAILED plist=/home/<user>/zhi-bench/dogfood-codex-friend-b.w1~15.g5/scratch/home/Library/LaunchAgents/com.nova.friend-bob.plist: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH:

exit 1. The plist stayed on disk afterwards (`ls $S/home/Library/LaunchAgents` listed
`com.nova.friend-bob.plist`), and the next `nova-friend run --as bob --harness dsh ...` opened with
`RUN ... plist drift: this daemon's arguments differ from the installed plist`.

**Expected:** the platform is known before anything is written (the verb's own effect line is
"writes the launchd agent ... and loads it"), so a machine with no `launchctl` should be refused up
front, or a failed bootstrap should remove the plist it just wrote and say the remedy. The line also
ends in a bare `": "` where the failed command's stderr was empty. A half-installed friend that the
next daemon start reads as plist drift is worse than no install.

**Grade:** NEXT

## 6. `ping-install`'s refusal for a missing `launchctl` points at `nova-friend help` — NEXT

**Command:**

    nova-friend ping-install --as ada --every 30s --redis $R

**Printed:**

    PING-INSTALL REFUSED: launchctl bootstrap: exec: "launchctl": executable file not found in $PATH: ; run: nova-friend help

exit 2.

**Expected:** the refusal's remedy is the one a reader can act on in one turn; `nova-friend help`
does not put `launchctl` on the machine. The missing executable, or the darwin requirement, is the
remedy, the way `install --harness nonsense` names the harnesses it wants.

**Grade:** NEXT

## 7. `refuse-go --name` accepts any name and invents a refusal about it — NEXT

**Command:**

    nova-friend refuse-go --name rm

**Printed:**

    REFUSE-GO REFUSED: rm is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && go ...'; run: nova-friend help

exit 2. (The printed `go ...` above is the line's own `rm ...` spelling; it names the command it was
given.) `--name go` and `--name gofmt` print the same shape.

**Expected:** `--name`'s own description is "the command that was run: go or gofmt (required)", so
`rm` should be refused as a name outside that set. Instead the verb answers as if `rm` were a go
command and writes a bench line for it. The line also carries two `run:` clauses, and the last one
(`nova-friend help`) is not the remedy the first one is.

**Grade:** NEXT

## 8. `install --harness dsh` says "name the real path" with no flag that names it — NEXT

**Command:**

    nova-friend install --as bob --harness dsh --dir $S/work/bob --redis $R --dry-run

on a scratch `HOME` with no `.dsh/profiles/desktop`.

**Printed:**

    INSTALL REFUSED: not a real directory: /home/<user>/zhi-bench/dogfood-codex-friend-b.w1~15.g5/scratch/home/.dsh/profiles/desktop (desktop-profile) is missing, want a directory; name the real path, install never replaces it; run: nova-friend help

exit 2.

**Expected:** install's flags (`nova-friend install -h`) have no flag for the dsh profiles
directory, and the refusal names neither one nor an environment variable, so "name the real path"
cannot be followed from the page the refusal points at. A reader who wants to install a dsh friend
has no turn to take.

**Grade:** NEXT

## 9. `run` starts a daemon against a harness binary that is not on PATH and names it nowhere — NEXT

**Command:**

    timeout 20 nova-friend run --as bob --harness dsh --dir $S/work/bob --redis $R

on a bench where `command -v dsh` prints nothing.

**Printed:**

    RUN 2026-10-06T20:51:19Z plist drift: this daemon's arguments differ from the installed plist (a kickstart keeps the arguments launchd loaded); running: run --as bob --harness dsh ...
    RUN 2026-10-06T20:51:19Z push proof: pending: the first session check goes into the dsh session now; nothing is delivered until the session answers it
    RUN 2026-10-06T20:51:19Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: ...

and then `harness check: cannot tell: alive=session no turn into the dsh session has ended yet; the session check alone`, with `STATUS ... session=ok ...` at exit 0 afterwards; no line in the 20 s
named the missing `dsh` binary.

**Expected:** the check "goes into the dsh session now" only if there is a dsh to run; the opencode
adapter reads `opencode run --help` before it composes a turn, so the dsh adapter can say the
binary is missing before its first check goes in, and `status` should not read `session=ok` (off an
older pong file) while the harness it names is absent.

**Grade:** NEXT

## 10. `check` prints a presence word its own docs retire, so a dogfood record cannot quote the line — NEXT

**Command:**

    nova-friend check --as ada --redis $R bob

**Printed:**

    CHECK DAEMON friend=bob agent=not-loaded pid=- status=ok connection=connected challenge=challenged pong_age=1m28s presence=<retired> seen_age=33s proof=pending proof_age=33s
    CHECK HARNESS friend=bob harness=dsh route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=-
    CHECK BUS friend=bob real_since=0 last_real=-

where the `presence=` value is the word `docs/TERMINOLOGY.md` retires in favour of `down`; it is
written `<retired>` here because the lint below refuses the word itself.

**Expected:** `check`'s own help writes the same word in `presence=<up|<retired>|down>`, while
`docs/TERMINOLOGY.md` says to say `down`; `internal/docs`'s
`TestRetiredWordsAppearOnlyInRecords` scores a use in any file outside the dated records, and
`docs/dogfood/` is not one of them (`internal/docs/testdata/retired-words.txt` carries allow rows
for `cmd/nova-friend/main.go`, `pkg/friend/check.go` and `docs/CLI.md`). So the tool prints the
retired word and the record that documents the print is the file the gate refuses. Expected the
printed value and the help to use the word the docs keep, or `docs/dogfood/` to join the dated
records the lint exempts.

**Grade:** NEXT

## What worked (no finding, kept short)

The banner answers what it does, how it works and how to use it; `nova-friend help`, every verb's
`-h`, `help <verb>` and the bare command all exit as the table says, and an unknown verb or flag is
answered with the full name list and the nearest. The refusal grammar is one line naming every
independent problem with a remedy (`ping` on an empty roster printed one line per unknown name and
the `nova-config friend add`/`apply` turn; `install` without `--redis`, `pong` with a `--as` that is
not the state's friend, `check --shown <missing file>`, `ping --to-friends` without `--wake`, and
`ping --to-friends` with the server down are each refused in the shape the tool documents). The
store verbs are real: `ping` wrote a stream message, `pong` wrote the pong file and the reply,
`wait-pong` read it back (`daemon=false`), `check --as ada --redis $R bob` read a live daemon and
its five CHECK lines plus the summary, `serve` read the friends table and pinged a friend each
second until SIGTERM (`SERVE STOP interrupted`), and `run --dry-run` and `install --dry-run` printed
their plans. `install`, `uninstall`, `ping-install`, `ping-uninstall` and `host` wrote and removed
real files under the scratch home; `host` started a real tmux session, refused a second one with
`tmux attach`, and `resume` cleared a real `PAUSED` marker and reported its message. JSON is the
same value as the lines (`version --json`, `status --json`).

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.545s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	20.503s

The card's named test does not exist at this tip: `go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent` reports `ok github.com/mas-bandwidth/nova-tools/internal/docs 0.010s [no tests to run]`. The two packages above are the real gate, run on the bench against this report's
own new directory. The first run of this report failed that gate on this file's own quoting of the
retired presence word (finding 10); the word is written `<retired>` and the gate is green.

READ 7/10 — the banner, the per-verb helps, the refusal grammar and the JSON twin answer a cold
reader fast and truly, but `check`'s usage line omits `--redis` while its flags list has it and the
parse then reports phantom friends, two documented `--dry-run` paths fail with the skeleton's
self-check, `check` prints a presence word its own terminology retires, and `refuse-go`'s `--name`
and the dsh remedy are not held to their own help.

USE 8/10 — every verb ran for real against one scratch store and a scratch dir (ping, pong,
wait-pong, check, serve, run, install, uninstall, ping-install, ping-uninstall, host, resume,
refuse-go, status, version, help), the daemon wrote a live `status.json`, and `resume` cleared a real
marker; held down by the wrong-result `check` parse, the two failed dry runs, and the bootout that
exits 0 on a machine with no `launchctl`.

urgent=3 next=7
