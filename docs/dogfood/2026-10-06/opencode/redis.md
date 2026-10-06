# nova-redis — dogfood report, 2026-10-06 (opencode)

Tool: `cmd/nova-redis` at tip `8076dfdfcd8593f1fb3f3156310a2ec61344be97` (binary reports
`v1.0.1-0.20261006184440-8076dfdfcd85 linux/amd64`).

Read first, as a stranger: `nova-redis -h`, `nova-redis help`, every `<verb> -h`, and
`docs/SPEC-REDIS.md`. Used against the only reachable store, `127.0.0.1:6379`, with scratch
keys under owner `dogfoodoc` (TTL'd). Writes that replace shared state — `serve`, `install`,
`uninstall`, `acl apply`, `fn load` — were exercised on their `--dry-run` path, except `fn load`,
which has no dry run (finding 3). Every verb was run; `fn load` was not run for real (see Not done).

## Findings

### 1. `install ... --dry-run` with a missing required flag fails with a lie and exit 1 — URGENT

command: `nova-redis install store --dry-run --secrets <dir> --as bench --key <file> --sops <bin> --units <dir>` (no `--secret`)

printed:
```
INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
```

expected: `INSTALL-STORE REFUSED: --secret is required: ...; run: nova-redis install store -h` at exit 2, naming the missing flag and confirming nothing was written. Instead it exits 1, names no flag, offers no remedy, and says it "may have written" — the `--units` directory stayed absent. Every required install flag (`--secrets`, `--as`, `--key`, `--sops`, `--secret`, and the same on `install bus`) triggers this. Without `--dry-run` the identical call refuses correctly at exit 2; the safe preview path is the one that fails worst.

### 2. Store writes are printed as local file writes — URGENT

command: `nova-redis spill -h` (also `nova-redis fn load -h`, `nova-redis acl apply -h`)

printed:
```
effect: local write: writes files on this machine
exit codes: 0 done (spill written, recall found, fn load done, ...
```

expected: `spill`, `fn load` and `acl apply` write to the Redis store named by `--addr` (the tool's own code comments call them "a store write"); the effect line should say so, not "writes files on this machine". A stranger reading `spill -h` is told the wrong destination for the write.

### 3. `fn load` is a store write with no `--dry-run` — NEXT

command: `nova-redis fn load -h`

printed:
```
flags:
  --addr <string>  the store's address as <host:port>, such as 127.0.0.1:6379 (no default)
  --password-env <string>  the NAME of the variable that holds the password, ...
```

expected: a `--dry-run`, like every other writing verb (`spill`, `acl apply`, `serve`, `install`, `uninstall`), so replacing the store's live `nova_sprint` library can be previewed. `fn check` on the only store reported `STALE` (`loaded=90266b4d1604bed6 want=5ad34e439bc4996e`); with no dry run and no scratch store, the replacement cannot be seen before it happens.

### 4. `acl render` output is unbounded and uncappable — NEXT

command: `nova-redis acl render`

printed:
```
ACL FAMILY name=tables keys=table:*,tables
ACL FAMILY name=views keys=view:*,views
ACL FAMILY name=sprint keys=sprint:*
```

expected: the bounding every listing gets — `--max <n>` with a `MORE shown=<n> total=<n>` line, or at least `--json`. After the 12 FAMILY lines `acl render` prints four `ACL SETUSER` lines, each roughly 2 KB of `+fcall|...` tokens (about 8 KB total), and takes no flags at all, so it can be neither cut nor parsed.

### 5. The banner promises `--max`/`MORE` that no verb honours — NEXT

command: `nova-redis help`

printed:
```
A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest.
`<verb> -h` lists a verb's flags.
```

expected: no nova-redis verb lists or accepts `--max`; the only listing-like verb, `acl render`, refuses every flag (finding 4). A reader who follows the sentence is refused.

### 6. `recall` of a foreign key gives an unactionable remedy and treats types inconsistently — NEXT

command: `nova-redis recall --addr 127.0.0.1:6379 --owner dogfoodoc --name s_noTtl` (a plain string key set outside the tool)

printed:
```
RECALL FAILED key=dogfoodoc:s_noTtl class=other: redis at 127.0.0.1:6379 as the default user, no password: failed: WRONGTYPE Operation against a key holding the wrong kind of value; next: the store answered, so the connection stands: read the refusal as the command's own
```

expected: the same clear answer a foreign hash key already gets — `RECALL UNBOUNDED key=... remedy="an unbounded key is a bug; it was not written by nova-redis spill"`. A string key (with or without a TTL) instead fails with `WRONGTYPE` and the remedy "read the refusal as the command's own", which tells the reader nothing to do.

### 7. Most verbs refuse `--json`, against the family's one-value contract — NEXT

command: `nova-redis fn check --json --addr 127.0.0.1:6379` (also `serve`, `acl render`, `acl check`, `acl apply`, `install`, `uninstall`)

printed:
```
FN-CHECK REFUSED: unknown flag --json; the flags of fn check are --addr, --password-env, --user; run: nova-redis fn check -h
```

expected: the same value the line form prints, as one JSON object, so a caller can read a drift or load report without re-parsing `ACL MISSING ...` lines. Only `spill`, `recall` and `version` accept `--json`; the banner admits the gap rather than closing it.

### 8. `install` hides the defaults it will use for `--bind` and `--port` — NEXT

command: `nova-redis install store --dry-run --secrets <dir> --as bench --key <file> --sops <bin> --secret PW --units <dir>`

printed:
```
INSTALL STORE DRY-RUN unit=.../units/nova-redis-store.service; nothing was written or loaded
[Unit]
Description=nova store: the sprint's store, a Redis server (nova-redis serve)
```

expected: `install store -h` should state the defaults, as it does for `--dir`, `--log` and `--units`. The unit it prints binds `127.0.0.1` on port `6380` (bus: `6381`), but `--bind` and `--port` list no default, so the fallback is hidden until `--dry-run` reveals it.

### 9. `fn help` is refused while `fn -h` and `help fn` work — NEXT

command: `nova-redis fn help`

printed:
```
REDIS REFUSED: unknown verb "fn help" in fn; did you mean fn check? the verbs are fn load, fn check; run: nova-redis fn -h
```

expected: one consistent help door. `nova-redis fn -h` and `nova-redis help fn` both print the group's help at exit 0, but a reader who follows the banner's `help [<verb>]` line to `fn help` is refused.

### 10. `spill` has no `--op`, so a lost reply cannot be retried idempotently — NEXT

command: `nova-redis spill --op abc --addr 127.0.0.1:6379 --owner dogfoodoc --name note --ttl 10m --value hi`

printed:
```
SPILL REFUSED: unknown flag --op; the flags of spill are --addr, --dry-run, --json, --name, --owner, --password-env, --ttl, --user, --value; run: nova-redis spill -h
```

expected: a write verb takes an op id, so after the spec's `SPILL UNCONFIRMED` case (a reply lost after the store took it) a caller can retry the same operation safely. Today the only remedy is a `recall`, and a second `spill` is a second write.

## Not done

- `fn load` was not run for real. The only reachable store is the shared live store, and `fn check` reports its library `STALE` against this binary, so `fn load` would have replaced the fleet's live `nova_sprint` library. With no `--dry-run` (finding 3), and the rule against starting a server, there was no safe store to exercise it on.
- `serve`, `install`, `uninstall` and `acl apply` were exercised only on their `--dry-run` paths: the card's rules forbid starting a server or loading a unit on this machine. `spill`, `recall`, `fn check`, `acl check`, `acl render` and `version` ran for real against `127.0.0.1:6379`.

## Gate

`go test -count=1 -timeout 600s ./internal/docs ./internal/ci`:

```
ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.233s
ok  	github.com/mas-bandwidth/nova-tools/internal/ci	17.174s
```

The card's named test, `./internal/docs TestDocsTreeIsConsistent`, does not exist at this tip:
`go test -run TestDocsTreeIsConsistent ./internal/docs` reports `ok ... [no tests to run]`.

## Scores

READ 8/10 — the banner, every verb's `-h`, its effect/exit table and its refusals are unusually complete and precise for a cold reader; the store-write effect line lies (finding 2) and two install defaults are hidden (finding 8).

USE 7/10 — spill/recall/fn check/acl check/serve dry-run behaved exactly as documented and each refusal names one fixable problem; but the `install --dry-run` failure path (finding 1) is a trap on the safe path, `fn load` cannot be previewed, and `acl render` cannot be parsed.

urgent=2 next=8
