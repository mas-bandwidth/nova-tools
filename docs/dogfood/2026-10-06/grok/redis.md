# nova-redis dogfood — grok (zhi), 2026-10-06

Read as a stranger: only `nova-redis -h`, `nova-redis help`, `nova-redis help
<verb>`, `nova-redis <verb> -h` and the tool's page `docs/SPEC-REDIS.md`. Built
from the checkout at dffea996e566b4d800b32667f28f4d5118191387 and used as
`nova-redis v0.0.0-20261007145710-dffea996e566 linux/amd64 go1.26.6`: every
verb once with its real flags against a scratch `redis-server` on the Linux
bench's loopback (a fresh store under `$STORE`, invented values only), a stop
and a restart on the same `--dir`, and the refusals too. The bench-home paths
in the transcripts below are written `$STORE`; the binary is `nova-redis`. The
scratch store was thrown away; no finding was fixed here.

## Findings

1. `nova-redis install store --dry-run` (the same in `install bus --dry-run`)
   Printed (stderr):
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   exit 1. I expected the documented safe form to refuse the way the same call
   refuses without `--dry-run`: `nova-redis install store` prints `INSTALL-STORE
   REFUSED: the unit carries no password ... it names no --secrets, --as, --key,
   --sops, --secret; nothing was written; run: nova-redis help` at exit 2. With
   `--dry-run` the required-flag check is skipped, a usage error is reported as
   `FAILED` (exit 1), the reader is shown an internal token (`Call.DryRun`), and
   the line claims "it may have written" for a run that wrote nothing, with no
   remedy. `install store --dry-run` with all its flags works (prints the unit,
   exit 0) and `serve --dry-run` with no flags refuses correctly, so the hole is
   the dry run's own validation path.
   Grade: URGENT (the documented safe form of a writing verb fails with no
   remedy and a line that is false about what it did).

2. `NOVA_REDIS_PASSWORD=... BENCH_PW=... nova-redis acl apply --addr 127.0.0.1:6390 --password-env-for bench=BENCH_PW`
   Printed:
   ```
   ACL OK user=coordinator role=coordinator
   ACL MISSING user=bench role=member
   ACL OK user=ns-table role=table
   ```
   then `ACL SET user=bench role=member` and `ACL APPLY OK users=4 set=1
   saved=acl-file library=5ad34e439bc4996e store=127.0.0.1:6390`, exit 0.
   `ls -l $STORE/users.acl` then printed `-rw-r--r-- ... users.acl`.
   `docs/SPEC-REDIS.md` rule 6 and `serve -h` say the file is mode 0600; the
   `ACL SAVE` an apply of a changed user runs left it world-readable, and the
   next `serve` start rewrote it `-rw-------`. The store directory `serve`
   creates is 0700, but a pre-existing `--dir` need not be.
   Grade: NEXT (the promised 0600 is restored only on the next launch).

3. `nova-redis acl render`
   Printed:
   ```
   ACL FAMILY name=tables keys=table:*,tables
   ACL FAMILY name=views keys=view:*,views
   ACL FAMILY name=sprint keys=sprint:*
   ```
   exit 0, twelve `ACL FAMILY` lines and no user row. `docs/SPEC-REDIS.md` says
   "`acl render` prints the build's users and opens no store"; the build's users
   are `coordinator`, `bench`, `ns-table` and `ns-friend`, which `acl check`
   prints with their roles and `acl render` never names. I expected the page's
   sentence or the output to change so the two agree.
   Grade: NEXT (the normative page describes a rendering the verb does not
   produce).

4. `nova-redis acl render --max 2` and `nova-redis acl check --addr 127.0.0.1:6390 --max 2`
   Printed:
   ```
   ACL-RENDER REFUSED: unknown flag --max; acl render takes no flags; run: nova-redis acl render -h
   ACL-CHECK REFUSED: unknown flag --max; the flags of acl check are --addr, --password-env, --user; run: nova-redis acl check -h
   ```
   both exit 2. The banner says "A verb that lists takes `--max <n>` (default
   20, 0 lists all) and says `MORE` for the rest"; `acl render` lists twelve
   families and `acl check` lists four users, and neither takes the flag or
   prints `MORE`. I expected the listed verbs to take `--max`, or the banner to
   say which listing verbs do not.
   Grade: NEXT (a banner promise the listing verbs refuse).

5. `nova-redis fn check --json --addr 127.0.0.1:6390` (and `acl check --json`, `acl apply --json`, `acl render --json`)
   Printed:
   ```
   FN-CHECK REFUSED: unknown flag --json; the flags of fn check are --addr, --password-env, --user; run: nova-redis fn check -h
   ```
   exit 2. The banner discloses which verbs take `--json`, but the standard the
   tool is built to says every verb builds one value rendered as lines or as
   JSON; `fn check`'s digest and `acl check`'s user rows are exactly the facts a
   caller parses, and none of them has a JSON rendering.
   Grade: NEXT (a missing flag against the stated one-structure-two-renderings
   rule).

6. `nova-redis spill -h` (and `nova-redis fn load -h`, `nova-redis acl apply -h`)
   The last line of each is:
   ```
   effect: local write: writes files on this machine
   ```
   `spill` writes `<owner>:<name>` into the Redis store, `fn load` loads the
   function library into the store, and `acl apply` writes the store's ACL file
   through `ACL SAVE`; a remote `--addr` means no file on "this machine" is
   involved. The effect vocabulary names a *store write*, and these are the
   verbs that are one.
   Grade: NEXT (help that misstates what the verb touches).

7. `nova-redis bogus` (and a bare `nova-redis`, and `nova-redis help bogus`)
   Printed:
   ```
   REDIS REFUSED: unknown verb "bogus"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help
   ```
   exit 2. The usage above it documents `nova-redis help [<verb>]`, but the verb
   list this refusal prints omits `help`, and the line is prefixed `REDIS`
   rather than the tool's name. I expected the list the usage gives and the
   tool's own name on its own refusal.
   Grade: NEXT (unclear help: the printed verb list is not the usage's list).

8. `nova-redis install store -h` (the same in `install bus -h`)
   Its `example:` line is
   ```
   example: nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>
   ```
   angle-bracket placeholders and a `~/nova-bench` path, so it cannot run as
   printed. The onboarding standard says the per-verb `example:` block carries
   no placeholder and its lines run; I expected a pasteable first line in the
   verb's own help.
   Grade: NEXT (an unrunnable example in the verb help).

9. `NOVA_REDIS_PASSWORD=... nova-redis serve --bind 127.0.0.1 --port 6390 --dir $STORE/second` (the port is already held by another serve)
   Printed (stderr):
   ```
   SERVE FAILED err=exit status 1 remedy="run: ls -ld -- '$STORE/second'; compare directory access and the explicit --bind/--port with the launch error and any redis-server output"
   ```
   exit 1. The child's own output names the cause (`Warning: Could not create
   server TCP listening socket 127.0.0.1:6390: bind: Address already in use`
   and `Failed listening on port 6390 (tcp), aborting.`), but the one line the
   tool prints for a failed launch says only `exit status 1` and its remedy asks
   the reader to find that line in the raw output.
   Grade: NEXT (a FAILED line that does not name its cause).

10. `nova-redis install store --dry-run --secrets /tmp/s --as seat --key /tmp/k --sops /tmp/sops --secret PW`
    Printed:
    ```
    INSTALL STORE DRY-RUN unit=$STORE/units/nova-redis-store.service; nothing was written or loaded
    [Unit]
    Description=nova store: the sprint's store, a Redis server (nova-redis serve)
    ```
    and its `ExecStart` names `--port "6380"` and the `--dir` default
    (`~/nova-bench/redis/store`) though neither flag was given; `install bus
    --dry-run` picks `6381`. `install store -h` documents the
    `--dir` default but shows no default for `--port`, so the port the unit will
    run on cannot be read from help. The standard says no default stands in for
    a missing input and nothing is hidden.
    Grade: NEXT (a unit port chosen by an undocumented default).

11. `env -u NOVA_REDIS_PASSWORD nova-redis recall --addr 127.0.0.1:6399 --owner ada --name note`
    Printed:
    ```
    RECALL REFUSED key=ada:note class=unreachable: redis at 127.0.0.1:6399 as the default user, no password: unreachable: dial tcp 127.0.0.1:6399: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-redis help
    ```
    exit 2. `docs/SPEC-REDIS.md` says a user whose password variable is empty is
    refused (exit 2) before anything is dialled; the same call against the live
    store prints `class=auth-refused ... login refused: NOAUTH Authentication
    required.`, so the pre-dial refusal never ran and the dial's error is what a
    bad address returns.
    Grade: NEXT (the normative page's pre-dial promise does not hold).

12. `nova-redis help` and `docs/SPEC-REDIS.md`
    The banner's usage block lists
    ```
    nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> ...
    nova-redis install bus --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> ...
    nova-redis uninstall store [--units <dir>] [--dry-run]
    ```
    and `serve ... [--secrets <dir> --as <seat> --key <file> --sops <path>
    --secret <NAME>]`; the page's normative "The verbs" block names only serve,
    spill, recall, fn load, fn check, acl render, acl check, acl apply, version
    and help, and its `serve` line carries no secrets flags. The page's own rule
    is "If the code and this document disagree, one of them has a bug, and the
    tests decide which"; `TestSpecRedisVerbBlockNamesEveryVerb` reads `Name:`
    literals and so cannot see the four verbs whose names are built (`"install "
    + kind`, `"uninstall " + kind`). I expected the page to name every verb the
    binary declares.
    Grade: NEXT (four live verbs and serve's auth flags are absent from the
    page).

13. `nova-redis version --json`
    Printed:
    ```
    {"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-redis v0.0.0-20261007145710-dffea996e566 linux/amd64 go1.26.6"}
    ```
    exit 0. `spill --json` and `recall --json` carry their values under `facts`,
    while `version` adds a `payload` string and leaves `facts` empty; the
    structure the standard states is `result`, `facts`, `items`, `notes`, one
    value with two renderings, so a parser that reads `facts` must special-case
    the one verb every tool has.
    Grade: NEXT (a second JSON shape for the same structure).

## What the tool got right

- `serve` is a real wall: `--bind` has no default and a wildcard, a hostname and
  a public address are each refused by name; `--dir` must be absolute; `--users`
  naming a user the ACL file lacks is refused before anything is written or
  launched; the store directory `serve` creates is 0700 and `users.acl` starts
  0600.
- `spill`/`recall` refuse a missing owner, a missing, zero, negative or
  malformed TTL, a missing, empty or portless `--addr`, a `--password-env` that
  is not a variable name, a user name with whitespace, and a wrong password,
  each as one line with a remedy; a miss and an expired key are exit 1; a key
  whose TTL was removed is `RECALL UNBOUNDED` with its reason; recall escapes a
  newline and `=` as `\x0a` and `\x3d`, and a blank value round-trips as
  `value=-`.
- `spill --dry-run`/`--json` are honest (`written=0 dry_run=true`, and a later
  `recall` still misses the planned key), `serve --dry-run` creates nothing and
  prints its plan, `fn load`/`fn check`/`fn load` again are `LOADED`/`OK`/
  `UNCHANGED` with the digest, a stop and a restart on the same `--dir` kept
  `ada:note` and the users `acl apply` set, `uninstall store/bus --dry-run`
  names the unit and removes nothing, and `acl apply --dry-run` plans without
  writing.

READ 6/10 — the banner, the per-verb help and the refusal grammar are dense and
mostly true, and the verb model is legible; the four page-omitted verbs, the
`--max` promise the listing verbs refuse, the wrong effect line on every
store-write verb and the unrunnable `install` example are what keep it low.

USE 7/10 — every verb ran for real on the scratch store, the dry runs are
honest, a refusal ends in a pasteable next command and the restart kept both
keys and users; but the safe `install --dry-run` form fails, a changed `acl
apply` leaves the ACL file 0644 until the next start, an empty password still
dials, and a held port is reported without its cause.

NOT DONE: a real (non-`--dry-run`) `install store`/`install bus` was not run —
it writes a user unit and loads it with launchctl or systemctl, which starting a
unit on the bench is out of scope for — so those two verbs are graded on
`--dry-run` and the refusals; the `serve --secrets/--as/--key/--sops/--secret`
path was graded on `--dry-run` and its help (no nova-secrets store on the
bench). The card's named test `TestDocsTreeIsConsistent` does not exist in
`./internal/docs` at this tip; the gate the card names runs what is there.

urgent=1 next=12
