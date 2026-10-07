# nova-redis dogfood — opencode-2 (zhi), 2026-10-06

Read as a stranger: only `nova-redis -h`, `nova-redis help`, `nova-redis help
<verb>`, `nova-redis <verb> -h`, the group helps `nova-redis fn -h` and
`nova-redis acl -h`, and the tool's page `docs/SPEC-REDIS.md`. Built from the
checkout at abb9bfecc72930ef36ce116ac2f652b0e85ad044 and used as `nova-redis
v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6` on the Linux build
bench's loopback: every verb at least once with its real flags against
throwaway stores (`redis-server` on a Unix socket for the scratch verbs; a
`serve`d store on `127.0.0.1:<port>` for the lifecycle, `acl apply` and the
restart), a stop and a restart on the same `--dir`, and the refusals too. The
bench-home paths and the random port are written `$STORE` and `$ADDR`; the
binary is `nova-redis`. The throwaway stores were thrown away; no finding was
fixed here.

## Findings

1. `nova-redis install store --dry-run` (the same in `install bus --dry-run`)
   Printed (stderr):
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   exit 1. The same call without `--dry-run` refuses cleanly: `INSTALL-STORE
   REFUSED: the unit carries no password, so serve reads it in its own process
   from the login the unit names, and it names no --secrets, --as, --key,
   --sops, --secret; nothing was written; run: nova-redis help`, exit 2. I
   expected the documented safe form of a writing verb to report the missing
   required flags the way the real form does, or at least one line naming them
   with a remedy. Instead it names an internal token (`Call.DryRun`), claims
   "it may have written" for a run that read no flag and wrote nothing, and
   offers no remedy. With all its flags `install store --dry-run` works
   (`INSTALL STORE DRY-RUN unit=$STORE/units/nova-redis-store.service; nothing
   was written or loaded`, exit 0), so the hole is the dry run's own
   validation path, not the dry run.
   Grade: URGENT (the safe form of a writing verb fails with no remedy, names
   an internal token, and its line is false about what it did).

2. `NOVA_REDIS_PASSWORD=... nova-redis spill --redis $ADDR --user ns-friend --password-env NSFRIENDPW --owner friend --name f --ttl 5m --value v` (then the same with `--owner loops`, `--owner routes`)
   Printed (first form):
   ```
   SPILL REFUSED: --owner "friend" is the friends store family (friend:*, friends, friends:*); scratch lives outside the store's families, so pick an owner no family claims; run: nova-redis help
   ```
   exit 2 with `--owner friend`; with `--owner loops` or `--owner routes`:
   ```
   SPILL FAILED key=loops:x class=other: redis at $ADDR as user ns-friend (password from NSFRIENDPW): failed: EXECABORT Transaction discarded because of previous errors.; next: the store answered, so the connection stands: read the refusal as the command's own
   ```
   exit 1. The build's own ACL render gives `ns-friend` `+@write` and the
   writable key patterns `~table:* ~tables ~view:* ~views ~sprint:*
   ~friend:* ~friends ~friends:* ~tokens:ledger:*`; every one of those owners
   (`table`, `view`, `sprint`, `friend`, `friends`, `tokens`) is refused by
   `spill` as a store family, and every owner that passes the family check
   (`loops`, `routes`, and any invented name) is outside the user's patterns,
   so the store refuses the key. I expected the ACL identity the tool builds to
   be able to write a scratch key, or the family refusal's advice ("pick an
   owner no family claims") to be followable for that user. The EXECABORT line
   hides the store's own `NOPERM` on the key, so the reader cannot see what was
   refused. Grade: URGENT (a refusal whose printed remedy cannot be followed as
   the tool's own ACL user, and a store refusal that does not name itself).

3. `NOVA_REDIS_PASSWORD=... nova-redis acl apply --redis $ADDR --password-env NOVA_REDIS_PASSWORD --password-env-for coordinator=COORDPW --password-env-for bench=BENCHPW --password-env-for ns-table=NSTABLEPW --password-env-for ns-friend=NSFRIENDPW` then `stat -c '%a' "$STORE/users.acl"`
   Printed:
   ```
   ACL APPLY OK users=4 set=4 saved=acl-file library=5ad34e439bc4996e store=$ADDR rotated=0
   ```
   exit 0, and the file was `644` (`-rw-r--r--`). `docs/SPEC-REDIS.md` rule 6
   and `serve -h` say the ACL file is mode 0600, and it is 0600 when `serve`
   creates it and 0600 again after the next `serve` on the same `--dir`, but
   `acl apply`'s own `ACL SAVE` leaves it world-readable until that launch. The
   file holds the users' password hashes. I expected the mode the page
   promises from the moment apply writes the file. Grade: NEXT (the promised
   0600 returns only on the next launch; the window is between apply and the
   next start).

4. `nova-redis acl check --redis $ADDR --json` (the same in `acl render --json`, `acl apply --json`, `fn load --json`, `serve --json`, `uninstall store --json`)
   Printed:
   ```
   ACL-CHECK REFUSED: unknown flag --json; the flags of acl check are --addr, --password-env, --redis, --user; run: nova-redis acl check -h
   ```
   exit 2; `serve --json` prints the same for serve's flags, `uninstall store
   --json` for uninstall's. The banner says every verb but ten takes `--json`,
   and the repository's standard says every verb builds one value rendered as
   lines or as JSON; `acl check`'s user rows and `fn check`'s digest are exactly
   the facts a caller parses, and none of the store verbs but `spill`/`recall`
   has a JSON rendering. I expected every verb to accept `--json` (or the
   banner to state the exception as the rule it is). Grade: NEXT (a missing
   flag against the stated one-structure-two-renderings rule).

5. `nova-redis version --json`
   Printed:
   ```
   {"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-redis v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6"}
   ```
   exit 0. `spill --json` and `recall --json` carry their values under `facts`,
   while `version` adds a `payload` string and leaves `facts` empty; the
   structure the standard states is `result`, `facts`, `items`, `notes`, one
   value with two renderings, so a parser that reads `facts` must special-case
   the one verb every tool has. Grade: NEXT (a second JSON shape for the same
   structure).

6. `nova-redis acl render --max 2` (the same in `acl check --redis $ADDR --max 2`)
   Printed:
   ```
   ACL-RENDER REFUSED: unknown flag --max; acl render takes no flags; run: nova-redis acl render -h
   ACL-CHECK REFUSED: unknown flag --max; the flags of acl check are --addr, --password-env, --redis, --user; run: nova-redis acl check -h
   ```
   both exit 2. The banner says "A verb that lists takes `--max <n>` (default
   20, 0 lists all) and says `MORE` for the rest"; `acl render` lists the four
   build users and the function library and `acl check` lists the four users,
   and neither takes the flag or prints `MORE`. I expected the listing verbs to
   take `--max`, or the banner to say which listings it does not cover. Grade:
   NEXT (a banner promise no verb keeps).

7. `nova-redis spill --owner ada --name note --ttl 10m --value hi` (no address) and `docs/SPEC-REDIS.md`
   Printed:
   ```
   SPILL REFUSED: --addr is required: the store's address as <host:port>, such as 127.0.0.1:6379 (no default); refusing to guess; run: nova-redis help
   ```
   exit 2. The binary's documented flag is `--redis` (`spill -h`: "the store's
   address"), with `--addr` kept as the old spelling that prints a NOTE; every
   refusal that misses the address names `--addr`, and the page's whole verb
   block uses `--addr` too. The page is normative and names only serve, spill,
   recall, fn load, fn check, acl render, acl check, acl apply, version and
   help: the four live `install store/bus` and `uninstall store/bus` verbs, the
   `--secrets/--as/--key/--sops/--secret` flags on `serve`, and `acl apply`'s
   `--rotate`/`--drop-old` are all absent from it. Its own rule is "If the code
   and this document disagree, one of them has a bug, and the tests decide
   which". I expected the page and the refusals to name the flags and verbs the
   binary has. Grade: NEXT (the normative page names a different flag
   vocabulary and omits live verbs and flags).

8. `nova-redis spill -h` (the same effect line in `fn load -h`, `acl apply -h`)
   The last line of each is:
   ```
   effect: local write: writes files on this machine
   ```
   `spill` writes `<owner>:<name>` into the store (a remote `--redis` means no
   file on this machine is touched), `fn load` loads the function library into
   the store, and `acl apply` writes the store's ACL file through `ACL SAVE`.
   The effect vocabulary names a *store write*, and these are the verbs that
   are one. I expected the effect line to say the store is written, not a file
   on this machine. Grade: NEXT (help that misstates what the verb touches).

9. `nova-redis install store -h` (the same in `install bus -h`)
   Its `example:` line is
   ```
   example: nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>
   ```
   angle-bracket placeholders and a `~/nova-bench` path, so it cannot run as
   printed. The onboarding standard says the per-verb `example:` block carries
   no placeholder and its lines run; I expected a pasteable first line in the
   verb's own help. Grade: NEXT (an unrunnable example in the verb help).

10. `nova-redis bogus` (and `nova-redis help bogus`)
    Printed:
    ```
    REDIS REFUSED: unknown verb "bogus"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help
    ```
    exit 2. The usage above it documents `nova-redis help [<verb>]`, but the
    verb list this refusal prints omits `help`, and the line is prefixed
    `REDIS` rather than the tool's name. (A near miss does get a hint: `srve`
    adds `did you mean serve?`.) I expected the list the usage gives and the
    tool's own name on its own refusal. Grade: NEXT (unclear help: the printed
    verb list is not the usage's list).

11. `NOVA_REDIS_PASSWORD=... nova-redis serve --bind 127.0.0.1 --port <held> --dir $STORE/second` (the port already held by another serve)
    Printed (stderr; the child's own lines appear above it):
    ```
    SERVE FAILED err=exit status 1 remedy="run: ls -ld -- $STORE/second; compare directory access and the explicit --bind/--port with the launch error and any redis-server output"
    ```
    exit 1. The child's own output names the cause (`Warning: Could not create
    server TCP listening socket 127.0.0.1:<port>: bind: Address already in
    use` and `Failed listening on port <port> (tcp), aborting.`), but the one
    line the tool prints for a failed launch says only `exit status 1` and its
    remedy asks the reader to go find that line. I expected the tool's own line
    to name the bind failure it just watched. Grade: NEXT (a FAILED line that
    does not name its cause).

12. `nova-redis install store --secrets $STORE/secrets --as seat --key $STORE/key.txt --sops $STORE/sops --secret PW --units $STORE/units --bind 127.0.0.1 --port <port> --dir $STORE/store --log $STORE/log` then `nova-redis uninstall store --units $STORE/units`
    Printed:
    ```
    INSTALL STORE FAILED err=the unit is written at $STORE/units/nova-redis-store.service and did not load: systemctl --user enable nova-redis-store.service: exit status 1: Failed to enable unit: Unit nova-redis-store.service does not exist
    ```
    exit 1, with the unit file left in `--units`; `uninstall store` then printed
    `UNINSTALL STORE FAILED err=the unit at ... did not unload, and is kept:
    systemctl --user disable --now nova-redis-store.service: exit status 1:
    Failed to disable unit: Unit ... does not exist`, exit 1, and kept the file.
    A `--units` directory systemd does not search is a caller mistake, but the
    tool wrote the file and then failed with no remedy line (no `systemctl
    --user daemon-reload`, no "name the directory systemd reads") and no
    rollback, so a retry leaves the same state. I expected a remedy a cold
    reader can act on in one turn, or the rollback of the file it wrote.
    Grade: NEXT (a write that fails mid-step with no remedy and no rollback).

## What the tool got right

- `serve` is a real wall: `--bind` has no default and a wildcard, a public
  address and a hostname are each refused by name; `--dir` must be absolute;
  `--users` naming a user the ACL file lacks is refused before anything is
  written or launched; the store directory `serve` creates is 0700 and
  `users.acl` starts 0600; SIGTERM is a clean stop (`SERVE STOP`) and a restart
  on the same `--dir` kept `ada:kept` and the four users `acl apply` set.
- `spill`/`recall` refuse a missing owner, a missing, zero, negative or
  malformed TTL, a missing, empty or portless `--redis`, a `--password-env`
  that is not a variable name, an owner with `:` or whitespace, a name with
  whitespace and a missing `--value`, each as one line with a remedy; a miss
  and an expired key are exit 1; a persisted key is `RECALL UNBOUNDED` with its
  reason; a blank value round-trips as `value=-` and a newline and `=` are
  escaped `\x0a` and `\x3d`.
- `spill --dry-run`/`--json` are honest (`written=0 dry_run=true`, `facts`
  holds the key), a later `recall` still misses the planned key, `serve
  --dry-run` creates nothing and prints its plan, `fn load`/`fn check`/`fn load`
  again are `LOADED`/`OK`/`UNCHANGED` with the digest and the remedy names the
  exact `fn load` command, `acl check` reports drift (exit 1) and then `OK`,
  `acl apply` refuses any user it cannot create a password for with the exact
  `--password-env-for` line, and `uninstall store/bus --dry-run` names the unit
  and removes nothing.
- `--addr` as the old spelling prints `SPILL NOTE --addr is --redis` under the
  result, so the migration is visible; `srve` gets `did you mean serve?`.

READ 6/10 — the banner, the per-verb help and the refusal grammar are dense and
mostly true, and the verb model is legible; the page that is supposed to be
normative names a different flag vocabulary and omits four live verbs, the
`--max` sentence is a promise no verb keeps, the store-write effect line is
wrong on three verbs, and the `install` example cannot run, and those are what
keep it low.

USE 6/10 — every verb ran for real, the `serve` lifecycle and the restart kept
both the key and the users, the dry runs are honest and a refusal ends in a
pasteable next command; but the safe `install --dry-run` form fails with an
internal token, the ACL identity the tool itself builds cannot spill a key, an
apply leaves the ACL file 0644 until the next start, and a held port is
reported without its cause.

NOT DONE: a real (non-`--dry-run`) `install store`/`install bus` load and a real
`uninstall` unload were run only against a `--units` directory outside
systemd's search path, so they failed at the `systemctl --user` step (finding
12) rather than through a whole load/unload cycle on a unit systemd reads; the
`serve --secrets/--as/--key/--sops/--secret` path was graded on `--dry-run` and
its help (no nova-secrets store on the bench). The card's named test
`TestDocsTreeIsConsistent` does not exist in `./internal/docs` at this tip; the
gate the card names runs what is there. The defect START states is a dogfooding
task, not a code defect, so STEP 1's "verify the defect still exists" has no
code defect to re-check; nothing in this branch changes code.

urgent=2 next=10
