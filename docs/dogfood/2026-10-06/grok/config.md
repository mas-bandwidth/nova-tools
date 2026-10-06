# nova-config dogfood — grok (zhi), 2026-10-06

Read as a stranger: only `nova-config -h`, `nova-config help`, `nova-config <verb> -h`
and the page under `docs/` (docs/SPEC-CONFIG.md, docs/nova-config/README.md). Built
from the staged checkout at cb5fb8d4c3290f710d22a21a86aa8d229e4db905 and used as
`nova-config v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`: the `example:`
block run as printed in a scratch dir, then every verb once against a scratch `--file`
store, the refusals too. `apply` and `status` were run with their real flags against a
read-only Redis (`--check`/`--dry-run`); the real delivery of `apply` into Redis was not
run, because no server may be started for this card and the store that answered is not
mine to write.

## Findings

1. An unset machine width is reported as `member=true`, and `--json` carries
   `"width":0,"member":true` — URGENT

   `nova-config machine width m2 --file demo.json`
   ```
   CONFIG WIDTH machine=m2 width=default member=true
   ```
   `nova-config machine width m2 --file demo.json --json`
   ```
   {"result":{"verb":"machine width","status":"ok","exit":0},"facts":{"machine":"m2","width":0,"member":true,"default":true}}
   ```
   The machine was added with no `--width`. I expected `member=false` (or no member
   claim) and no `width=0` beside it: the page says "a machine with a width of 1 or
   more is a member of the sprint's fleet; a machine with width 0 is not", and that a
   machine whose width is the default "is not a member" until `nova-sprint fleet sync`
   resolves it from a beat this store does not have. `width=0` with `member=true` is
   the one combination the page calls impossible; `machine list --json` spells the same
   state `"width":""`, a third spelling. A script that gates on `member` would treat a
   machine that may resolve to no member as one.
   Grade: URGENT.

2. `tier add` is advertised and has a help page but can never succeed — URGENT

   `nova-config tier add pro --routes pro-grok --as a1 --file demo.json`
   ```
   nova-config tier add REFUSED: tier pro exists; run: nova-config tier set pro --<field> <value> --file demo.json
   ```
   `nova-config tier add mytier --routes flash-b --as a1 --file demo.json`
   ```
   nova-config tier add REFUSED: tier mytier: want one of flash, pro, heavy; run: nova-config tier add -h
   ```
   `tier add -h` prints a normal store-write page (effect line, flags, required marks)
   with no caveat, and a bare `nova-config tier` lists `add` among its verbs. The page
   says the three tier rows are made by migrate "and there is nothing to add". I
   expected `tier add` to refuse the way `fleet add` and `sprint add` do (`fleet is one
   row, created by migrate; want set, show or history`), or not to exist; `tier
   remove -h` at least carries the "made by migrate and never removed" sentence.
   Grade: URGENT.

3. `--seat` is the machine's secrets seat on `add` and the PostgreSQL connection seat
   on `set`, so a machine's required `seat` field cannot be changed — NEXT

   `nova-config machine set m1 --seat newseat --as a1 --file demo.json`
   ```
   nova-config machine set REFUSED: --seat and --file are exclusive: --file keeps the rows in a local file in PostgreSQL's place; set names no field; the fields are user, seat, slots, runners, width, tla, note; run: nova-config machine set -h
   ```
   `nova-config machine set m1 --seat newseat --as a1 --pg postgres://user@host:5432/db`
   ```
   nova-config machine set REFUSED: seat newseat: no row: ~/.config/nova-config/seats.tsv does not exist; the fleet play writes it, one tab-separated row per seat (name, dsn, password env); set names no field; the fields are user, seat, slots, runners, width, tla, note; run: nova-config machine set -h
   ```
   On `machine add`, `--seat` is the machine's field ("its nova-secrets seat"); on
   `machine set` it is the connection seat. The refusal names `seat` among the fields
   while no flag can name it, so a machine added with the wrong seat can only be
   removed and added again, and remove is refused while a fleet, loop or anything else
   names it. I expected one flag to mean one thing, or a way to set the field.
   Grade: NEXT.

4. The `note` is prose but the line renderings hex-escape it, so the saved reason is
   not readable — NEXT

   `nova-config machine set m1 --width 12 --note "held 1:46 PM: reads kernel-bound" --as a1 --file demo.json`
   then `nova-config machine list --file demo.json`
   ```
   MACHINE name=m1 user=nova seat=s1 slots=32 runners=1 width=12 tla=true note=held\x201:46\x20PM:\x20reads\x20kernel-bound
   ```
   `nova-config route show flash-b --file demo.json`
   ```
   ... note=measured:\x202\x20ok\x20of\x2012 created=2026-10-06T20:54:31Z updated=2026-10-06T20:54:31Z
   ```
   `machine history` renders the same field as
   `note=the\x20build\x20machine>held\x201:46\x20PM:\x20reads\x20kernel-bound`. I expected
   the note printed whole and plain (the page: "`route show` and `machine show` print it
   whole"; the standard: a row's reason is prose, plain in the line), the way `--json`
   already prints it. A reader of the line form has to decode `\x20`.
   Grade: NEXT.

5. `login` and `logout` do not take `--json`, and the nearest-flag hint for `--json` is
   `--dsn` — NEXT

   `nova-config login --check --json`
   ```
   LOGIN REFUSED: unknown flag --json; the flags of login are --as, --check, --dsn, --friend, --key, --secret, --sops, --store; did you mean --dsn?; run: nova-config login -h
   ```
   The standard says every verb accepts `--json`, and `logout -h` lists no flags at
   all. I expected `--json` on both, and a hint that does not offer a DSN flag as the
   nearest spelling of `--json`.
   Grade: NEXT.

6. The spec's `answer_rules_off` list is stale: six rules there, ten accepted and
   listed by the tool — NEXT

   `nova-config sprint set --answer_rules_off friend-take --as a1 --file demo.json`
   ```
   CONFIG SET kind=sprint name=sprint rev=8 changed=answer_rules_off
   ```
   `nova-config sprint set --answer_rules_off bogus --as a1 --file demo.json`
   ```
   nova-config sprint set REFUSED: --answer_rules_off "bogus": want a comma list of base-gate, bound, brief-defect, conflict, failed, friend-take, hold-need, late, read-broken, read-late; run: nova-config sprint set -h
   ```
   SPEC-CONFIG.md's sprint row lists `base-gate, bound, brief-defect, conflict, failed,
   late`; the tool accepts four more (`friend-take`, `hold-need`, `read-broken`,
   `read-late`) and `sprint set -h` describes all ten. The spec is the page a reader
   trusts, so it should carry the ten the tool enforces.
   Grade: NEXT.

7. `tier history` refuses a migrate-made row that `fleet history` and `sprint history`
   print as an empty history — NEXT

   `nova-config tier history flash --file demo.json`
   ```
   nova-config tier history REFUSED: tier flash has no history: it was never added; run: nova-config tier list --file demo.json
   ```
   `nova-config fleet history --file demo.json`
   ```
   CONFIG HISTORY kind=fleet name=fleet changes=0
   ```
   Both rows are made by migrate, and `tier show flash` prints the row. I expected one
   rule for migrate-made rows (the page gives a singleton's `history` with no change the
   count line and exit 0), or a `tier history` that prints the count line and exits 0
   too.
   Grade: NEXT.

8. An unreachable Redis prints four client log lines before the one-line refusal — NEXT

   `nova-config apply --dry-run --kind machine --redis 127.0.0.1:1 --file demo.json`
   ```
   redis: 2026/10/06 20:54:58 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/06 20:54:59 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   redis: 2026/10/06 20:54:59 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
   nova-config apply REFUSED: redis: read machines: dial tcp 127.0.0.1:1: connect: connection refused; run: nova-config apply -h
   ```
   I expected the one refusal line; the pool's own logger leaks to stderr and buries it.
   `machine list --redis <the same dead port>` does the same.
   Grade: NEXT.

9. `inventory` prints a `tla` group its help and page do not name, and its `--host`
   refusal drops the `--fixture` the run was given — NEXT

   `nova-config inventory --fixture fleet/testdata/inventory-fixture.yml`
   ```
   "benches": {"hosts": ["bench-a", "bench-b"]}, ... "runners": {"hosts": ["bench-b"]}, "tla": {"hosts": ["bench-b"]}
   ```
   `nova-config inventory --host nosuch --fixture fleet/testdata/inventory-fixture.yml`
   ```
   nova-config inventory REFUSED: --host "nosuch" names no machine row; known machines: bench-a, bench-b; run: nova-config machine list
   ```
   `inventory -h` and docs/nova-config/README.md list all, benches, coordinator, store,
   store_deployer and runners, not `tla`, which the output has (driven by the machine
   `tla` field). The page also says a remedy repeats the store the run was given, so the
   `--host` remedy should carry `--fixture`; without it `machine list` opens the real
   store.
   Grade: NEXT.

10. The `login --dsn` refusal names `--pg` — NEXT

    `nova-config login --dsn postgres://user:pw@host:5432/db --store /s --as a --key /k --secret S --friend f`
    ```
    nova-config login REFUSED: nothing was recorded: --dsn: --pg carries a password; leave it out and export it as the variable NOVA_PG_PASSWORD_ENV names (a ps reads the line); run: nova-config login -h
    ```
    The flag given was `--dsn`; the refusal names `--pg`. I expected the flag actually
    given, as the other refusals name theirs.
    Grade: NEXT.

11. A freshly migrated `--file` store reports `created=2023-11-14T22:13:20Z` — NEXT

    `nova-config migrate --file demo.json` then `nova-config fleet show --file demo.json`
    ```
    FLEET name=fleet store=- coordinator=- redis_port=- pg_dsn=- bus=- loops_dir=~/nova-bench/loops created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z
    ```
    The store was made seconds earlier; the `--file` store seeds every migrate-made row
    with one fixed time (1700000000 = 2023-11-14T22:13:20Z). On the documented no-database
    first run a reader sees a fleet row a year older than the file. I expected the
    migrate time, or a word that the file store's stamps are synthetic.
    Grade: NEXT.

## What the tool got right

- The `example:` block is real: all five lines ran as printed in a scratch dir.
- The `--file` store makes every kind's add/set/remove/list/show/history usable with no
  database, the same refusals and history as the real store, and `--dry-run` prints the
  plan from the same code path.
- `add` names every missing required flag in one line, each with what it wants and its
  unit; a bad enum, a bad integer, `--pg` with a password and `--pg` with `--file` each
  get one refusal with a remedy that repeats the store given.
- Every kind's refusals name the thing that holds the row (a machine that is the
  fleet's `--store` or a loop's machine, a friend who coordinates the sprint, a route in
  a tier's array, a disabled route with no note) and the command that frees it.
- `apply --check`/`--dry-run` prints the whole per-kind plan and the revisions, and
  `status` reads Redis against the store and exits 1 with the apply command when Redis
  is behind.
- `--json` is the same value as the lines for every verb that takes it.

READ 8/10 — the banner answers what it does, how it works and where its state lives, the
kind list is complete, `add -h` marks its required fields, and the example block runs as
printed; the dead `tier add`/`tier remove` help pages and the `--seat` flag meaning two
things keep it off a 10.

USE 8/10 — the `--file` store lets a stranger run every verb for real with no database,
the refusals are actionable and the apply plan is legible; the escaped notes and the
unreachable-store log noise are the cost.

urgent=2 next=9
