# nova-redis dogfood — opencode-2, 2026-10-06

Read cold, as a stranger: `nova-redis -h`, `nova-redis help`, `nova-redis help <verb>`, every verb's `-h`, and the tool's own page `docs/SPEC-REDIS.md`. Built in the staged checkout at 7acb90e18a764f0e728cd5ed701196a34405a824 with `go build -o $B/nova-redis ./cmd/nova-redis` (never the installed binary) and used as `nova-redis v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6` on a Linux bench. Every verb ran at least once with its real flags against a scratch `redis-server` on the bench's loopback (a Unix socket under `$STORE`, invented owners `ada` and `boss`) and against a store `serve` ran itself on `127.0.0.1:16399` with `$STORE/users.acl`: the refusals too, a stop and a restart on the same `--dir`, the users set and rotated. `$B` is the built binary and `$STORE` the scratch directory or socket; the scratch store was thrown away and no code was changed. A finding is never fixed here, only recorded.

## Findings

1. `nova-redis install store --dry-run`
   Printed:
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected the five required secrets flags (`--secrets`, `--as`, `--key`, `--sops`, `--secret`) to be reported the way `serve --dry-run` reports its missing flags: one `INSTALL-STORE REFUSED: ... ; run: ...` line at exit 2 that names what each flag wants, with nothing written. The same one line is the whole output of `nova-redis install bus --dry-run`, and of `nova-redis install store --dry-run ... --bind 0.0.0.0` with every other flag given; with every flag given and a loopback `--bind` the same verb does print its dry-run plan and writes nothing, so the failure is the dry run's own guard. The reader gets an internal token (`Call.DryRun`), a `FAILED` at exit 1 for a usage error the banner's table puts at 2, and the false claim "it may have written" for a run that wrote nothing.
   Grade: URGENT (a refusal with no remedy, and the documented safe form is broken)

2. `ls -l $STORE/users.acl`, after `nova-redis acl apply --redis $STORE --password-env-for coordinator=COORD_PW --password-env-for bench=BENCH_PW --password-env-for ns-table=TABLE_PW --password-env-for ns-friend=FRIEND_PW`
   Printed:
   ```
   -rw-r--r-- 1 <owner> <group> 5546 ... $STORE/users.acl
   (one line printed)
   ```
   I expected mode 0600. `serve -h` says "The store's ACL users live in <store-dir>/users.acl (mode 0600)", `docs/SPEC-REDIS.md` rule 6 says the same, and `acl apply` printed `ACL APPLY OK users=4 set=4 saved=acl-file ...`; the `ACL SAVE` an apply runs left the file world-readable, and only the next `serve` start on that `--dir` rewrote it `-rw-------`. The store directory `serve` creates is 0700, so the exposure is bounded, but the promised 0600 is restored only by the next launch.
   Grade: NEXT (a documented protection that does not hold on the apply path)

3. `nova-redis acl apply --redis $STORE` (a scratch store that lacks the four rendered users)
   Printed:
   ```
   ACL MISSING user=coordinator role=coordinator
   ACL MISSING user=bench role=member
   ACL MISSING user=ns-table role=table
   ```
   The run's fourth line is `ACL APPLY REFUSED users=4 missing=coordinator,bench,ns-table,ns-friend: a user the store lacks is created only with a password; run: nova-redis acl apply --redis '$STORE' --password-env-for coordinator=<VARIABLE> --password-env-for bench=<VARIABLE> --password-env-for ns-table=<VARIABLE> --password-env-for ns-friend=<VARIABLE> (the variable set, under nova-secrets exec --only <VARIABLE>)`, exit 1. I expected exit 2 for a missing flag the banner's own table calls "a usage error, a flag refused before dialling", and a remedy a reader can paste; the line says `REFUSED` yet exits 1, and the recovery is four `<VARIABLE>` placeholders that cannot run as printed.
   Grade: NEXT (an exit code the banner's own table denies, and a remedy that is not a command)

4. `nova-redis acl render --max 2`
   Printed:
   ```
   ACL-RENDER REFUSED: unknown flag --max; acl render takes no flags; run: nova-redis acl render -h
   (one line printed)
   ```
   I expected `acl render` to take `--max <n>` and print a `MORE shown=<n> total=<n>` line: `nova-redis help` says "A verb that lists takes `--max <n>` (default 20, 0 lists all) and says MORE for the rest", and `acl render` prints a list of 12 families and 4 users (17 lines). Its own help says it takes no flags, so the banner's promise holds for no verb that lists here.
   Grade: NEXT (a missing flag the banner promises)

5. `nova-redis fn check --redis $STORE --json`
   Printed:
   ```
   FN-CHECK REFUSED: unknown flag --json; the flags of fn check are --addr, --password-env, --redis, --user; run: nova-redis fn check -h
   (one line printed)
   ```
   I expected a JSON rendering: the standard this tool is built to says "Every verb accepts `--json`", one value with two renderings. The same refusal came from `acl check --json`, `acl apply --json`, `acl render --json`, `serve --json`, `install store --json` and `uninstall store --json`; the banner does disclose the omissions, so this is a gap against the standard rather than a lie in the help.
   Grade: NEXT (a missing flag against the stated standard)

6. `nova-redis spill --redis $STORE --owner boss --name x --ttl 10m --value x --user ns-table --password-env TABLE_PW`
   Printed:
   ```
   SPILL FAILED key=boss:x class=other: redis at 127.0.0.1:16399 as user ns-table (password from TABLE_PW): failed: NOPERM User ns-table has no permissions to run the 'multi' command; next: the store answered, so the connection stands: read the refusal as the command's own
   (one line printed)
   ```
   I expected a `FAILED` line that names the store's answer as a refusal of this write and gives the remedy for the cause, as `docs/SPEC-REDIS.md` says a store refusal is. The class is `other`, the store's `NOPERM` is about the `multi` wrapper rather than the key, and the "next" tells the reader to read the refusal as the command's own without naming the missing permission. The same store answers the same login with `NOPERM` on a direct write.
   Grade: NEXT (a FAILED line that does not name the cause or the remedy)

7. `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi`
   Printed:
   ```
   SPILL OK key=ada:note ttl=10m0s expires=2026-10-07T21:26:14Z bytes=2 store=127.0.0.1:6379 written=0 dry_run=true
   SPILL NOTE --addr is --redis
   ```
   I expected the banner's own `example:` block — the first run a stranger is told to paste — to use the spelling the usage lines and every flag help now use (`--redis`). The example block and the normative `docs/SPEC-REDIS.md` (its verbs block and its prose) still say `--addr`, while `spill -h`, `recall -h`, `fn load -h`, `acl check -h` and `acl apply -h` label `--addr` "the old spelling of --redis, kept for one release", so the documented first run prints a deprecation NOTE.
   Grade: NEXT (unclear help: the page and the example use the spelling the binary deprecates)

8. `nova-redis spill -h`
   Printed:
   ```
   usage: nova-redis spill [flags]
   from `nova-redis help`:
     nova-redis spill --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
   ```
   The `effect:` line further down says `effect: local write: writes files on this machine`, and `fn load -h` and `acl apply -h` say the same. I expected the effect to say the verb writes to the store: `--redis` may name a store on another host over the tailnet, and none of these three verbs writes a file on this machine, so the label names the wrong place.
   Grade: NEXT (help that misnames where the write lands)

9. `nova-redis uninstall bogus`
   Printed:
   ```
   REDIS REFUSED: unknown verb "uninstall bogus" in uninstall; did you mean uninstall bus? the verbs are uninstall store, uninstall bus; run: nova-redis uninstall -h
   (one line printed)
   ```
   I expected the hint to name a nearest verb and the list to be the list the usage gives: `"bogus"` is not nearest to `uninstall bus`, the line is prefixed `REDIS` rather than the tool or verb, and the same list (bare `nova-redis`, `nova-redis bogus`) omits `help`, though the usage documents `nova-redis help [<verb>]`.
   Grade: NEXT (unclear help: the list is not the list the usage gives, and the hint is wrong)

10. `nova-redis install store -h`
    Printed:
    ```
    usage: nova-redis install store [flags]
    from `nova-redis help`:
      nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
    ```
    The verb's `example:` line is `nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>`, and `install bus -h` is the same. I expected the verb's own example to be pasteable, as the banner's `example:` block is held to; the angle-bracket placeholders and the `~/nova-bench` path make it fail as printed.
    Grade: NEXT (an unrunnable example in the verb help)

## What held

- Every verb ran at least once with its real flags and each ran clean on its own path: `help` and `version`, `serve` (a real start on `127.0.0.1:16399` with a password, plus `--dry-run` and the missing-flag, wildcard, relative-dir, out-of-range-port, hostname and non-loopback refusals), `spill` (real, `--dry-run`, `--json`, an empty value, missing/zero/negative/malformed TTL, bad owner/name/address/password-env/user), `recall` (hit, miss at exit 1, `--json`, `UNBOUNDED`, auth refusals), `fn load` and `fn check` (`MISSING` → `LOADED` → `OK` → `UNCHANGED`), `acl render`, `acl check` (`DRIFT` → `OK`), `acl apply` (set, `--dry-run`, `--rotate`, `--drop-old`), `install store`/`install bus` (`--dry-run`) and `uninstall store`/`uninstall bus` (`--dry-run`).
- A stop and a restart of `serve` on the same `--dir` kept `boss:persist` (AOF) and the four users `acl apply` set; the restart rewrote `users.acl` 0600, the file held no plaintext password, and `--users` named the users the file must hold, with a file lacking one refused before anything was started.
- The scratch-verb refusals each name what the flag wants and exit 2; a hit, a miss and a refusal render the same value under `--json`; an empty value is written and read back (`value=-`); a wrong password and an unknown user are one `auth-refused` line at exit 2 with the next step; `acl apply --rotate` added a password beside the old one (both logged in) and `--drop-old` removed the old one (only the new one logged in); `acl render` opens no store; `install store/bus --dry-run` with every flag prints the unit and writes nothing; `version --json` renders the same value.
- Not run, with the reason: a real (non-`--dry-run`) `install store`/`install bus` writes a user unit and loads it, which starts a server and is out of scope here, so those two verbs are graded on `--dry-run` and the refusals; `uninstall store`/`uninstall bus` were run only `--dry-run`, nothing having been installed; the `serve --secrets/--as/--key/--sops/--secret` path was graded on `--dry-run`, there being no secrets store on the bench.

READ 6/10 — the banner, the per-verb help and the refusal grammar answer a cold reader fast and mostly truly, and every missing input is named with its remedy; held down by the `install --dry-run` guard's `FAILED` with an internal token and a false "it may have written", the example block and the normative page still spelling the address flag `--addr`, the effect lines that say "writes files on this machine" for a store write, and the `--max`/`--json` gaps against the banner's own promises.

USE 8/10 — every verb ran with its real flags against a scratch store and against a `serve` store, a stop and a restart proved AOF persistence and the ACL users, `--rotate`/`--drop-old` worked, and each refusal ended in a next step a reader can use; held down by the one documented safe `install` form that fails with a false claim, the ACL file `acl apply` leaves world-readable before the next launch, and the `NOPERM` write whose `FAILED` line does not name the cause.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.005s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	14.015s

Run on a Linux bench against this report's own committed file; `./internal/docs` (which holds `TestDocsTreeIsConsistent`) and `./internal/ci` are the card's gate, and the named `TestDocsTreeIsConsistent` passed on its own (`ok github.com/mas-bandwidth/nova-tools/internal/docs 0.015s`).

urgent=1 next=9
