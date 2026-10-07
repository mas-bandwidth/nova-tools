# Dogfood: nova-redis — 2026-10-06, dsh

One friend, one tool, cold. I read only the tool's own surfaces — `nova-redis -h`,
`nova-redis help`, `nova-redis help <verb>`, every verb's `-h`, and its page
`docs/SPEC-REDIS.md` — then used every verb at least once with its real flags
against a scratch store and a temp units directory, the refusals too, on a Linux
build bench's loopback. The binary was built from this checkout at
`051068768266` and reports
`nova-redis v1.0.1-0.20261007144949-051068768266 linux/amd64 go1.26.6`. The
binary is written `$B`, the scratch store directory `$STORE`, a second store
`$STORE2`, the store address `$A` = `127.0.0.1:16390`, and the named-password
variables are `COORD_PW`, `BENCH_PW`, `TABLE_PW`, `FRIEND_PW` (their values are
never shown). The scratch store was thrown away; `redis-cli` was used only to
arrange two probes (an unbounded key, and a store that lacks its users). About
30 minutes end to end. Every finding is recorded, not fixed; no code was
changed.

## Findings

1. `$B install store --dry-run` (and `$B install bus --dry-run`; also `$B install store --dry-run --secrets $STORE2`)

   Printed (stderr):
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   exit 1. `install bus` prints the same line with `INSTALL-BUS`, and the same
   line appears for `install store --dry-run --secrets <dir>` with no
   `--as/--key/--sops/--secret`. With the full login flags the verb works:
   `INSTALL STORE DRY-RUN unit=$STORE2/nova-redis-store.service; nothing was
   written or loaded` plus the unit, exit 0.
   I expected the documented safe form to work everywhere: `install store -h`
   says "`--dry-run` prints the unit and writes and loads nothing", and the
   banner offers `--dry-run` as the first-run door. Instead the reader gets a
   `FAILED` line that names an internal token (`Call.DryRun`), carries no `run:`
   remedy, claims "it may have written" for a dry run that wrote nothing, and
   never reaches the missing-flag checks. Grade: URGENT.

2. `$B acl apply --dry-run --addr $A` (the store lacks the four users the build renders, and no `--password-env-for` is given)

   Printed:
   ```
   ACL WOULD-SET user=coordinator role=coordinator
   ACL WOULD-SET user=bench role=member
   ACL WOULD-SET user=ns-table role=table
   ```
   then `ACL APPLY OK dry-run=true users=4 set=0 would=4 library=5ad34e439bc4996e store=$A`, exit 0. The same command without `--dry-run` prints `ACL MISSING ...` and `ACL APPLY REFUSED users=4 missing=coordinator,bench,ns-table,ns-friend: a user the store lacks is created only with a password; run: ...`, exit 1.
   I expected the dry run to plan that refusal, or to name the missing
   `--password-env-for`, rather than answer `OK`: the standard says `--dry-run`
   prints the plan the real run takes, from the same code path, and here the
   printed plan is one the real run will not take. A reader who trusts the safe
   form is told a call will succeed that then refuses. Grade: URGENT.

3. `$B acl apply --addr $A --password-env-for coordinator=COORD_PW --password-env-for bench=BENCH_PW --password-env-for ns-table=TABLE_PW --password-env-for ns-friend=FRIEND_PW` then `ls -l $STORE/users.acl`

   Printed:
   ```
   ACL SET user=coordinator role=coordinator
   ACL SET user=bench role=member
   ACL SET user=ns-table role=table
   ...
   ACL APPLY OK users=4 set=4 saved=acl-file library=5ad34e439bc4996e store=$A
   -rw-r--r-- 1 ... $STORE/users.acl
   ```
   Before the apply the same file was `-rw-------`; the next `serve` start
   rewrote it `-rw-------` again.
   I expected `serve -h` and `docs/SPEC-REDIS.md` rule 6 to hold:
   `<store-dir>/users.acl` is mode 0600. The `ACL SAVE` an apply runs leaves the
   password hashes world-readable until the next `serve` start rewrites the
   file. The exposure is bounded by the 0700 store directory `serve` creates,
   but the documented mode does not hold on the apply path. Grade: NEXT.

4. `$B spill -h` (the same effect line in `fn load -h` and `acl apply -h`)

   Printed (the effect line):
   ```
   effect: local write: writes files on this machine
   ```
   `spill` and `fn load` write to the store `--addr` names — a store on another
   machine over the tailnet is a normal case — and `acl apply` sets the store's
   live ACL; none of the three writes files on the caller's machine.
   I expected the effect to say where the write lands, the standard's own
   "store write", instead of "writes files on this machine", which is true only
   of `serve` and `install`. Grade: NEXT.

5. `$B spill --op abc --addr $A --owner zzz --name x --ttl 5m --value v`

   Printed:
   ```
   SPILL REFUSED: unknown flag --op; the flags of spill are --addr, --dry-run, --json, --name, --owner, --password-env, --ttl, --user, --value; run: nova-redis spill -h
   ```
   exit 2.
   I expected the write verb to take `--op <id>`, the standard's idempotency
   flag, so a `spill` retried after a lost reply is safe. The spec instead ships
   `SPILL UNCONFIRMED` with a `recall` remedy, and there is no way to make the
   retry idempotent. Grade: NEXT.

6. `$B acl render --max 2` (the same in `$B acl check --max 2 --addr $A`)

   Printed:
   ```
   ACL-RENDER REFUSED: unknown flag --max; acl render takes no flags; run: nova-redis acl render -h
   ```
   exit 2; `acl check --max` answers the same shape. `acl render` prints 16 rows
   (12 families and 4 users) and `acl check` prints a row per user, so both
   list.
   I expected the banner's own claim to hold: "A verb that lists takes `--max
   <n>` (default 20, 0 lists all) and says MORE for the rest." No listing verb
   here takes `--max`, and none can print `MORE`. Grade: NEXT.

7. `$B bogus` (and a bare `$B`)

   Printed:
   ```
   REDIS REFUSED: unknown verb "bogus"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help
   ```
   exit 2.
   I expected the line to lead with the tool's name (the standard's grammar is
   `<tool>[ <verb>] REFUSED:`) and the verb list to name `help`, which the usage
   documents (`nova-redis help [<verb>]`) and which works. `fn bogus`,
   `acl bogus`, `install bogus` and `uninstall bogus` answer under the same bare
   `REDIS` prefix. Grade: NEXT.

8. `$B serve --bind 127.0.0.1 --port 16390 --dir $STORE2` (a store already listening on 16390)

   Printed:
   ```
   SERVE START bind=127.0.0.1 port=16390 auth=on persistence=aof eviction=none dir=$STORE2 aclfile=$STORE2/users.acl users=0 program=/usr/bin/redis-server
   ```
   then, after the child's own `Could not create server TCP listening socket 127.0.0.1:16390: bind: Address already in use`:
   ```
   SERVE FAILED err=exit status 1 remedy="run: ls -ld -- '$STORE2'; compare directory access and the explicit --bind/--port with the launch error and any redis-server output"
   ```
   exit 1.
   I expected the `FAILED` line to name the cause the child named (the port is
   taken, or at least "the server did not start") and a next command. Instead
   the line prints the raw `exit status 1` and sends the reader at directory
   access, which is not the cause. Grade: NEXT.

9. `env -u NOVA_REDIS_PASSWORD $B spill --addr 127.0.0.1:1 --owner zzz --name x --ttl 5m --value v`

   Printed:
   ```
   SPILL REFUSED key=zzz:x class=unreachable: redis at 127.0.0.1:1 as the default user, no password: unreachable: dial tcp 127.0.0.1:1: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-redis help
   ```
   exit 2. The same command naming a user (`--user coordinator --password-env
   COORD_PW`, with `COORD_PW` empty) refuses before the dial: `SPILL REFUSED:
   user coordinator (from --user) but COORD_PW is empty; run under nova-secrets
   exec --only COORD_PW, refusing to log in without a password; run: nova-redis
   help`.
   `docs/SPEC-REDIS.md` says "a user whose password variable is empty [is]
   refused (exit 2) before anything is dialled". The default-user path dials and
   reports the store's `unreachable` (or, against a live store, its `NOAUTH`),
   so the pre-dial promise holds only when `--user` is given. Grade: NEXT.

## What worked, for the record

The rest of the run was clean and the store was real. The banner answers all
three onboarding questions and `help <verb>` is byte-identical to `<verb> -h`;
every verb's `-h` exits 0 and `spill --dry-run` needs no store, as advertised.
Every refusal I could arrange names every independent problem in one run (a
`spill` with nothing given prints all five), says what each flag wants, carries
a paste-able `run:` line, and exits 2 before the dial; the wall is real —
`--bind` has no default and refuses a wildcard, a public address, a hostname
and an address outside the tailnet range, while loopback (`127.0.0.1,::1`) and
an address in the tailnet range pass; `--dir` must be absolute and `--port` is
bounded. `spill`
stores `<owner>:<name>` as a hash with an expiry, refuses an owner a store
family claims (`table`, `friend`, ...), the `--dry-run` twin prints the plan
and writes nothing, an empty value round-trips as `value=-`, and `--json` is
the same value as the lines for spill, recall and version. `recall` is a miss
after a 1s TTL and reports `UNBOUNDED` for a key without one. `fn check` →
`fn load` → `fn check` → `fn load` is `MISSING` (exit 1) → `LOADED` → `OK` →
`UNCHANGED` with the library digest, and `acl render` opens no store. A stop and
a restart on the same `--dir` kept every key, kept the four `acl apply` users
(`coordinator` logged in again), kept the default user wanting its password,
left no plaintext password in `users.acl`, restored the file to 0600, and
refused `serve --users nosuch` before starting anything. `install`/`uninstall`
`--dry-run` with full flags print the unit and remove nothing; a missing
`redis-server` fails with the remedy "install redis-server (Redis 7 or later)
so it is on PATH". `version` and `version --json` agree, and the unknown-flag
answers list the real flags and the nearest.

## NOT DONE

A real (non-`--dry-run`) `install store`/`install bus` was not run: it writes a
user unit and loads it, which starts a server and is out of scope for this run,
so those two verbs are graded on `--dry-run` and their refusals. `uninstall
store`/`uninstall bus` were run for real only with nothing installed
(`removed=false`, exit 0). The `serve --secrets/--as/--key/--sops/--secret`
path was graded on `--dry-run` and its help, as no nova-secrets store exists on
the bench. The `SPILL UNCONFIRMED` reply-lost path and `fn load`'s `REPLACED`
path were not arranged. The card's named test `TestDocsTreeIsConsistent` does
not exist in `./internal/docs` at this tip; the gate below runs the packages
the card names. No code was changed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.981s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	9.134s

The card's own named test does not exist at this tip, so its line answers no
tests to run:

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.017s [no tests to run]

READ 7/10 — the banner, every verb's `-h`, the refusal grammar and the spec
page answer a cold reader fast and truly, and the bind and owner-family walls
are honest; the two dry runs that do not match their real run, the effect lines
that send a store write to the local disk, the `--max` claim with no listing
verb to hold it, and the verb list that omits `help` are what hold it down.

USE 7/10 — every verb ran at least once with its real flags against a real
scratch store, a stop and a restart proved persistence and the ACL users, and
each refusal (bar the `serve` launch one) ended in a next command a reader can
paste; held down by the one documented safe `install` form that fails, the
`acl apply --dry-run` that says OK for a call the real run refuses, and the
0644 ACL file the apply leaves.

urgent=2 next=7
