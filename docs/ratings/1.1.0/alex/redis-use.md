# nova-redis USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 56754ede15b
Score: 7.5/10

## Reasons

Used cold, from `nova-redis help`, `nova-redis help <verb>`, `-h` and the
binary alone, every command in a scratch directory, no redis-server started (the
rules forbid one) and no store on the network: dials went to closed loopback
ports and to one port an HTTP service holds, and this tool offers no in-memory
twin (the `--redis mem:<file>` form is another tool's flag). What STEP 2 read
is no evidence here.

First run: `nova-redis version` prints the one version line and exits 0; the
bare command refuses in one line naming the door; `nova-redis help` answers
what, how, first run, exit codes and an example block, and its first-run line
("the --dry-run line needs no store") told the truth in every run I made.

Two small real jobs, end to end as far as the rules allow. Job one, plan the
store and its write with no store: `nova-redis serve --dry-run --bind 127.0.0.1
--port 6390 --dir <scratch>/store` prints `SERVE OK bind=127.0.0.1 port=6390
auth=on persistence=aof eviction=none dir=<scratch>/store dry_run=true created=0
launched=0` and creates nothing (verified: the directory is absent after), then
`nova-redis spill --dry-run --addr 127.0.0.1:6390 --owner trial --name note
--ttl 10m --value hello` prints `SPILL OK key=trial:note ttl=10m0s
expires=2026-10-04T02:40:07Z bytes=5 store=127.0.0.1:6390 written=0 dry_run=true`.
Job two, a different real job the tool exists for, store-free: `nova-redis acl
render` prints the key families, one pasteable `ACL SETUSER` line per user and
`ACL RENDER OK users=4 functions=41 library=11dc308260975247`, exit 0, opening
no store — the one verb a stranger can run for real on day one, though the
banner's how-it-works never names it.

Four refusals provoked, and they are the best writing in the tool. A missing
required flag (`spill --addr 127.0.0.1:6379 --name note --ttl 10m --value hi`)
names `--owner` and what it wants; with everything missing, one run prints all
five problems, one line each. An unknown flag (`--colour red`) lists every flag
the verb has and points at `spill -h`. An unknown verb (`spil`) answers "did
you mean spill?" with the verbs; `acl push` answers inside its group. A bad
value says the fix inline: `--ttl "10x" is not a duration (try 10m)`, `--addr
"localhost" is not <host:port>; refusing to guess`, `--bind "0.0.0.0" binds
every interface; name 127.0.0.1 or a tailnet address`, `--dir "store" is not
absolute`. Login refusals before the dial are just as good: a `--user boss` with
no password variable set refuses with "run under nova-secrets exec --only
NOVA_REDIS_PASSWORD".

`--json` and `--dry-run` wherever the help offers them. `spill --dry-run --json`
returns the same value as one object, `dry_run` among the facts; a refusal is
one aggregated object with every problem in `why` — five problems, one object,
exit 2 — a program can act on that without parsing lines. The line form escapes
its blanks (`err=redis\x20at\x20127.0.0.1:6391...`) and `--json` carries the
same text exactly. The Prints verbs (serve, fn, acl) refuse `--json` by naming
their flags, which teaches the boundary; recall refuses `--dry-run` the same
way.

What costs the score. On the tool's own advertised first-run path, a `--dry-run`
run that meets a refused login or an unreachable store is answered by the
skeleton's guard, not the verb: `spill --dry-run --user boss ...` prints `SPILL
FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may
have written` — the real refusal (the empty password variable) is lost, a Go
identifier is named, and the exit is 1 where the same run without `--dry-run`
exits 2. `acl apply --dry-run` against a closed port prints its own failure line
and then the same guard line, exiting 1 where the plain run exits 2. Second,
`acl apply --dry-run` dials the store (it must, to know what differs) and its
help does not say which reads the dry run still makes, where serve's help says
so in detail. Third, `spill -h` states `effect: local write: writes files on
this machine`, but spill writes a key on the store at `--addr` and no file.

Where I had to guess: whether an address that answers but is not a Redis is
"could not run" (spill says exit 1, fn check says exit 2, same error); what the
`beats` family is when its keys are `bench:*`; and which example-block lines
need a store (the how-it-works line says it, the block does not mark them).
Verbs not tried, judged from their help and dry runs instead, no redis-server
being allowed here: spill and recall for real, fn load, fn check, acl check,
acl apply — each needs a live store; their helps, exit tables and remedies read
consistently with the paths I could run.

A 10 needs: the dry-run guard never replacing a verb's own refusal; the dry-run
reads named in every verb that makes them; an effect line that tells the truth
for a store write; and one exit for one error class across the verbs.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis spill --dry-run --addr 127.0.0.1:6391 --user boss --owner trial --name note --ttl 10m --value hi` | a --dry-run run whose login the check refuses loses the refusal and its remedy: the line prints SPILL FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written, names a Go identifier, and exits 1 where the same run without --dry-run exits 2; acl apply --dry-run against a closed port does the same, appending the guard after its own failure line | read c.DryRun() in spillRun and aclVerbRun before any refusal or dial can return, so the guard never fires on a path the verb answered | M |
| 2 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6391` | the dry run dials the store (an unreachable store fails it at exit 1) and the verb's help does not say which reads the dry run still makes, where serve's help states its dry run reads nothing | a Detail line on acl apply naming the reads the dry run keeps, as serve has | S |
| 3 | `nova-redis spill -h` | the effect line says local write: writes files on this machine, but spill writes a key on the store at --addr and no file; fn load and acl apply carry the same wording | a store-write wording for the verbs that dial, or a fourth effect class | S |
| 4 | `nova-redis spill --addr 127.0.0.1:6379 --name note --ttl 10m --value hi` | the missing-flag refusal points at nova-redis help while the unknown-flag refusal points at spill -h; the tighter door exists and is not used where the reader needs the verb's flag list | make a missing flag's remedy the verb's own -h | S |
| 5 | `nova-redis spill --addr 127.0.0.1:6390 --owner trial --name note --ttl 10m --value hi` | an address that answers but is not a Redis exits 1 from spill (FAILED class=other) and 2 from fn check on the same error, so the exit table's could-not-run line reads differently per verb | one classification of an answered-but-not-a-store dial, shared by every verb | S |
| 6 | `nova-redis acl render` | the beats family holds the bench:* keys, so the family's name and its key prefix disagree and a cold reader must guess they are one thing | name the family for its keys, or say why the names differ | S |

## Good, keep

The dry-run plans that print from the same parse as the real run and create nothing: serve's plan names bind, port, auth, persistence and eviction, and the directory is verifiably absent after.

The refusal grammar: every problem named in one run, each saying what it wants, unknowns answered with the names there are, bad values with the fix inline.

The aggregated JSON refusal: five problems, one object, exit 2 — a program can act on it without parsing lines.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | the refusal lists every missing user in missing= but the run: command names the first only (cmd/nova-redis/acl.go:336); not runnable here, no store being allowed |
| recall hex-escapes the value | CHANGED | the line still escapes its blanks by the one-line grammar (`nova-redis spill --addr 127.0.0.1:6391 ...` prints err=redis\x20at\x20127.0.0.1:6391...) while `nova-redis recall --json --addr 127.0.0.1:6390 --owner trial --name note` carries the same text exactly in the object, blanks and all |
| --addr refuses the socket nova-table's first run makes | STILL THERE | `nova-redis recall --addr ./trial.sock --owner trial --name note` answers RECALL REFUSED: --addr "./trial.sock" is not <host:port>; refusing to guess |
