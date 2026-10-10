# nova-config READ and USE rating, nova-tools 1.2.0

Rater: rater
Build: 65416146b5bb
READ: 9/10
USE: 8.5/10

Built and run on a Linux machine in a scratch directory. Used with `--file` store only: no PostgreSQL, no Redis, no server started. `nova-config help`, every verb's `-h`, `migrate`, `machine add`, `machine set`, `machine list`, `machine history`, `kinds`, and `status` were all exercised.

## Reasons

READ. `nova-config help` answers what it is in one line, explains the two stores, and gives a first run with no database. Every verb's `-h` shows its effect, required flags, an example, and exit codes. SPEC-CONFIG.md is well-structured and covers the boundary, kinds, schema, history, and apply. cmd/nova-config/main.go is 668 lines, a marked reduction from earlier versions. `migrate` sets up a file store correctly.

What keeps READ at 9. The banner offers `--seat` as a store for every verb in `help`, but `machine list --seat` is an unknown flag. The inventory first run names a fixture by a path relative to a source checkout, which refuses as printed from anywhere else. The spec has small drifts: loop has `width` in the field table but `loop add --width` is unknown; fleet has `bus` and `loops_dir` in the binary but not in the spec field table; tier is missing from apply order in the spec. The friend kind's help uses "she/her" pronouns. `login --as` means the seat while `--as` on every other verb means the actor. Every verb's `-h` repeats `migrate --dry-run` and `machine self` exit codes that don't apply.

USE. The tool works well on a file store. `migrate` made the file (35 migrations applied), and a second `migrate` applied none. Adding machines, friends, loops, routes, and tiers all worked. `list`, `show`, and `history` agreed with every write. Refusals are excellent: missing flags, bad values, duplicates, refs naming no row, and disabled routes without notes all refuse in one line with a runnable remedy. `--dry-run` writes nothing. `--json` prints one envelope.

What keeps USE at 8.5. Three issues: under `--json` a refusal leaves stdout empty and prints only stderr; unknown flags hide bad values beside them; a dead Redis prints 2-4 library pool lines before the REFUSED line. The fixture path in inventory refuses outside a checkout. A machine with no `--width` shows `width=-` in `show` while `machine width` says `width=default`. Friend list `--json` gives numbers as strings while `machine width --json` gives numbers. `machine remove` held by fleet points at `machine list` instead of `fleet set --store`.

A 10 would put the refusal envelope on stdout under `--json`, silence the Redis pool logger, fix the inventory fixture path, drop `--seat` from the banner's claim on reads, and make list/show consistent with machine width on `width=default`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m1 --json --file try.json` (duplicate) | Exit 1, stdout empty, stderr prints only the REFUSED line. Under `--json` refusals should print the result envelope. | Print the result envelope on stdout for a refusal, with status refused and the remedy in notes. | M |
| 2 | `nova-config machine add m9 --slots no --bogus 1 --file try.json` | Unknown flag `--bogus` is named but the bad value for `--slots` is not. | Collect unknown flags and bad values into one refusal line. | S |
| 3 | `nova-config apply --redis 127.0.0.1:1 --dry-run` | Four `redis: ... pool.go:762` library lines before the REFUSED line on stderr. | Install a quiet logger on the Redis client to suppress pool errors. | S |
| 4 | cmd/nova-config/verbs.go:176 | Inventory first run names `fleet/testdata/inventory-fixture.yml`, a checkout path that refuses as printed. | Embed the fixture or add `--fixture example` with a built-in fixture. | S |
| 5 | pkg/config/inventory.go:412 | A fixture of wrong shape dumps a Go struct type and lists fleet as `store, coordinator`, omitting required `redis_port` and `pg_dsn`. | Say the YAML path that failed and the wanted mapping with all four fleet fields. | S |
| 6 | pkg/config/inventory.go:207 | Under `--fixture`, missing `redis_port` and `pg_dsn` names `nova-config fleet set ... then apply` as the remedy, a store write the fixture does not read. | Under `--fixture`, name the fixture file and the two keys to add to its fleet mapping. | S |
| 7 | cmd/nova-config/inventory.go:142 | `--host nope` against a fixture points at `nova-config machine list`, which reads the store, not the fixture. | Under `--fixture`, the remedy is the same inventory with `--list`. | S |
| 8 | cmd/nova-config/main.go:109 | The banner offers `--seat` as a store for every verb; `machine list --seat` is unknown (only writes take it). | Accept `--seat` on every verb that opens the store, or clarify in the banner that reads do not take it. | S |
| 9 | pkg/config/kind.go:412 | The friend kind's Doc and field help use "she/her" pronouns; the tool cannot know a friend's pronouns. | Write "the friend's slots", "the tiers it can do", or use they/their. | S |
| 10 | pkg/config/kind.go:444 | sprint set's decide flags carry calibration history (AUC, label counts) in `-h`; help lines run past 400 characters. | Keep the bar's meaning and default in `-h`; cite the spec section for calibration details. | S |
| 11 | cmd/nova-config/login.go:320 | `login --as` is the seat; on every other verb `--as` is the actor. | Name it `--seat` and keep `--as` the actor everywhere. | S |
| 12 | `nova-config help machine` | Prints `width` and `self` usage lines and four examples, not the six verbs (add, set, remove, list, show, history). | Print the kind's usage block from the banner, then its examples. | S |
| 13 | pkg/config/store.go:247 | Migrate seeds rows with `created=2023-11-14T22:13:20Z`, the in-memory store's fixed test clock. | Stamp the file store's seed rows with the wall clock. | S |
| 14 | `nova-config machine show m2 --file try.json` (no --width) | Shows `width=-`; `machine width m2` prints `width=default` and the add NOTE says `width=default`. | Print `width=default` in list and show. | S |
| 15 | `nova-config friend list --json --file try.json` | Every field is a JSON string (`"slots":"4"`, `"width":"8"`), while `machine width --json` gives `"width":6`. | Emit int and bool fields as JSON numbers and booleans. | S |
| 16 | `nova-config machine remove m1 --file try.json` (held by fleet) | Refused with `run: nova-config machine list`, which does not free it. | Point at the holder: `nova-config fleet set --store <machine> --as <name>`. | S |
| 17 | every verb's `-h` | The exit-code block repeats `migrate --dry-run` and `machine self` codes that don't apply to that verb. | Print each verb's own exit codes only. | S |

## Good, keep

- The refusal grammar on a store write: one line, every independent problem named, the reason, and a runnable next command with the same `--file`.
- The ref discipline: a ref naming no row and a row another kind names are both refused with the holder named.
- A disabled route must carry its reason (`--note`).
- The `--file` store runs every verb but apply's write and inventory with no database.
- History records every write with actor and before/after.
- Passwords never on the line: `--pg_dsn` with a password is refused without echoing it.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| a 1,646-line main.go that renders every result twice | FIXED | cmd/nova-config/main.go is 668 lines; kind verbs are in cmd/nova-config/kindverbs.go |
| machine width --json breaks the envelope | FIXED | `machine width m1 --json` prints the family envelope |
| every refusal under --json leaves stdout empty | STILL THERE | `machine add m1 ... --json` (duplicate) exits 1, stdout empty |
| an unknown flag hides a bad value | STILL THERE | `machine add m9 --slots no --bogus 1` names only `--bogus` |
| a failed dial prints library logs before REFUSED | STILL THERE | `apply --redis 127.0.0.1:1 --dry-run` prints pool.go lines |
