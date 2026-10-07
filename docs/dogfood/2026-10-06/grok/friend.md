# nova-friend dogfood, 2026-10-06 (grok)

Tool: nova-friend. Build: `nova-friend v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`,
the tip of this branch's base (`e8f70f600`). Run cold on a Linux bench, from the binary's own help
(`nova-friend`, `nova-friend help`, `nova-friend <verb> -h`) and its pages under `docs/`
(`docs/SPEC-FRIEND.md`, `docs/FRIENDS.md`) only, no source read: the help was captured to files and
read from there, and every verb ran at least once for real. One scratch store and one scratch home:

    B=$HOME/zhi-bench/dogfood-grok-friend-b.w1~15.g7
    S=$B/scratch
    R=127.0.0.1:16499
    HOME=$S/home NOVA_BUS_REDIS=$R

`$R` was a `redis-server` started for this pass on the bench, holding only a roster seeded through
`nova-config apply --file` (the remedy the tool itself prints): `ada`, `bench`, `bob`, `coordinator`.
Every write verb ran under the scratch `HOME`, so `install`, `uninstall`, `ping-install` and
`ping-uninstall` wrote under the scratch home and touched no real home. Two load paths could not run to
the end: `install --as bob --harness opencode --dir $S/work/bob` wrote the harness settings and the
plist and then failed at `launchctl bootstrap: exec: "launchctl": executable file not found in $PATH`
(the bench is Linux), and `ping-install` the same; both refusals are recorded in the findings below
only for what they say, not for the missing loader. `run` ran for real under `timeout` (it started,
wrote `status.json`, and the timeout ended it), `serve` ran for real until `SERVE STOP interrupted`,
and `wall` ran `/bin/echo hi`.

## 1. `check`'s own usage line puts the flags after the friends, and the parse keeps them as friends — URGENT

**Command:**

    nova-friend check --as ada bob --redis 127.0.0.1:16499

**Printed:**

    CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=- proof=none proof_age=-
    CHECK HARNESS friend=bob harness=unknown route=push last=- last_exit=- failed_of_last20=0 deferred=0 broken=- reason=- session_live=- queued=-
    CHECK BUS friend=bob real_since=0 last_real=-

then the same five lines for `friend=--redis` and `friend=127.0.0.1:16499`, and

    CHECK OK friends=3 ok=0 broken=0 deaf=0 silent=0 down=3 untrue=0

exit 1.

**Expected:** `--redis` is in check's own flag list (`nova-friend check -h`: `--redis <string> the bus store's Redis address`), so it should be parsed as the flag it is and only `bob` checked
(`friends=1`). Instead its two tokens became two friends that do not exist and were reported down. The
same parse swallows every flag the usage line teaches: `check --as ada bob --redis $R --json` printed
the plain lines, not JSON, because `--json` became a friend name (`friends=4`, one block starting
`CHECK DAEMON friend=--json`), and `--shown -` became two more friends (`friend=--shown`, `friend=-`).
The `-h` usage line is `nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]`,
which puts `[<friend>...]` before every flag — the exact order that invents them; typed flags-first
(`check --as ada --redis $R bob`) it checks one friend and `--json` prints one JSON object.

**Grade:** URGENT

## 2. `resume --dry-run` fails and warns it may have written, though the help sells it — URGENT

**Command:**

    nova-friend resume --as bob --dir $S/work/bob --dry-run

**Printed:**

    RESUME FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. `resume --as bob --dir $S/work/bob` without `--dry-run` printed `RESUME OK cleared=none` at
exit 0 on the same directory.

**Expected:** `resume -h` lists `--dry-run` and says "print what the verb would write and write
nothing", so it should print the plan (`RESUME OK cleared=none`) and write nothing. Instead a
documented safety flag is refused with a skeleton self-check, no remedy and a warning that the verb it
refused to run may already have written.

**Grade:** URGENT

## 3. `ping`'s next-command note drops the `--redis` that made the ping work — NEXT

**Command:**

    nova-friend ping --as ada --to bob --nonce n1a2b3 --redis 127.0.0.1:16499

**Printed:**

    PING OK nonce=n1a2b3 id=01M4A774S1VEBT7EK3G8FE7S38 to=bob at=2026-10-07T03:41:21Z
    PING NOTE wait for it: nova-friend wait-pong --from bob --nonce n1a2b3

exit 0.

**Expected:** the NOTE is the next command and should run as pasted. It omits `--redis`, so without
`NOVA_BUS_REDIS` in the environment the pasted line is
`WAIT-PONG REFUSED: --redis is required; it wants the bus store's Redis address, host:port (or NOVA_BUS_REDIS); refusing to guess; run: nova-friend help` at exit 2.
A caller who named the store only on the command line is sent to the wrong store, or to a refusal, by
the tool's own advice; the note should carry the `--redis` (and any `--server`) it was given.

**Grade:** NEXT

## 4. `wall` runs and answers `-h`, but no banner names it and its help is not the tool's help — NEXT

**Command:**

    nova-friend wall -h

**Printed:**

    Usage of wall:
      -config-dir string
        	the friend's CLAUDE_CONFIG_DIR, and the HOME inside the wall

exit 0, with the full flag list. `nova-friend wall --dir $S/work/bob --deny /etc -- /bin/echo hi`
printed `hi` at exit 0.

**Expected:** the shape every other verb's help has (the from-help usage line, what the verb does, its
effect, the exit table), and `wall` named in the banner. Neither `nova-friend`'s refusal ("the verbs
are run, beat, install, uninstall, check, host, ping, ping-install, ping-uninstall, pong, wait-pong,
status, refuse-go, resume, serve, version") nor `nova-friend help`'s usage names it, though it is a
living verb with a real effect; and its refusal is not the shared grammar:
`WALL REFUSED reason=bad_profile the friend profile denies nothing: name the coordinator's self (nova-friend run --deny-self), a lane never runs with nothing denied`
at exit 125 has no `run:` remedy and prints a raw `reason=` pair.

**Grade:** NEXT

## 5. `refuse-go --name` answers any name as though it were `go` — NEXT

**Command:**

    nova-friend refuse-go --name cc

**Printed:**

    REFUSE-GO REFUSED: cc is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && cc ...'; run: nova-friend help

exit 2.

**Expected:** `refuse-go -h` says `--name <string> the command that was run: go or gofmt (required)`,
so a name that is neither (here `cc`) should be refused as not a go command, or at least not answered
in the go refusal's words; instead any name reaches the same message, so a shim named anything is told
"no go command runs on this machine" and handed a `cc ...` bench line. The real path is right: a
symlink named `go` to the binary, run as `go build ./...`, printed
`REFUSE-GO REFUSED: go is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && go ...'; run: nova-friend help`
at exit 2.

**Grade:** NEXT

## 6. `serve --as` accepts a coordinator name no roster holds, where `ping` and `pong` refuse it — NEXT

**Command:**

    nova-friend serve --as carol --dry-run --redis 127.0.0.1:16499

**Printed:**

    SERVE OK friends=ada,bob,coordinator every=1s down_after=10s dry_run=true

exit 0.

**Expected:** `carol` is not on the roster (`nova-bus names` lists `ada`, `bench`, `bob`,
`coordinator`), and `ping --as carol` / `pong --as carol` both refuse an unknown name
(`PING REFUSED: carol is no known name; ... add one with nova-config friend add carol --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-friend help`).
`serve`'s own `--as` says "your name, the coordinator", and the coordinator is a friend row too
(`docs/FRIENDS.md`), so serve should refuse an unknown name the same way rather than ping the roster
under a name no row holds and nobody reads pongs from.

**Grade:** NEXT

## 7. an empty `--nonce` is silently replaced, and one carrying a blank leaves the next command unquoted — NEXT

**Command (a):**

    nova-friend ping --as ada --to bob --nonce "" --redis 127.0.0.1:16499

**Printed:**

    PING OK nonce=vktdml id=01M4A78WQBC0TSYBJC9ZNEXETS to=bob at=2026-10-07T03:42:18Z
    PING NOTE wait for it: nova-friend wait-pong --from bob --nonce vktdml

exit 0. **Command (b):**

    nova-friend ping --as ada --to bob --nonce "a b" --redis 127.0.0.1:16499

**Printed:**

    PING OK nonce=a\x20b id=01M4A78WR16QS91XGAZ7Y2JSV5 to=bob at=2026-10-07T03:42:18Z
    PING NOTE wait for it: nova-friend wait-pong --from bob --nonce a b

exit 0.

**Expected:** an empty `--nonce` should be refused the way an empty `pong --nonce` is
(`PONG REFUSED: --nonce is required; it wants the nonce the PING carried; refusing to guess; run: nova-friend help`),
not silently replaced with a random one the caller never named; and a nonce the result prints escaped
(`nonce=a\x20b`) should be quoted in the NOTE (`--nonce 'a b'`), so the suggested line carries one
argument, not two.

**Grade:** NEXT

## 8. `run` and `host` accept a symlinked `--dir` that `install` refuses — NEXT

**Command:**

    nova-friend run --as bob --harness tmux --dir $S/work/boblink --redis 127.0.0.1:16499

with `$S/work/boblink` a symlink to `$S/work/bob`.

**Printed:**

    RUN 2026-10-07T03:42:47Z push proof: pending: the first session check goes into the tmux session now; nothing is delivered until the session answers it
    RUN 2026-10-07T03:42:47Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
    RUN 2026-10-07T03:42:47Z harness: not seen: the tmux session friend-bob is not running; advisory: presence is the session's answer

then the daemon wrote `status.json`, `presence.json` and `deliver.log` in `$S/work/bob/.nova-friend`
(through the symlink); my `timeout` ended it at 124.

**Expected:** `install --as bob --harness opencode --dir $S/work/boblink --dry-run` refuses that same
path (`INSTALL REFUSED: not a real directory: ... (dir) is symlink to ..., want a directory; name the real path, install never replaces it`) and `docs/FRIENDS.md` states the rule: the working directory
"must be a real directory, not a symlink to one: install refuses a symlink and writes nothing (on
2026-10-05 a Codex writable root that was a symlink took no writes for ten hours)". `run --dry-run`
prints `state=$S/work/boblink/.nova-friend` and `host --dry-run` saves the symlink too, so the check
lives only on the install path and a person who runs `run` or `host` directly walks past it.

**Grade:** NEXT

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.479s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	12.062s

The card's named test does not exist at this tip:
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent` answers
`ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.007s [no tests to run]`; `./internal/docs` is the
guard `docs/AGENTS.md` names for `docs/dogfood`, so the two packages above are the gate. One earlier
combined run of the same command flaked in `./internal/ci` at
`TestUnitTierRefusesRedisServer: ... fork/exec .../unit-tier-bin/redis-server: text file busy`; the
next run of `./internal/ci` alone and the combined run above were both green. No code changed: this
pass adds only this record.

READ 5/10 — the banner, the per-verb helps and the refusal grammar answer a cold reader quickly and
truly for most verbs, but `check`'s usage line is the wrong order and its parse turns the flags the
line teaches into friends, `wall` is a real verb no banner names and its help is Go's default, and a
`ping` result prints a next command that refuses.

USE 6/10 — most of the protocol ran cold for real against one scratch store and one scratch home
(ping, pong, wait-pong, check, serve, run, install, uninstall, ping-install, ping-uninstall, host,
status, resume, refuse-go through its `go`/`gofmt` symlinks, version, help, wall), and the
refusals named every valid name and a runnable remedy; held down by the wrong-result `check` parse in
the order its own help prints, the failed `resume --dry-run`, and the two next commands and names that
do not hold up (`ping`'s note, `serve`'s `--as`).

urgent=2 next=6
