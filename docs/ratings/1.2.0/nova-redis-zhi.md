# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: Zhi, model deepseek/deepseek-v4, harness dsh (the DeepSeek Harness headless runner)
Build: 169a7eef0e30
READ: 8/10
USE: 8/10

## Reasons

Read cold, then used for real. The build is commit 169a7eef0e30, compiled on a
Linux bench; the binary answers `nova-redis v1.0.1-0.20261007204312-169a7eef0e30 linux/amd64 go1.26.6`. I read `nova-redis help`, the `-h` of every group and
every verb, `docs/SPEC-REDIS.md` and the nova-redis section of `docs/CLI.md`,
then ran it against a throwaway `redis-server` on a Unix socket in a private
temp directory: spill and spill --dry-run in lines and JSON, recall in lines
and JSON, recall of a missing key, fn check, fn load, fn check again, fn load
again, acl render, acl check, acl apply --dry-run, serve --dry-run, install
store --dry-run and uninstall store --dry-run. Every dialling refusal class was
provoked against a dead loopback port. No live store was touched; the store
listening on port 6379 on the bench was never dialled, which is itself evidence
for finding 5.

READ 8. The banner's first line says what the tool is, the `how it works:`
paragraph names the nouns and where the state lives, and every verb's `-h`
prints its usage, its flags with what each wants, its effect and the exit
table. The spec explains bind, auth, persistence, the ACL file and the
function library, and `docs/CLI.md` carries the ACL and unit detail. What
keeps it from 10: the normative spec's verb block omits the four service verbs
the binary ships (finding 6), its dialling-verb sentence omits both ACL verbs
(finding 8), and it and the CLI reference teach only `--addr <host:port>`,
never the current `--redis` (finding 9) or the Unix-socket form the banner's
own first run uses (finding 10); the CLI reference describes `acl check`'s and
the JSON envelope's lines differently from what they print (findings 16 and
18); it has no `### First run` (finding 20) and still names a ticket (finding
21); `install store -h`'s example is angle-bracket placeholders (finding 17);
and every verb repeats the whole tool's exit table, which lists neither the ACL
nor the unit outcomes (finding 19). The `--max` sentence is printed for a tool
with no listing verb (finding 4).

USE 8. Real work succeeded store-free and against a real throwaway store with
no wrong guess: spill --dry-run prints the plan and `written=0` without
dialling; the real spill and its recall round-trip; `acl apply --dry-run` is a
real dry run (four `ACL WOULD-SET` lines, exit 0, no dial) where the 1.1.0
rating found it dialling; `fn load` then `fn check` reports `LOADED` then `OK`;
`acl render` opens no store; and exit codes held (0 on every plan, 1 on a miss
and on fn check MISSING, 2 on every refusal and every dead store). What keeps
it from 10: `install store --dry-run` with a missing login flag fails as
`INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1, a broken promise on a write
verb's own dry run (finding 1); the effect line calls spill, fn load and acl
apply `local write: writes files on this machine` though each writes the store
at --redis (finding 2); the missing-address refusal names the deprecated
`--addr` (finding 3); fn load and recall have no dry run (findings 11 and 12);
seven verbs refuse `--json` (finding 13); the fn remedy is not pasteable
(finding 14) and the fn/acl lines lead with a status that is not the verb
(finding 15); an unreachable store gets a login remedy from acl check (finding
7); and the top-level example would spill into a live store on the default
Redis port if pasted (finding 5).

## Findings

| # | tool | where | finding | fix |
|---|---|---|---|---|
| 1 | nova-redis | cmd/nova-redis/install.go:203 | `install store --dry-run` (and `install bus`) with any login flag missing prints `INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` and exits 1, because `installRun` calls `c.DryRun()` only after the refusals at lines 148-200, while the verb declares `DryRun: true` (install.go:57); the natural first run claims it may have written when it wrote nothing | read `c.DryRun()` at the top of `installRun` (and in `uninstallRun`) so every earlier refusal is a clean `REFUSED` at exit 2 and no may-have-written line is ever printed |
| 2 | nova-redis | cmd/nova-redis/main.go:157; cmd/nova-redis/fn.go:63; cmd/nova-redis/acl.go:211 | `spill -h`, `fn load -h` and `acl apply -h` say `effect: local write: writes files on this machine`, but each writes the store at `--redis`, which may be another machine, and none writes a local file; `install`/`uninstall` (install.go:56,82) call the unit loader as well as writing the unit | add a store-write effect (`writes the store at --redis`) for spill, fn load and acl apply, and a unit effect that names the loader call for install and uninstall |
| 3 | nova-redis | cmd/nova-redis/main.go:325 | a missing address is refused as `SPILL REFUSED: --addr is required: the store's address as <host:port> ...`, naming the deprecated `--addr` and not `--redis`, the flag the usage, the flag list and the family's one shape use; the retry then prints `SPILL NOTE --addr is --redis`, teaching the old spelling | name `--redis` in the refusal and keep `--addr` as a silent accepted alias |
| 4 | nova-redis | pkg/tool/tool.go:522 | the top help says `A verb that lists takes --max <n> (default 20, 0 lists all)`, but no nova-redis verb lists; `recall --max 5` is refused as an unknown flag after the help sent the reader after a flag that exists nowhere here | print the `--max` sentence only for a tool with a listing verb |
| 5 | nova-redis | cmd/nova-redis/main.go:156 | the banner's `example:` block's third line (`spill --addr 127.0.0.1:6379 --owner ada ...`) and fourth line dial Redis's default port and write/read a real store, so a cold reader who pastes the first run spills into whatever store runs there (the bench has one on 6379); the block is not the store-free first run the onboarding standard requires | make the real write the throwaway Unix-socket recipe the UsageNote already carries, and keep the `--dry-run` line store-free |
| 6 | nova-redis | docs/SPEC-REDIS.md:18-28 | the normative verb block omits `install store`, `install bus`, `uninstall store` and `uninstall bus`, although the binary ships them (main.go:147, install.go:36-54) and docs/CLI.md:2608-2609 documents them | list the four service verbs with their effects and dry-run behaviour |
| 7 | nova-redis | internal/docs/redis_test.go:94 | `TestSpecRedisVerbBlockNamesEveryVerb` passes while the spec block omits the service verbs because `verbNameRe` matches only a literal `Name: "..."` and the install verbs are built as `"install " + kind` (install.go:39,50); the guard cannot see a dynamically named verb | collect the declared names from the built `Tool` (or match the concatenation), then add the four verbs to the block |
| 8 | nova-redis | docs/SPEC-REDIS.md:50 | the dialling-verb sentence names spill, recall, fn load and fn check only; `acl check` and `acl apply` dial a store too, as `acl check --redis 127.0.0.1:1` shows | name all six dialling verbs and state their common address and login contract |
| 9 | nova-redis | docs/SPEC-REDIS.md:19-25; docs/CLI.md:2617,2624 | the spec block and the CLI reference's `<login>` line use `--addr <host:port>` only and never the current `--redis`, so a cold reader following the documents learns the deprecated spelling and prints a NOTE on every run | document `--redis` first and `--addr` as the one-release alias |
| 10 | nova-redis | docs/SPEC-REDIS.md:19-25; docs/CLI.md:2624 | both documents call the store address `<host:port>`, while the binary accepts the absolute path of a Unix socket (main.go:317,461-497) and the banner's own first-run recipe uses `--redis "$d/redis.sock"` | say `<host:port> or the absolute path of a Unix socket` in the flag, the spec and the CLI reference |
| 11 | nova-redis | cmd/nova-redis/fn.go:58-71 | `fn load` writes the store's function library and takes no `--dry-run` (`fn load --dry-run` is refused as an unknown flag), so the deploy cannot be planned store-free, unlike spill, serve and acl apply | add `--dry-run` printing this binary's digest, the store's state and the login it would use, dialling nothing |
| 12 | nova-redis | cmd/nova-redis/main.go:188-206 | `recall` has no `--dry-run`, so the read path cannot be planned store-free; with no store the only answer is a classified refusal | add `recall --dry-run` printing the key, the store and the login it would use |
| 13 | nova-redis | cmd/nova-redis/fn.go:65,80; cmd/nova-redis/acl.go:184,198,214; cmd/nova-redis/install.go:65,88 | `fn load`, `fn check`, `acl render`, `acl check`, `acl apply`, `install store|bus` and `uninstall store|bus` take no `--json` (`fn check --json --redis ...` is refused with the flag list, `install store --json` likewise), while the skeleton's contract is one value with two renderings; the inspection verbs an AI most wants to parse are prose only | give these verbs `--json` through the shared result value |
| 14 | nova-redis | cmd/nova-redis/fn.go:169-174 | `fn check` STALE/MISSING prints `remedy="nova-redis fn load --redis <addr> puts this binary's library on the store"`: prose after the command inside the one quoted value, so the advertised pasteable remedy cannot be pasted, and docs/CLI.md:2646 promises it can | put the command alone in the `remedy` value and move the prose to a note before it |
| 15 | nova-redis | cmd/nova-redis/fn.go:167,174; cmd/nova-redis/acl.go:341,345 | the success and state lines lead with a status that is not the verb: `OK nova_sprint ...`, `MISSING nova_sprint ...`, `LOADED nova_sprint ...`, `ACL MISSING user=...`, `ACL CHECK DRIFT ...`, against the standard's `<VERB> OK|REFUSED|FAILED` grammar, so a parser cannot key the line off its first word | lead every line with the verb and one status word in one spelling |
| 16 | nova-redis | docs/CLI.md:2650 | the reference says `acl check` prints `ACL OK`, `ACL MISSING` or `ACL DRIFT user=<u> role=<r>` with `keys+=` and `commands+=`; on a store holding none of the users it printed per-user `ACL MISSING user=... role=...` lines and `ACL CHECK DRIFT users=4 differ=4`, with no add-or-remove detail | make the reference match the lines the verb prints, or print the fields it promises |
| 17 | nova-redis | cmd/nova-redis/install.go:69 | `install store -h`'s example is `nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>`, with literal angle-bracket placeholders, so it is not a command a cold reader can run | give a runnable dry-run example with concrete values (and the same for `install bus`) |
| 18 | nova-redis | docs/CLI.md:2628 | the reference says the JSON envelope always carries `result.remedy`, `items`, `notes` and `payload`; `spill --dry-run --json` prints only `{"result":{"verb","status","exit"},"facts":{...}}`, and `recall --json`'s refusal puts the concrete next step in `why` while `result.remedy` stays `nova-redis help` | document the envelope as the renderer emits it, and set `result.remedy` to the classified next step |
| 19 | nova-redis | cmd/nova-redis/main.go:136 | every verb's `-h` quotes the whole tool's exit table (spill written, recall found, fn load done, fn check, serve stopped) and names no acl apply/check, install or uninstall outcome; `version -h` lists recall's misses | give each verb its own exit table through `Verb.ExitTable` |
| 20 | nova-redis | docs/CLI.md:2604-2672 | the nova-redis section opens with the command block and has no `### First run`, though the standard's onboarding point 3 requires one and the neighbouring sections carry one | add the first-run transcript (version, spill --dry-run, acl render) produced by running the tool, with what a first run gets wrong |
| 21 | nova-redis | docs/CLI.md:2671 | the section still names the ticket `nova-tools #3620`, which a cold reader cannot resolve | state the rule in the present tense and drop the number |
| 22 | nova-redis | pkg/nsprint/verbflag/verbflag.go:111 | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a remedy identical to the input and saying nothing about the verb being two arguments | answer that `fn load` is two words and give the runnable form, or match the joined spelling |

## Good, keep

- `spill --dry-run` is a bounded store-free plan (key, ttl, expiry, bytes,
  store, `written=0`) and the same value as one JSON object.
- `acl apply --dry-run` is now a real dry run: four `ACL WOULD-SET` lines and
  `ACL APPLY OK dry-run=true ... would=4` at exit 0 with nothing listening.
- `serve --dry-run` refuses a public bind and a relative `--dir` in one run and
  reports its bind, persistence, auth and ACL plan with `created=0 launched=0`.
- Refusals name every independent problem in one run and carry a next step, on
  lines and `--json` alike, and exit codes tell the truth: 0 on every plan, 1
  on a miss and on `fn check` MISSING, 2 on every refusal and dead store.
- `install store --dry-run` and `uninstall store --dry-run` print the whole
  unit (or its path and presence) and change nothing.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| `acl apply --dry-run` dialled the store and reported it may have written (USE 1.1.0) | FIXED | `acl apply --dry-run --redis 127.0.0.1:1` printed four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4`, exit 0, with nothing listening |
| `--addr` refused the Unix-socket form the throwaway recipe makes (USE 1.1.0) | FIXED, documented | `spill --redis /tmp/.../redis.sock ...` round-tripped through a throwaway store; the flag help now says `or the absolute path of a Unix socket`, though the spec and CLI reference still say `<host:port>` (finding 10) |
| the spec verb block omitted the ACL verbs (READ 1.1.0) | FIXED | docs/SPEC-REDIS.md:23-25 names acl render, check and apply; the dialling sentence still omits the ACL verbs (finding 8) |
| the spec verb-list test should catch every shipped verb (READ 1.1.0) | STILL OPEN | `TestSpecRedisVerbBlockNamesEveryVerb` passes while install/uninstall are absent because the name regex misses the dynamically built names (finding 7) |
| `--max` promised for a verb that lists (USE 1.1.0) | STILL THERE | the top help still says it and `recall --max 5` is refused (finding 4) |
| `"fn load"` quoted gives a self-referential remedy (USE 1.1.0) | STILL THERE | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?` (finding 22) |
| fn and ACL verbs take no `--json` (READ 1.2.0, another rater) | STILL THERE | `fn check --json` and `install store --json` are refused (finding 13) |
| effect lines call store writes local writes (READ 1.2.0, another rater) | STILL THERE | `spill -h`, `fn load -h` and `acl apply -h` say `local write: writes files on this machine` (finding 2) |
| docs/CLI.md has no First run for nova-redis (READ 1.1.0 and 1.2.0) | STILL THERE | finding 20 |
| docs/CLI.md keeps a ticket number (READ 1.2.0, another rater) | STILL THERE | docs/CLI.md:2671 names `#3620` (finding 21) |
| `install store --dry-run` was not rated before this card | NEW DEFECT | the missing-login-flag run prints the may-have-written FAILED line at exit 1 (finding 1) |
