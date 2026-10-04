# nova-config USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 7.5/10

## Reasons

`nova-config help` is enough to start. The banner's `example:` block, run as printed in an empty directory, is the first success: `migrate --file try.json` prints `CONFIG MIGRATE file=try.json from=0 to=19 applied=19` and exits 0, then add, set, list, and history of machine `m1` each exit 0. `machine add -h` and `help machine add` print the same text, including `required: --user --seat --slots (and --as)` and one example.

The first real job is that machine row, end to end. After the example, `machine show` prints every field and `loops=-`, and `machine width m1` prints `CONFIG WIDTH machine=m1 width=6 member=true`. `status --file try.json` prints `schema=19` and `machine=1`. Nothing was guessed: each line names the kind, the name, and the revision.

The second job is a different kind of row: how a tier runs. `route add pro-a ... --deadline 900` prints `CONFIG ADD kind=route name=pro-a rev=3`. `route set` of the two prices and a note prints `changed=note,price_input,price_output`. `tier set pro --routes pro-a` prints `rev=5`. `tier show` and `route history` then show the array and the two changes. A loop on `m1`, a friend, and the sprint coordinator also add in one line each. `friend show` reports `width=8`, the default I did not pass. `fleet set` of only `--store` and `--coordinator` is accepted; `fleet show` prints `redis_port=- pg_dsn=-`, so the omitted endpoints stay visible.

Four refusals, each one line, exit 2 unless the store itself says no:

- Missing flag: `machine add` without `--slots` names the flag, what the number is for, and `run: nova-config machine add -h`.
- Unknown flag: `--jsno` names the nearest `--json` and the flags the verb takes.
- Unknown verb: `bake` names kinds, migrate, status, apply, inventory, and the kind verbs, and `run: nova-config help`.
- Bad value: `--slots no` says `want a non-negative integer`.

One bad friend add names four problems in that same line: missing `--as`, a negative `--slots`, a `--tiers` word outside `flash, frontier, pro`, and a name that is not lower case. A duplicate `machine add m1` exits 1 and the remedy repeats `--file try.json`. `route set --enabled false` without a note exits 2 and tells me to pass `--note`. A dry-run add of `m9` prints `CONFIG DRY-RUN ... wrote=nothing`, and the next list is still one row. The same dry-run of `m1` exits 1 with the set remedy, so the dry-run is not a free pass.

`--json` on a successful list and on `route show` is one object whose fields match the lines, and a blank in the note is a real blank there. I can act on that object without guessing. A refusal with `--json` does not do this: stdout is empty, and the same plain line is on stderr, for both the missing `--slots` (exit 2) and the duplicate (exit 1).

`apply --dry-run` with no `--redis` exits 2 and names the flag, the two variables, and a seat, and it does not dial. After the endpoints are set, the same dry-run against a closed port does not print a `CHECK` plan. It writes several `redis:` pool lines, then `apply REFUSED: redis: read machines: dial tcp 127.0.0.1:1: connect: connection refused`, and the remedy is `nova-config apply -h`. The real apply on that port fails the same way. `--json` on that dry-run still leaves stdout empty. `inventory --fixture inventory-fixture.yml` exits 0 and prints host variables I can read. `inventory` with no store exits 2 and names `--redis`.

Where I had to guess: whether `fleet set` would take two fields and leave the ports empty (the show answered), and whether `0.30` would stay `0.30` (`route show` prints `price_input=0.3`; the flag help says "a decimal like 0.30" and does not say the trailing zero is dropped). The note's blanks were the third: the line prints `prices\x20from\x20the\x20provider\x20page` even though `-h` says show prints the note whole.

The score is 7.5. The file trial, the two jobs, and the refusal line are what an AI wants, and they ran. A 10 would put every refusal in the same JSON object as the success, print a note as text on the line, and make `apply --dry-run` either print the plan with no dial or refuse in one line that names the address, with no pool log in front of it.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m1 --user nova --seat s1 --slots 8 --as a1 --json --file try.json` | Exit 1, stdout empty, stderr the plain line `nova-config machine add REFUSED: machine m1 exists; run: nova-config machine set m1 --<field> <value> --file try.json`. The same empty stdout happens for a missing `--slots`. An AI that reads only the JSON object has no remedy. | Emit the refusal as the one JSON object, with the remedy in it, and keep the line as the other rendering. | M |
| 2 | `nova-config route show pro-a --file try.json` | The note is `note=prices\x20from\x20the\x20provider\x20page`. `-h` says show prints the note whole. The JSON object has the blanks as blanks. The line cannot be copied back as the text. | Print the note as text on the line, or say in the flag help that a blank is escaped. | S |
| 3 | `nova-config apply --dry-run --json --file try.json --redis 127.0.0.1:1` | No plan. Stdout empty. Stderr is four `redis:` pool lines about a failed dial, then a REFUSED whose remedy is `nova-config apply -h`, not the address. A closed port and a real apply fail the same way, so this dry-run does not preview a refusal the write would add. | One refusal line, no pool log, the remedy naming the address. Say in the effect line that a plan is only printed when Redis answers. | M |

## Good, keep

The banner example runs as printed: migrate, add, set, list, history, each a typed line and exit 0, and the file is the only store.

One invocation names every problem. `nova-config friend add F1 --slots -1 --tiers nope --file try.json` exits 2 and lists the missing `--as`, the negative slots, the bad tier word, and the bad name, then `run: nova-config friend add -h`.

`nova-config machine add m9 --user nova --seat s9 --slots 2 --width 1 --as a1 --dry-run --file try.json` prints `wrote=nothing`, and the following list is still the one machine. A duplicate dry-run refuses instead of pretending the add is new.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty | STILL THERE | `nova-config machine add m1 --user nova --seat s1 --slots 8 --as a1 --json --file try.json` exits 1 with an empty stdout and the REFUSED line on stderr |
| apply --dry-run passes where apply refuses | CHANGED | `nova-config apply --dry-run --file try.json --redis 127.0.0.1:1` exits 2 with a dial refusal and no CHECK line; `nova-config apply --file try.json --redis 127.0.0.1:1 --as a1` exits 2 with the same dial refusal. A ceiling check was not reached |
| --json refusals escape as plain stderr | STILL THERE | the duplicate add above prints `nova-config machine add REFUSED: machine m1 exists; run: nova-config machine set m1 --<field> <value> --file try.json` on stderr and no JSON |
