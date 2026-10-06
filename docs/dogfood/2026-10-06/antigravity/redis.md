# nova-redis dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-redis -h`, `nova-redis help`, `nova-redis help <verb>`, `nova-redis <verb> -h` and the tool's page `docs/SPEC-REDIS.md`. Built
from the staged checkout at d762f545478f8c5118ed9342f69fd23d8b2d5042 and used
as `nova-redis v1.0.1-0.20261006205236-d762f545478f linux/amd64 go1.26.6`:
every verb once with its real flags against a scratch `redis-server` on the
Linux bench's loopback (a fresh store under `$STORE`, invented values only), a
stop and a restart on the same `--dir`, and the refusals too. The bench-home
paths in the transcripts below are written `$STORE`; the binary is
`nova-redis`. The scratch store was thrown away; no finding was fixed here.

## Findings

1. `nova-redis install store --dry-run`
   Printed (stderr):
   ```
   INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   exit 1; `nova-redis install bus --dry-run` prints the same with `INSTALL-BUS`.
   I expected the documented safe form to refuse the way the same call refuses
   without `--dry-run`: `nova-redis install store --units $STORE/units --dir $STORE/inst` prints `INSTALL-STORE REFUSED: the unit carries no password ... it names no --secrets, --as, --key, --sops, --secret; nothing was written; run: nova-redis help` at exit 2. With `--dry-run` the missing-flag check never
   runs, a usage error is reported as `FAILED` (exit 1), the reader is shown an
   internal token (`Call.DryRun`), and the line claims "it may have written" for
   a dry run that wrote nothing, with no remedy.
   Grade: URGENT (a failure with no remedy; the documented safe install form is
   broken).

2. `nova-redis acl apply --addr 127.0.0.1:6390 --password-env-for coordinator=coordinator_pw --password-env-for bench=bench_pw --password-env-for ns-table=table_pw --password-env-for ns-friend=friend_pw`
   Printed (stderr):
   ```
   ACL-APPLY REFUSED: invalid value for --password-env-for (--password-env-for wants <user>=<VARIABLE> (capital letters, digits and underscores), got "coordinator=coordinator_pw"): it wants <user>=<VARIABLE>, repeatable: the variable holding the password a user apply creates gets; a user the store lacks is created only with one; run: nova-redis acl apply -h
   ACL-APPLY REFUSED: unknown flag --password-env-for; the flags of acl apply are --addr, --dry-run, --password-env, --password-env-for, --user; did you mean --password-env-for?; run: nova-redis acl apply -h
   ```
   exit 2. I expected one refusal for the bad value and its remedy; the
   repeatable flag the help advertises instead draws a second line that calls
   the flag unknown, lists it among the flags, and suggests itself. The same
   flag twice with only the second value bad prints one line, so the
   contradiction is not "one line per occurrence".
   Grade: NEXT (a refusal that contradicts itself on a bad input).

3. `nova-redis help` and `docs/SPEC-REDIS.md`
   The banner's usage block lists
   ```
   nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> ...
   nova-redis install bus --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> ...
   nova-redis uninstall store [--units <dir>] [--dry-run]
   ```
   and `serve ... [--secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>]`; the page's normative "The verbs" block names only serve,
   spill, recall, fn load, fn check, acl render, acl check, acl apply, version
   and help, and its `serve` line carries no secrets flags. I expected the page
   the card calls the tool's page to name every verb the binary declares (the
   page's own rule: "This spec is normative ... one of them has a bug").
   Grade: NEXT (four live verbs and serve's auth flags are absent from the page).

4. `BENCH_PW=benchpw nova-redis acl apply --addr 127.0.0.1:6390 --password-env-for bench=BENCH_PW` (after the live `bench` user was dropped with `redis-cli acl deluser bench`)
   Printed:
   ```
   ACL SET user=bench role=member
   ACL APPLY OK users=4 set=1 saved=acl-file library=5ad34e439bc4996e store=127.0.0.1:6390
   ```
   and `ls -l $STORE/users.acl` then printed `-rw-r--r-- ... users.acl`.
   `docs/SPEC-REDIS.md` rule 6 and `serve -h` say the file is mode 0600; the
   `ACL SAVE` an apply of a changed user runs left it world-readable, and the
   next `serve` start rewrote it `-rw-------`. The store directory `serve`
   creates is 0700, but a pre-existing `--dir` need not be.
   Grade: NEXT (the promised 0600 is restored only on the next launch).

5. `nova-redis acl apply --addr 127.0.0.1:6390` (the store lacks the four rendered users)
   Printed:
   ```
   ACL MISSING user=coordinator role=coordinator
   ACL MISSING user=bench role=member
   ACL MISSING user=ns-table role=table
   ```
   then `ACL APPLY REFUSED users=4 missing=coordinator,bench,ns-table,ns-friend: a user the store lacks is created only with a password; run: nova-redis acl apply --addr 127.0.0.1:6390 --password-env-for coordinator=<VARIABLE> ...`, exit 1. I expected exit 2: the banner's table says 2 is "a usage error, a flag refused before dialling" and reserves 1 for "ran and said NO"; a missing `--password-env-for` in a line that says `REFUSED` is the first.
   Grade: NEXT (an exit code the banner's own table denies).

6. `env -u NOVA_REDIS_PASSWORD nova-redis recall --addr 127.0.0.1:6399 --owner ada --name note`
   Printed:
   ```
   RECALL REFUSED key=ada:note class=unreachable: redis at 127.0.0.1:6399 as the default user, no password: unreachable: dial tcp 127.0.0.1:6399: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-redis help
   ```
   exit 2. `docs/SPEC-REDIS.md` says a user whose password variable is empty "is
   refused (exit 2) before anything is dialled"; the same call against the live
   store reports `class=auth-refused ... login refused: NOAUTH`, so the empty
   password path dials, and against an unreachable address the dial error is
   what the reader gets.
   Grade: NEXT (the normative page's pre-dial promise does not hold).

7. `nova-redis fn check --json --addr 127.0.0.1:6390` (and `acl check --json`, `acl apply --json`, `serve --json`, `install store --json`, `uninstall store --json`)
   Printed:
   ```
   FN-CHECK REFUSED: unknown flag --json; the flags of fn check are --addr, --password-env, --user; run: nova-redis fn check -h
   ```
   exit 2. The banner discloses which verbs take `--json`, but the standard the
   tool is built to says "Every verb accepts `--json`" with "one output
   structure, two renderings"; `fn check`, `acl check` and `acl apply` are
   exactly the verbs whose facts (a digest, the live-vs-rendered user rows) a
   caller wants to parse, and none has a JSON rendering.
   Grade: NEXT (a missing flag against the stated standard).

8. `nova-redis bogus`
   Printed:
   ```
   REDIS REFUSED: unknown verb "bogus"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus, version; run: nova-redis help
   ```
   exit 2. The usage above it documents `nova-redis help [<verb>]`, but the verb
   list this refusal prints omits `help`, and the line is prefixed `REDIS`
   rather than the tool's name; a bare `nova-redis` answers the same way.
   Grade: NEXT (unclear help: the list is not the list the usage gives).

9. `env NOVA_REDIS_PASSWORD=... nova-redis serve --bind 127.0.0.1 --port 6390 --dir $STORE/fresh6` (the port is already held by another serve)
   Printed (stderr):
   ```
   SERVE FAILED err=exit status 1 remedy="run: ls -ld -- '$STORE/fresh6'; compare directory access and the explicit --bind/--port with the launch error and any redis-server output"
   ```
   exit 1. The child's own output names the cause (`Could not create server TCP listening socket 127.0.0.1:6390: bind: Address already in use`), but the one
   line the tool prints for a failed launch says only `exit status 1` and its
   remedy asks the reader to find that line in the raw output.
   Grade: NEXT (a FAILED line that does not name its cause).

10. `nova-redis install store -h` (the same in `install bus -h`)
    Its `example:` line is
    ``` nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME> ```
    — angle-bracket placeholders and a `~/nova-bench` path, so it cannot run as
    printed. I expected the verb's own first line to be pasteable, the standard
    the banner's `example:` block meets.
    Grade: NEXT (an unrunnable example in the verb help).

## What the tool got right

- `serve` is a real wall: `--bind` has no default, and a wildcard, a public
  address and a hostname are each refused by name; `--dir` must be absolute; a
  `--users` user the ACL file lacks is refused before anything is written or
  launched; the store directory `serve` creates is 0700.
- `spill`/`recall` refuse a missing owner, a missing, zero or negative TTL, a
  missing or malformed `--addr`, a `--password-env` that is not a variable
  name, a user name with whitespace and a wrong password, each as one line with
  a remedy; a miss and an expired key are exit 1, a key whose TTL was removed
  is `RECALL UNBOUNDED` with its reason, and recall escapes a blank, a newline
  and `=` as `\x20`, `\x0a`, `\x3d` so the line parses.
- `spill --dry-run`/`--json` and `recall --json` are honest, `version --json`
  renders the same value, and `fn load` / `fn check` / `fn load` again are
  `LOADED` / `OK` / `UNCHANGED` with the digest; a stop and a restart on the
  same `--dir` kept `ada:note` (AOF) and the users `acl apply` set still logged
  in, with the default user still wanting the password.
- `acl render` opens no store, `acl apply --dry-run` plans without writing,
  `install store/bus --dry-run` prints the exact unit, `uninstall store/bus --dry-run` names the unit and removes nothing, and `version extra`
  and an unknown flag are answered with the names there are.

READ 6/10 — the banner, the per-verb help and the refusal grammar are dense and mostly true, and the verb model is legible; the normative page omitting four verbs and serve's auth flags, the unrunnable `install` example and the dry run that fails with an internal token are what keep it low.

USE 7/10 — every verb ran for real on the scratch store, the dry runs are honest and a refusal ends in a pasteable next command; but the only safe `install` form fails, a changed `acl apply` leaves the ACL file 0644 until the next start, an empty password still dials, and `acl apply`'s missing flag exits 1.

NOT DONE: a real (non-`--dry-run`) `install store`/`install bus` was not run —
it writes a user unit and loads it, which starting a server on this machine is
out of scope for — so those two verbs are graded on `--dry-run` and the
refusals; the `serve --secrets/--as/--key/--sops/--secret` path was graded on
`--dry-run` and its help (no nova-secrets store on the bench). The card's
named test `TestDocsTreeIsConsistent` does not exist in `./internal/docs` at
this tip (the gate below runs what is there).

urgent=1 next=9
