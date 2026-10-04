# nova-redis USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: bd7949b97aec
Score: 8.5/10

## Reasons

The first run works with no store and no guessing: `nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi` exits 0 and prints `SPILL OK key=ada:note ttl=10m0s expires=... bytes=2 store=127.0.0.1:6379 written=0 dry_run=true`. The two jobs this tool exists for, writing a short-lived named value and reading it back, both have a store-free dry-run: the second job's plan comes from `recall --addr ... --owner ada --name note` and the help for recall says inspection, nothing written. But the rules forbid a store on this machine, so the real spill and the real recall could not be run end to end; both are judged from their dry-run and their help alone, and the `fn`/`acl` verbs that dial the store only from their refusal path. The four provoked refusals each name the problem, every problem at once, and the next command: missing `--value` says `--value is required ... refusing to guess; run: nova-redis help`, an unknown flag lists every flag spill has, an unknown verb lists the nine verbs, and a bad value names the flag and the format it wanted (`--ttl \"abc\" is not a duration (try 10m)`). Exit codes tell the truth: 0 done, 1 ran and said no, 2 could not run. `spill --json` and `recall --json` print one JSON object whose facts are typed and match the line exactly, so a caller acts on them without guessing. `version --json`, `serve -h`, `fn -h` and `acl -h` all answer 0 and quote the exit table.

What costs the score: `nova-redis acl apply --dry-run --addr 127.0.0.1:6379 --password-env-for ...` never honours `--dry-run`; it dials, prints `ACL-APPLY FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, and exits 1, while the flag help says `--dry-run print what the verb would write and write nothing`. The one dry run that a cold reader would reach for to plan ACL setup is the one that promises nothing and then says it may have written. A 10 would need that dry-run honoured, and an example in `acl render`/`acl check`/`acl apply` help so the ACL half of the tool has a paste-ready first command like `spill` does. The verb help carries no error codes table of its own, only the global one, which is verbose to read for a single verb.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis acl apply --dry-run --addr 127.0.0.1:6379` | exits 1 and says the verb may have written, while the flag help says write nothing; the dry-run promise is broken | honour --dry-run before opening the store and print the would-set plan | S |
| 2 | `nova-redis help acl apply` | no example section, so the ACL half gives flags and exit codes but no sample command to copy | add one example line per ACL verb in cmd/nova-redis | S |
| 3 | `nova-redis spill --addr mem:` | the addr is refused as needing a port, but the tool offers no store-free way to run the real spill; the only memory form is the dry-run the help names | say in the help which store addresses spill accepts and that `mem`/`mem:` is not one; point at `--dry-run` for a store-free run | S |
| 4 | `nova-redis help recall` vs `nova-redis help spill` | both show the same global exit table with all verbs' reasons; the verb's own two lines of usage are buried above it | quote only the verb's own exit meanings in the verb help | M |
| 5 | `nova-redis help spill` (refusal for `--addr mem:`) | the refuse line says `run: nova-redis help`, sending the reader to the whole banner for a flag format the verb help already documents | refuse with `run: nova-redis spill -h` when the problem is one flag's value | S |

## Good, keep

The refusal grammar: one problem per line, all problems in one run, each ending `; run:`. The `spill --dry-run` path that checks every flag and prints the write before dialling. The `--json` envelope on spill and recall that carries the value and the TTL exactly as typed.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| acl apply's remedy names one of four missing users | STILL THERE | `nova-redis acl apply --addr 127.0.0.1:6379` says `missing=coordinator,bench,ns-table,ns-friend` then `run: ... --password-env-for coordinator=<VARIABLE>`, naming only the first |
| recall hex-escapes the value | CHANGED | `nova-redis recall --json --addr 127.0.0.1:6379 --owner ada --name note` returns `\"value\":\"hi\"` as typed, while the line form still escapes via the shared encoder |
| --addr refuses the socket nova-table's first run makes | STILL THERE | `nova-redis spill --dry-run --addr /tmp/redis.sock --owner ada --name n --ttl 10m --value v` exits 2 with `--addr \"/tmp/redis.sock\" is not <host:port>` |
| bounded store-free plans and aggregated JSON refusals | FIXED | `nova-redis spill --json --dry-run` prints one object with `dry_run:true`; `nova-redis spill` with nothing given prints five `why` reasons in one JSON refusal |
