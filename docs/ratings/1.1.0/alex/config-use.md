# nova-config USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8.5/10

## Reasons

Cold, from `nova-config help`, `nova-config help <verb>` and `-h` only, every command in a scratch directory on the `--file` store the help offers; nothing real touched (no PostgreSQL, no live Redis, no gh, no model, no key).

First run: the banner's example block ran as printed, five lines, exit 0 each, writing only ./try.json. Two real jobs end to end: a small fleet (machine, fleet endpoints, friend, sprint coordinator, route, tier array, then `status` reading every row and revision back), and a different one (a supervised loop with a seat and two secret names, a route disabled with its measured reason, `machine width`, `machine self` printing this host's own first label). Four refusals provoked: a missing required flag (five problems in one line: `--as`, the store, `--seat`, `--slots`, `--user`), an unknown flag (naming the nearest flag and every flag the verb takes), an unknown verb (both the tool's and a kind's, each naming the verbs there are), and a bad value (two bad values in one line) — each names the problem, every problem at once, and a next command that pastes because it repeats the `--file` the run was given. `--dry-run` on add, set and remove prints the change the write would record from the same checks, carries the width NOTE that teaches before the write, and a dry run that would refuse refuses; `migrate --dry-run` prints the ledger ending ready=yes. `--json` on kinds, list, show, history, width, self, status, version and the dry runs is the family's one object shape, notes included.

Where I had to guess: which stream a refusal lands on under `--json` — a refused kind verb prints nothing on stdout and a plain line on stderr, while a refused `status` prints the failed object on stdout, and no help or doc says the two differ; the JSON spelling of an unset width (`width:0` beside `default:true`, where the line says width=default); what a dead store prints (four library log lines, then the refusal, whose remedy is the verb's `-h`, which does not fix a dead store). Verbs not tried, each needing a live store or a real database: `apply`'s write (and `apply --dry-run` beyond its endpoint refusal, which fired before the dial, as documented), `inventory` against a store (its `--fixture` form ran, printing the Ansible JSON with no store), the live beat facts of `machine list` and `machine show` under `--redis`, `status --redis`, and `migrate` against PostgreSQL. A 10 would need every refusal under `--json` on stdout as the one result object, the dead-store refusal as one line with a remedy naming the address, and the width default printed in JSON as the line prints it.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine remove nope --json --as a1 --file try.json` | the refused kind verbs print nothing on stdout and their refusal only as a plain stderr line, while a refused status prints the failed JSON object on stdout, so a script driving --json must guess which stream carries a refusal, and the docs do not say | render every refusal under --json as the same result object on stdout (status shows the shape) and keep the line on stderr | M |
| 2 | `nova-config apply --file try.json --redis 127.0.0.1:6399 --as a1` | a store that does not answer exits 2 with four library pool log lines before the one REFUSED line, and the remedy is the verb's -h, which does not name the address a dead store leaves unchecked | one line (route the library's logger into the refusal) and a remedy naming the address to check | S |
| 3 | `nova-config machine width bench-4 --json --file try.json` | an unset width prints width:0 beside default:true and member:true, so a JSON consumer reading width alone reads no member, where the line rendering says width=default | print width as default (or leave the number out) when default is true, as the line does | S |

## Good, keep

The `--file` store: every verb but apply's write ran with no database, the banner's example block as printed, and the refusals, history and revisions behaved as the real store's.

One grammar across seven kinds: machine add learned once, then friend, route, loop, tier and the fleet and sprint singletons all behaved as learned, and every singleton or seed-row refusal named its rule with its remedy (tier remove, fleet add, a loop both periodic and kept alive).

Refusals that collect every problem in one run and a next command that pastes, the remedies repeating the `--file` the run was given, and the width NOTE teaching on the dry run before the write.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty | CHANGED | `nova-config machine remove nope --json --as a1 --file try.json` → exit 1, stdout empty, a plain stderr line; `nova-config status --json --file empty.json` → the failed object on stdout, exit 1: status now emits it, the kind verbs still do not |
| apply --dry-run passes where apply refuses | CHANGED | the endpoint refusal fires in the dry run too: `nova-config apply --dry-run --file bare.json --redis 127.0.0.1:6399` → REFUSED fleet: endpoints are unset, exit 1; the write-time refusals (a ceiling, a role) stay write-time: the dry run returns its plan before any write (internal/config/apply.go:174-179) |
| --json refusals escape as plain stderr | STILL THERE | `nova-config machine remove nope --json --as a1 --file try.json` → the refusal a plain stderr line, stdout empty |
