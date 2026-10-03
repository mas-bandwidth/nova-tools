# nova-table USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 6.5/10

## Reasons

The first run could not be completed without a Redis instance: `nova-table create demo --columns ready,done --redis mem:table.twin` refuses with `redis at "mem:table.twin" given to this tool: unreachable: not an address`, and the help offers no mem or in-memory form, so the no-Redis first run this card promised is absent. The refusals that could be provoked all work: a missing --redis says `--redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-table help`; an unknown flag lists every create flag; an unknown verb lists the nineteen verbs; and a flag missing its argument exits 2.

What costs the score: with no store and no mem path, the table verbs themselves could not be exercised at all, so the rating rests on help and refusal behaviour. The reads offer no --json (`nova-table list --json` is an unknown flag), and `create --columns` with a missing value prints the raw Go flag error `flag needs an argument: -columns` rather than the tool's own refusal grammar.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table create demo --columns ready,done --redis mem:table.twin` | no mem or in-memory path exists, so a cold user cannot try the tool without a Redis instance | accept mem:<file> like nova-sprint, or state plainly that a real Redis is required | M |
| 2 | `nova-table list --json` | reads have no --json, so programs must parse the text table | add --json to list, show, render and cell members | S |
| 3 | `nova-table create demo --columns` | a flag missing its argument prints the raw Go flag error, not the tool's refusal grammar | catch the flag package's missing-argument error and rewrite it as a refusal | S |

## Good, keep

The refusal that names the env-var fallback order for --redis. The unknown-flag refusal that lists every flag of the verb. The help's column-spec quoting note.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| reads have no --json | STILL THERE | `nova-table list --json` exits 2 with unknown flag |
| the unknown-option refusal is generic | FIXED | `nova-table create demo --columns ready --bogus` lists every create flag |
