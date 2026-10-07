# nova-redis dogfood — codex (zhi), 2026-10-06

Read as a stranger: only `nova-redis -h`, `nova-redis help`, `nova-redis help
<verb>`, `nova-redis <verb> -h` and the tool's page `docs/SPEC-REDIS.md`. Built
from the staged checkout at 7b132f109d92d8281b85f9c3ded24dddce2073ba and used
as `nova-redis v1.0.1-0.20261007013005-7b132f109d92 linux/amd64 go1.26.6`:
every verb at least once with its real flags against a scratch `redis-server`
on a Linux build bench's loopback (a fresh store under `$STORE`, invented
values only), a stop and a restart on the same `--dir`, and the refusals too.
The bench-home paths in the transcripts below are written `$B` (the binary) and
`$STORE` (the store dir); the store address is `$A` = `127.0.0.1:16390`; the
password variables are named, never shown. The scratch store was thrown away;
no finding was fixed here.

## Findings

1. `nova-redis install store --dry-run`
   Printed (stderr):
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   exit 1. The same line is printed for `install store --dry-run --secrets
   $STORE/secrets --as bench --key $STORE/bench.key --sops sops --secret
   NOVA_REDIS_PASSWORD --bind 0.0.0.0 --port 16390 --dir $STORE --units $STORE/units`.
   I expected the dry run to make the refusal the real form makes when a login
   flag is missing (`INSTALL-STORE REFUSED: the unit carries no password ... it
   names no --secrets, --as, --key, --sops, --secret; nothing was written`, exit
   2), or to print a plan: the missing-flag and bind checks never run under
   `--dry-run`, a usage error is reported as `FAILED` (exit 1), the reader is
   shown an internal token (`Call.DryRun`), and the line claims "it may have
   written" for a dry run that wrote nothing, with no remedy.
   Grade: URGENT (the documented safe form is broken; a failure with no remedy).

2. `nova-redis acl apply --addr $A --password-env-for coordinator=lower_pw --password-env-for bench=BENCH_PW`
   Printed (stderr):
   ```
   ACL-APPLY REFUSED: invalid value for --password-env-for (--password-env-for wants <user>=<VARIABLE> (capital letters, digits and underscores), got "coordinator=lower_pw"): it wants <user>=<VARIABLE>, repeatable: the variable holding the password a user apply creates gets; a user the store lacks is created only with one; run: nova-redis acl apply -h
   ACL-APPLY REFUSED: unknown flag --password-env-for; the flags of acl apply are --addr, --dry-run, --password-env, --password-env-for, --user; did you mean --password-env-for?; run: nova-redis acl apply -h
   ```
   exit 2. I expected one refusal for the bad value and its remedy; the
   repeatable flag the help advertises instead draws a second line that calls
   the flag unknown, lists it among the flags, and suggests itself. The same
   flag twice with only the first value bad prints one line, so the
   contradiction is not "one line per occurrence".
   Grade: NEXT (a refusal that contradicts itself on a bad input).

3. `nova-redis acl apply --addr $A` (the store lacks the four rendered users)
   Printed:
   ```
   ACL MISSING user=coordinator role=coordinator
   ACL MISSING user=bench role=member
   ACL MISSING user=ns-table role=table
   ```
   then `ACL APPLY REFUSED users=4 missing=coordinator,bench,ns-table,ns-friend:
   a user the store lacks is created only with a password; run: nova-redis acl
   apply --addr 127.0.0.1:16390 --password-env-for coordinator=<VARIABLE> ...`,
   exit 1. I expected exit 2: the banner's table says 2 is "a usage error, a
   flag refused before dialling" and reserves 1 for "ran and said NO"; a
   missing `--password-env-for` in a line that says `REFUSED` is the first. The
   remedy also carries a `<VARIABLE>` placeholder rather than a command that
   runs.
   Grade: NEXT (an exit code the banner's own table denies).

4. `nova-redis acl apply --addr $A --password-env-for coordinator=COORD_PW --password-env-for bench=BENCH_PW --password-env-for ns-table=TABLE_PW --password-env-for ns-friend=FRIEND_PW`, then `ls -l $STORE/users.acl`
   Printed:
   ```
   ACL APPLY OK users=4 set=4 saved=acl-file library=5ad34e439bc4996e store=127.0.0.1:16390
   -rw-r--r-- 1 ... $STORE/users.acl
   ```
   `docs/SPEC-REDIS.md` rule 6 and `serve -h` say the file is mode 0600; the
   `ACL SAVE` an apply runs left it world-readable, and the next `serve` start
   rewrote it `-rw-------`. The store directory `serve` creates is 0700, so the
   exposure is bounded, but the promised 0600 is restored only on the next
   launch.
   Grade: NEXT (a documented protection that does not hold on the apply path).

5. `nova-redis spill -h` (the same in `fn load -h`, `acl apply -h`)
   Printed:
   ```
   effect: local write: writes files on this machine
   ```
   `--addr` may be a store on another machine over the tailnet, so `spill` and
   `fn load` do not write files on this machine; the standard names a "store
   write" effect and the skeleton offers only inspection, local write and
   delivery, so the tool has no true label to give and prints a false one. I
   expected the effect line to say the verb writes to the store.
   Grade: NEXT (help that misnames where the write lands).

6. `nova-redis spill --addr $A --user ns-friend --password-env FRIEND_PW --owner zzz --name x --ttl 10m --value x`
   Printed (stderr):
   ```
   SPILL FAILED key=zzz:x class=other: redis at 127.0.0.1:16390 as user ns-friend (password from FRIEND_PW): failed: EXECABORT Transaction discarded because of previous errors.; next: the store answered, so the connection stands: read the refusal as the command's own
   ```
   exit 1. The same store answers the same write directly with `NOPERM No
   permissions to access a key`, and the spec says a store refusal is a refusal
   the FAILED line should name; instead the reader gets `class=other` and
   Redis's MULTI/EXEC wrapper text, with no mention of permissions and no remedy
   that names the cause.
   Grade: NEXT (a FAILED line that does not name the store's answer).

7. `nova-redis acl render --max 2`
   Printed (stderr):
   ```
   ACL-RENDER REFUSED: unknown flag --max; acl render takes no flags; run: nova-redis acl render -h
   ```
   exit 2; `acl check --max 2` is the same shape. `nova-redis help` says "A verb
   that lists takes `--max <n>` (default 20, 0 lists all) and says MORE for the
   rest", and `acl render` prints 17 lines (12 families and 4 users) with no
   MORE; I expected the banner's own claim to hold for the verbs that list.
   Grade: NEXT (a missing flag the banner promises).

8. `nova-redis bogus` (and a bare `nova-redis`)
   Printed (stderr):
   ```
   REDIS REFUSED: unknown verb "bogus"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help
   ```
   exit 2. The usage documents `nova-redis help [<verb>]`, but the list omits
   `help`, and the line is prefixed `REDIS`, not the tool's name. `nova-redis
   uninstall bogus` also answers `did you mean uninstall bus?` for the word
   `bogus`.
   Grade: NEXT (unclear help: the list is not the list the usage gives, and the
   nearest-name hint is wrong).

9. `nova-redis fn check --json --addr $A` (and `acl check --json`, `acl apply --json`, `acl render --json`, `serve --json`, `install store --json`, `uninstall store --json`)
   Printed (stderr):
   ```
   FN-CHECK REFUSED: unknown flag --json; the flags of fn check are --addr, --password-env, --user; run: nova-redis fn check -h
   ```
   exit 2. The standard the tool is built to says "Every verb accepts `--json`"
   with "one output structure, two renderings"; `fn check`, `acl check` and
   `acl apply` are exactly the verbs whose facts (a digest, the live-vs-rendered
   user rows) a caller wants to parse, and none has a JSON rendering. The banner
   does disclose the omissions, so this is a gap against the standard, not a
   lie.
   Grade: NEXT (a missing flag against the stated standard).

10. `env -u NOVA_REDIS_PASSWORD nova-redis recall --addr $A --owner ada --name note`
    Printed (stderr):
    ```
    RECALL REFUSED key=ada:note class=auth-refused remedy="nova-redis reads the store's password from NOVA_REDIS_PASSWORD, which is not set: export it, holding the password of the default user": redis at 127.0.0.1:16390 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user and the variable that holds its password; run: nova-redis help
    ```
    exit 2. `docs/SPEC-REDIS.md` says a user whose password variable is empty
    "is refused (exit 2) before anything is dialled"; the empty-password path
    dials and reports the store's `NOAUTH` instead, so the pre-dial promise does
    not hold (the remedy itself is one a reader can act on).
    Grade: NEXT (the normative page's pre-dial promise does not hold).

11. `nova-redis install store -h`
    Its `example:` line is
    ```
    nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>
    ```
    angle-bracket placeholders and a `~/nova-bench` path, so it cannot run as
    printed; the `install bus -h` example is the same. I expected the verb's own
    example to be pasteable, the standard the banner's `example:` block meets.
    Grade: NEXT (an unrunnable example in the verb help).

## What the tool got right

- `serve` is a real wall: `--bind` has no default, and a wildcard, a public
  address and a hostname are each refused by name; `--dir` must be absolute; a
  `--users` user the ACL file lacks is refused before anything is written or
  launched; the store directory `serve` creates is 0700.
- `spill`/`recall` refuse a missing owner, a missing, zero or negative TTL, a
  missing or malformed `--addr`, a `--password-env` that is not a variable
  name, a user name with whitespace, a wrong password and an unknown user, each
  as one line with a remedy; a miss, an expired key and a key whose TTL was
  removed (`RECALL UNBOUNDED`) are exit 1, an empty value is written and read
  back (`value=-`), and the `--json` twin of a success, a miss and a refusal is
  the same value.
- A stop and a restart on the same `--dir` kept `ada:persist` (AOF), kept the
  four users `acl apply` set (`bench` and `coordinator` logged in again), kept
  the default user wanting its password, left no plaintext password in
  `users.acl`, and rewrote the file 0600 on the next start.
- `fn check` → `fn load` → `fn check` → `fn load` are `MISSING` (exit 1) →
  `LOADED` → `OK` → `UNCHANGED` with the digest, and `acl render` opens no
  store; `install store/bus --dry-run` and `uninstall store/bus --dry-run`
  print the exact unit or name it and write nothing; `version --json` renders
  the same value; an unknown flag gets the names there are and the nearest.

READ 6/10 — the banner, the per-verb help and the refusal grammar answer a
cold reader fast and mostly truly, and the store-write path is legible; the
broken `install --dry-run` guard, the effect lines that say "writes files on
this machine" for a remote store, the `acl render` wall with no `--max`/MORE,
the verbs that list but refuse `--json`, and an `install` example that cannot
run are what hold it down.

USE 8/10 — every verb ran at least once with its real flags against a scratch
store, a stop and a restart proved persistence and the ACL users, and each
refusal (bar the `EXECABORT` one) ended in a next command a reader can paste;
held down by the one documented safe `install` form that fails, the `acl apply`
missing-login exit code and the 0644 ACL file it leaves, and the empty-password
path that dials against its own spec.

NOT DONE: a real (non-`--dry-run`) `install store`/`install bus` was not run —
it writes a user unit and loads it, which starting a server is out of scope for
— so those two verbs are graded on `--dry-run` and the refusals; `uninstall
store`/`uninstall bus` were run only `--dry-run` (nothing was installed to
uninstall); the `serve --secrets/--as/--key/--sops/--secret` path was graded on
`--dry-run` and its help (no nova-secrets store on the bench). The card's named
test `TestDocsTreeIsConsistent` does not exist in `./internal/docs` at this tip
(the gate below runs what is there). No code was changed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.745s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	9.281s

Run on a build bench against this report's own new file; the two packages are
the card's real gate, since `TestDocsTreeIsConsistent` does not exist:
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent`
answers `ok  github.com/mas-bandwidth/nova-tools/internal/docs  0.014s [no tests
to run]`. One earlier standalone `./internal/ci` run answered `FAIL` with no
reproducible test; three standalone runs and four combined runs then passed. It
is recorded, not chased here.

urgent=1 next=10
