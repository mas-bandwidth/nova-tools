# nova-config USE rating, nova-tools 1.1.0

Rater: grok-4.7
Build: 4983443c24f4d9a092f8ea3a697adbca44fe62ca
Score: 7.5/10

## Reasons
The `--file` store does the job the help promises, with no database. `nova-config migrate --file try.json` exits 0 and prints `CONFIG MIGRATE file=try.json from=0 to=26 applied=26`. One job then adds two machines, a friend row, fleet endpoints, a route, a tier, a sprint row and a loop; list, show, history and status match the writes, and a later set moves width from 4 to 6. A second file adds two routes (one disabled, with a note), sets a tier to a repeated route name, dry-run-removes a machine (show still finds it) and then removes it (history keeps both ops). Missing required flags are named together, each with what it wants, and a next command. `--dry-run` on a row write prints `CONFIG DRY-RUN` and writes nothing. Success `--json` is one object with `result.verb`, `status`, `exit`, `facts` and `items`.

A 10 needs the error path to be as usable as the success path. A refusal under `--json` must be that same object on stdout; today stdout is empty and the REFUSED line is plain stderr. One refusal must name every independent problem, including an unknown flag beside a bad value. An inventory fixture refusal must name the missing fixture fields and a command that edits the fixture, not a store write. A failed dial must be one REFUSED line, not library logs in front of it.

Not tried: a live database, and apply or inventory against a live store. apply was judged from its help and from `--dry-run` (it still dials, and a dead address refuses). inventory was judged from `--fixture`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m9 --file try.json --json` | Exit 2, stdout empty, stderr a plain `nova-config machine add REFUSED: --as is required...; run: nova-config machine add -h`. The same empty stdout on a bad value, an unknown verb, and a store refusal at exit 1. Help says `--json` prints one JSON object instead of the lines, so a caller that reads only stdout never sees the problem or the next command. | Print the result object on stdout for a refusal too, status refused, remedy the run line, stderr quiet. | M |
| 2 | `nova-config machine add m9 --slots no --bogus 1 --file try.json` | Exit 2 names only `unknown flag --bogus` and the flag list. Alone, `--slots no` says `--slots "no": want a non-negative integer`. A signed decimal stops at the first flag (`--decide_review "-1"` named, `--decide_bounce 2` not). Two out-of-range probabilities are both named. Missing required flags are all named. | Collect unknown flags and bad values in one refusal, as missing required flags already are. | S |
| 3 | `nova-config inventory --fixture inv.yml --list` | A fixture with machines, fleet store and coordinator, and loops still exits 1: `fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set ... then nova-config apply --kind fleet`. That next command does not edit a fixture. Adding those two fields then prints an inventory and exits 0. A first wrong shape exits 2 and dumps a Go type before the wanted mapping, which omits those two fields. | Name redis_port and pg_dsn in the fixture shape, and point the next command at the fixture file. | S |
| 4 | `nova-config apply --file try.json --redis 127.0.0.1:1 --dry-run --as a1` | Exit 2. stderr is four `redis: ... pool.go:762` dial lines, then `nova-config apply REFUSED: redis: read machines: dial tcp 127.0.0.1:1: connect: connection refused; run: nova-config apply -h`. Help says dry-run still reads the store. The library lines are not the refusal grammar. `--kind fleet` with unset endpoints refuses before any dial, same text with and without `--dry-run`. | One REFUSED line, no pool log, when the dial fails. | S |

## Good, keep
The example block runs as printed on a `--file` store: migrate, machine add, set, list, history, no database.
A missing required set is one refusal that names every missing flag, what it wants, and the verb's `-h`.
`--dry-run` on a row write prints CONFIG DRY-RUN and writes nothing. Success `--json` is one result object a caller can act on. `machine width -h` documents its own JSON keys, and the object matches.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty | STILL THERE | `nova-config machine add m9 --file try.json --json` exits 2, stdout empty, stderr starts `nova-config machine add REFUSED:` |
| apply --dry-run passes where apply refuses | FIXED | `nova-config apply --file job2.json --redis 127.0.0.1:1 --kind fleet --dry-run --as writer` and the same command without `--dry-run` both exit 1 with `fleet: endpoints are unset: redis_port, pg_dsn` |
| --json refusals escape as plain stderr | STILL THERE | `nova-config machine add m9 --user nova --seat s1 --slots no --as a1 --file try.json --json` exits 2, stdout empty, stderr `nova-config machine add REFUSED: --slots "no": want a non-negative integer; run: nova-config machine add -h` |
