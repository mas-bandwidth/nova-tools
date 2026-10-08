# nova-redis dogfood — opencode-2, 2026-10-06

Read cold, as a stranger: `nova-redis -h`, `nova-redis help`, `nova-redis help <verb>`, every verb's `-h`, and the tool's own page `docs/SPEC-REDIS.md`; nothing else. The binary was built in the staged checkout at `bba7b0ca883af0bd43b8775da225f48c93e49a5c` with `go build -o $JOB/bin/nova-redis ./cmd/nova-redis` (never the installed binary) and printed `nova-redis v1.0.1-0.20261008022703-bba7b0ca883a linux/amd64 go1.26.6`. Every verb ran at least once with its real flags against a scratch store and against a store `serve` ran itself on `127.0.0.1`, on a Linux bench's loopback, the refusals too, with a stop and a restart on the same `--dir`; the binary path is `$B`, the scratch directory and the `serve` store `$STORE`, the store address `$A` = `127.0.0.1:16499`, and the named-password variables are `COORD_PW`, `BENCH_PW`, `TABLE_PW`, `FRIEND_PW` (their values are never shown). The scratch stores were thrown away and no code was changed; a finding is recorded, never fixed here.

## Findings

1. `nova-redis install store --dry-run`
   Printed:
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected the documented safe form to refuse the way the real form refuses when the login flags are missing (`INSTALL-STORE REFUSED: the unit carries no password, ... it names no --secrets, --as, --key, --sops, --secret; nothing was written; run: nova-redis help`, exit 2), or to print the plan the same verb prints when every flag and a loopback `--bind` are given. `nova-redis install bus --dry-run` prints the same line with `INSTALL-BUS`, and with every other flag and a wildcard `--bind 0.0.0.0` the same `FAILED` line is the whole output, so the missing-flag and bind checks never run under `--dry-run`, a usage error is a `FAILED` at exit 1, the reader is shown an internal token (`Call.DryRun`), and the line claims "it may have written" for a run that wrote nothing, with no remedy.
   Grade: URGENT (a writing verb's documented safe form is broken: a `FAILED` with no remedy and a line that is false about what it did)

2. `nova-redis acl apply --redis $A --password-env-for coordinator=COORD_PW --password-env-for bench=BENCH_PW --password-env-for ns-table=TABLE_PW --password-env-for ns-friend=FRIEND_PW`
   Printed:
   ```
   ACL MISSING user=coordinator role=coordinator
   ACL MISSING user=bench role=member
   ACL MISSING user=ns-table role=table
   ```
   I expected mode 0600: `serve -h` says "The store's ACL users live in <store-dir>/users.acl (mode 0600)", `docs/SPEC-REDIS.md` rule 6 says the same, and the `ACL SAVE` an apply runs left the file world-readable; a separate `ls -l $STORE/users.acl` printed `-rw-r--r-- 1 nova nova 5546 Oct  8 03:46 users.acl`, and only the next `serve` start on that same `--dir` rewrote it (`-rw------- 1 nova nova 5546 Oct  8 03:46 users.acl`). The store directory `serve` creates is 0700, so the exposure is bounded, but the promised 0600 is restored only by the next launch.
   Grade: NEXT (a documented protection that does not hold on the apply path)

3. `nova-redis acl render --max 2`
   Printed:
   ```
   ACL-RENDER REFUSED: unknown flag --max; acl render takes no flags; run: nova-redis acl render -h
   (one line printed)
   ```
   I expected `acl render` to take `--max <n>`: `nova-redis help` says "A verb that lists takes `--max <n>` (default 20, 0 lists all) and says MORE for the rest", and `acl render` prints 12 families, 4 users and a bounded-looking `ACL RENDER OK` line; `acl check --max 2` answers the same shape. Its own help says it takes no flags, so the banner's promise holds for no verb that lists here.
   Grade: NEXT (a missing flag the banner promises)

4. `nova-redis spill --redis $A --owner boss --name x --ttl 10m --value x --user ns-table --password-env TABLE_PW`
   Printed:
   ```
   SPILL FAILED key=boss:x class=other: redis at 127.0.0.1:16499 as user ns-table (password from TABLE_PW): failed: NOPERM User ns-table has no permissions to run the 'multi' command; next: the store answered, so the connection stands: read the refusal as the command's own
   (one line printed)
   ```
   I expected a `FAILED` line that names the store's answer as a refusal of this write and gives the remedy for the cause, as `docs/SPEC-REDIS.md` says a store refusal is. The class is `other`, the store's `NOPERM` is about the `multi` wrapper rather than the key, and the "next" tells the reader to read the refusal as the command's own without naming the missing permission; the same store answers the same login with `NOPERM` on a direct write.
   Grade: NEXT (a FAILED line that does not name the cause or the remedy)

5. `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi`
   Printed:
   ```
   SPILL OK key=ada:note ttl=10m0s expires=2026-10-08T01:56:01Z bytes=2 store=127.0.0.1:6379 written=0 dry_run=true
   SPILL NOTE --addr is --redis
   ```
   I expected the banner's own `example:` block — the first run a stranger is told to paste — to use the spelling the usage lines and every flag help now use (`--redis`). The example block and the normative `docs/SPEC-REDIS.md` (its verbs block and its prose) still say `--addr`, while `spill -h`, `recall -h`, `fn load -h`, `acl check -h` and `acl apply -h` label `--addr` "the old spelling of --redis, kept for one release", so the documented first run prints a deprecation NOTE.
   Grade: NEXT (unclear help: the page and the example use the spelling the binary deprecates)

6. `nova-redis spill -h`
   Printed:
   ```
   usage: nova-redis spill [flags]
   from `nova-redis help`:
     nova-redis spill --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
   ```
   I expected the effect line (the last line of this help) to say where the write lands: `spill` writes to the store `--redis` names, `fn load` installs the library there, and `acl apply` sets the store's live ACL, so the effect line `effect: local write: writes files on this machine` is false for a write that lands in the store the address names.
   Grade: NEXT (help that misnames where the write lands)

7. `nova-redis bogus`
   Printed:
   ```
   REDIS REFUSED: unknown verb "bogus"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help
   (one line printed)
   ```
   I expected the line to lead with the tool's name, as the usage and every other refusal do, and the verb list to name `help`, which the usage documents (`nova-redis help [<verb>]`) and which works; a bare `nova-redis` answers the same way. The `uninstall bogus` hint ("did you mean uninstall bus?") is correct for that subverb list and is not the defect.
   Grade: NEXT (unclear help: the list is not the list the usage gives, and the line is prefixed `REDIS`)

8. `nova-redis install store -h`
   Printed:
   ```
   usage: nova-redis install store [flags]
   from `nova-redis help`:
     nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
   ```
   I expected the verb's own `example:` line to be pasteable, as the banner's `example:` block is held to. The line is `nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>`, and `install bus -h` is the same; the angle-bracket placeholders and the `~/nova-bench` path make it fail as printed.
   Grade: NEXT (an unrunnable example in the verb help)

## What held

- `help` and `version` answer as the banner promises (`version --json` renders the same value), `help <verb>` is byte-identical to `<verb> -h`, and every verb's `-h` exits 0 without touching a store.
- `serve` ran for real on `127.0.0.1` with a password and printed `SERVE START`, stopped on `SIGTERM` with `SERVE STOP`, and a restart on the same `--dir` kept the store's data and its four `acl apply` users at mode 0600; `--bind`, `--port` and `--dir` are each required, a wildcard bind, a relative `--dir` and a `--users` user the ACL file lacks are refused before anything starts, and `--dry-run` validates without launching.
- `spill` and `recall` ran clean: a write under `<owner>:<name>` with an expiry, a hit, a miss at exit 1, an empty value (`value=-`), `UNBOUNDED` for a key whose TTL was removed, `--dry-run` and `--json` renderings of the same value, and refusals for a zero, negative or missing `--ttl`.
- `fn check` → `fn load` → `fn check` → `fn load` are `MISSING` (exit 1) → `LOADED` → `OK` → `UNCHANGED` with the library digest.
- `acl render` opens no store and prints the 12 families and 4 users; `acl check` against the applied store is `ACL CHECK OK users=4`; `acl apply` sets all four users and `users.acl`; the same `acl apply` against a store that lacks the users refuses in one line naming every missing user, exit 1 (the store answered and the verb said no, as the banner's exit table says), with a `--password-env-for <user>=<VARIABLE>` template the caller fills.
- `install store`/`install bus --dry-run` with every flag print the unit and write and load nothing; `uninstall store`/`uninstall bus --dry-run` name the unit and remove nothing; an unknown flag names the real flags and the nearest, and `fn check --json`, `acl check --json`, `acl apply --json`, `acl render --json`, `serve --json`, `install store --json` and `uninstall store --json` each refuse by name, the banner disclosing which verbs take `--json`.
- Not run, with the reason: a real (non-`--dry-run`) `install store`/`install bus` writes a user unit and loads it, which starts a server, so those two verbs are graded on `--dry-run` and their refusals; `uninstall store`/`uninstall bus` were run only `--dry-run`, nothing having been installed; the `serve --secrets/--as/--key/--sops/--secret` path was graded on `--dry-run`, there being no secrets store on the bench.

READ 6/10 — the banner, the per-verb help and the refusal grammar answer a cold reader fast and mostly truly, and every missing input is named with its remedy; held down by the `install --dry-run` guard's `FAILED` with an internal token and a false "it may have written", the example block and the normative page still spelling the address flag `--addr`, the effect lines that say "writes files on this machine" for a store write, and the `--max` gap against the banner's own promise.

USE 7/10 — every verb ran with its real flags against a scratch store and a `serve` store, a stop and a restart proved persistence and the ACL users, the refusals end in a named next step, and `--dry-run` and `--json` are honest; held down by the one documented safe `install` form that fails with a false claim, the ACL file `acl apply` leaves world-readable before the next launch, and the `NOPERM` write whose `FAILED` line does not name the cause.

urgent=1 next=7
