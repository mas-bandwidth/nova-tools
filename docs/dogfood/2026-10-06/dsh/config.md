# Dogfood: nova-config — 2026-10-06, dsh

One friend, one tool, cold. Before and during the run I read only the tool's
own surfaces — `nova-config -h`, `nova-config help`, `nova-config help <verb>`,
every verb's `-h`, and its page under docs/ (`docs/nova-config/README.md`) —
then used every verb at least once with its real flags, refusals included. The
binary was built from this checkout at 0510687682 (`nova-config v1.0.1-0.20261007144949-051068768266 linux/amd64 go1.26.6`) and run on a Linux
bench; every store was a scratch `--file` JSON file under the job's temp
directory, so no PostgreSQL and no Redis were touched. About 25 minutes end to
end. Every finding is recorded, not fixed.

## Findings

1. `nova-config machine add mm2 --user '' --seat s --slots 1 --as a1 --file try.json`

        CONFIG ADD kind=machine name=mm2 rev=34
        NOTE machine=mm2 width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: nova-config machine set mm2 --width <n> (0: no member) --as a1 --file try.json

   and `nova-config machine show mm2 --file try.json` answers `user=-`. The
   same hole takes `--user '  '` (blanks), `--seat ''`, and
   `nova-config machine set m2 --user ''` (which cleared the field on an
   existing row to `user=-`), while `--slots ''` and `--as ''` are both
   refused. Expected: the text fields the help marks `required;` are refused
   when empty or blank, the way `--slots ""` is refused with
   `--slots: want a non-negative integer`; a row whose login is empty cannot be
   read by the plays (`ansible_user` is left out). Grade: URGENT.

2. `nova-config apply --check --file try.json --redis 127.0.0.1:6399`

        nova-config apply REFUSED: --as is required: the name the write is recorded under (or NOVA_FRIEND); run: nova-config apply -h

   The same refusal comes from plain `apply` and from `apply --dry-run`, and
   `nova-config apply --check --file try.json` (no `--redis`) names `--as`
   first. Expected: `--check` and `--dry-run` write nothing, so they plan
   without an actor; the tool's own page runs `nova-config apply --check` in
   its "Apply: Redis as a copy" transcript with no `--as`, and `apply -h` does
   not mark `--as` required, so the documented first command exits 2 for a
   reader with no recorded login. Grade: URGENT.

3. `nova-config machine set m1 --seat s9 --as a1 --file try.json`

        nova-config machine set REFUSED: --seat and --file are exclusive: --file keeps the rows in a local file in PostgreSQL's place; set names no field; the fields are user, seat, slots, runners, width, tla, note; run: nova-config machine set -h

   The machine's `seat` is a field (`kinds` prints `fields=user,seat,...` and
   `machine add -h` asks for it as `required;`), but on `set` the name `--seat`
   is taken by the connection-profile flag, and no other flag names the field,
   so a machine's seat cannot be changed after `add`. The refusal contradicts
   itself: it says `set names no field` and lists `seat` among the fields in
   the same line. Expected: `--seat` on `set` sets the machine's seat (or the
   connection profile takes a flag of its own), and the refusal names the flag
   that does. Grade: URGENT.

4. `nova-config route add heavy-x --tier heavy --provider deepseek --model deepseek-v4 --deadline 600 --as a1 --file try.json`

        CONFIG ADD kind=route name=heavy-x rev=15

   and `nova-config kinds` prints the tier kind as `one row each for flash and pro and heavy, created by migrate`, and `tier list` answers `heavy`. But the
   tool's page says `--tiers` is `a comma list of flash, frontier, pro` and
   `The tier is flash or pro: frontier cards are never dealt from routes`.
   Expected: the page names `heavy` beside `flash, frontier, pro`, as the help
   and `kinds` do. Grade: NEXT.

5. `nova-config tier add mytier --routes pro-deepseek-direct --as a1 --file try.json`

        nova-config tier add REFUSED: tier mytier: want one of flash, pro, heavy; run: nova-config tier add -h

   and `tier add pro` answers `tier pro exists`. Every name `add` accepts is
   already made by `migrate`, so no `tier add` can ever write, yet
   `nova-config tier add -h` claims `effect: store write: one row and its history row ...` and offers `--routes`. `nova-config tier remove -h` says
   the truth in a line of its own. Expected: `tier add` is not a verb (the
   top-level help says fleet and sprint have no add; tier should say so too),
   or its help says the rows are made by migrate and names `set`. Grade: NEXT.

6. `nova-config inventory --fixture fleet/testdata/inventory-fixture.yml --json`

        {"result":{"verb":"inventory","status":"refused","exit":2,"remedy":"nova-config inventory -h","why":["unknown flag --json; this verb takes --fixture, --host, --list, --redis, --timeout"]},"facts":{}}

   Every other verb takes `--json` and answers one value in the tool's shape;
   `inventory` answers the Ansible JSON already but refuses the flag. Expected:
   `inventory` accepts `--json` (the same document, or the tool's result
   wrapping it), as the one-shape rule asks. Grade: NEXT.

7. `HOME=<home> nova-config login --check --json` (no login recorded)

        {"result":{"verb":"","status":"refused","exit":2},"facts":{},"notes":["LOGIN REFUSED: unknown flag --json; the flags of login are --as, --check, --dsn, --friend, --key, --secret, --sops, --store; did you mean --dsn?; run: nova-config login -h"]}

   `logout --json` answers the same shape. Compared with `friend add ... --json`
   (`"verb":"friend add"`, `"remedy":...`, `"why":[...]`), the login refusal has
   an empty `verb`, no `remedy`, and its reason in `notes` as a hand-printed
   `LOGIN REFUSED:` line, so a reader of the JSON cannot find the remedy the way
   every other verb gives it. Expected: the one refusal shape, as the rest of
   the tool uses. Grade: NEXT.

8. `nova-config apply --as a1 --file try.json --redis 127.0.0.1:6399` (nothing listening)

        redis: 2026/10/07 14:59:49 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
        redis: 2026/10/07 14:59:50 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
        redis: 2026/10/07 14:59:51 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
        nova-config apply REFUSED: redis: read machines: dial tcp 127.0.0.1:6399: connect: connection refused; run: nova-config apply -h

   and `nova-config inventory --redis 127.0.0.1:6399` prints four of the same
   raw pool lines before its refusal. Expected: one refusal line in the tool's
   grammar (`apply REFUSED: ...; run: ...`), no unsolicited library logs on the
   way, as `inventory --redis 10.255.255.1:6379 --timeout 2s` shows when its
   timeout path answers. Grade: NEXT.

9. `nova-config machine remove m1 --as a1 --file try.json` (m1 is the fleet coordinator and is named by loop reader-m1)

        nova-config machine remove REFUSED: machine m1 is the --coordinator of the fleet; run: nova-config machine list --file try.json

   After `fleet set --coordinator m2`, the same command answers `machine m1 is the --machine of loop reader-m1`. Two blockers, one per turn. Expected: one
   refusal naming every reason m1 cannot go (coordinator and loop), as the
   onboarding standard asks, so the reader clears them in one turn. Grade: NEXT.

10. `nova-config inventory --fixture fleet/testdata/inventory-fixture.yml --host nope`

        nova-config inventory REFUSED: --host "nope" names no machine row; known machines: bench-a, bench-b; run: nova-config machine list

    The remedy drops the `--fixture` the run was given, so it cannot be pasted
    (and `machine list` opens a store the fixture run never had); store refusals
    repeat the `--file` or `--pg`, as `machine show m9 --file try.json` shows.
    Expected: the remedy carries the `--fixture`. Grade: NEXT.

11. `nova-config machine set m1 --note 'held 1:46 PM: reads kernel-bound' --as a1 --file try.json` then `nova-config machine show m1 --file try.json`

        CONFIG SET kind=machine name=m1 rev=21 changed=note
        MACHINE name=m1 user=nova seat=s1 slots=64 runners=1 width=16 tla=true note=held\x201:46\x20PM:\x20reads\x20kernel-bound created=... updated=... loops=-

    `machine history m1` shows the same `\x20` escapes, while `machine show m1 --json` prints `"note":"held 1:46 PM: reads kernel-bound"`. Expected: the
    line the tool says "prints the note whole" carries the text as typed, or the
    page names the escaping; a reader of the line sees escapes where the JSON
    has prose. Grade: NEXT.

12. `nova-config machine add mm3 --user u --seat s --slots 1 --op op1 --as a1 --file try.json`

        nova-config machine add REFUSED: unknown flag --op; this verb takes --as, --dry-run, --file, --json, --note, --pg, --runners, --seat, --slots, --tla, --user, --width; run: nova-config machine add -h

    and `machine list --max 1` answers `unknown flag --max`. Expected, from the
    tool's own standard: a write verb takes `--op <id>` so a retry after a
    timeout is safe, and a listing is bounded with `--max` and a `MORE` line.
    Grade: NEXT.

## What worked, for the record

The rest of the run was clean: the bare command and unknown verbs name the
door in one line; `help`, `-h`, `--help` and `help <verb>` are the same and
exit 0; the refusals name every independent problem at once and what each flag
wants (`machine add` with nothing names the name and all four flags;
`--as ""` and `--slots ""` and `--slots -1` and a bad name are each refused with
the exact want); the printed first-run example runs as printed; `migrate` is
idempotent (`applied=35` then `applied=0`), `--print` and `--dry-run` print the
migration ledger, and `--dry-run` wrote nothing; every kind's `add`, `set`,
`list`, `show`, `history` and `remove` did its real work on the scratch file
(including `--note ''` clearing a note, `--roles ""` clearing a list, a disabled
route refusing without a `--note` and accepting with one, the `--width` argv
refusal on a member loop, and `--keys` without `--seat`); `machine width`,
`machine self --check` and `--json`, `fleet`/`sprint`/`tier` singleton behavior,
`kinds`, `version`, `login --check`, `logout`, `status`, `apply` (its plan lines
and `--kind`) and `inventory --fixture`/`--host`/`--list`/`--timeout` all held;
`--json` matched the lines everywhere it is offered, and `--dry-run` wrote
nothing.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

Ran on the Linux bench against the pushed tip before this file was committed
and again with it in place:

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.775s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	11.588s

The card's own named test does not exist at this tip, so its line answers no
tests and the gate ran as the packages the card's PATHS name:

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.009s [no tests to run]

READ 7/10 — the banner, `help`, every verb's `-h` and `kinds` answered almost
everything before my first command, and every refusal says what its flag wants
with a paste-able remedy; the three URGENTs are what the page and the help
misstate (the required text fields it lets empty, `apply --check` without
`--as`, and the seat flag that names two things), so a cold reader is misled
three times.

USE 7/10 — every verb did its real work first try on the scratch file, migrate
was idempotent, every cross-row refusal named its blocker, and the dry runs
wrote nothing; the cost was the machine seat that cannot be corrected, the
check that demands an actor it does not use, the raw pool lines on a dead
store, and the JSON shapes of `login`/`logout`.

urgent=3 next=9
