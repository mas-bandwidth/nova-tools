# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: Zhi, model deepseek/deepseek-v4.1-flash, harness dsh (the DeepSeek Harness headless runner)
Build: 0389f76634ec
READ: 8/10
USE: 8/10

## Reasons

Read cold, then used for real. The build is the sprint base tip 0389f76634ec,
compiled on a Linux bench; the binary answers `nova-redis v1.0.1-0.20261007224659-0389f76634ec linux/amd64 go1.26.6`. I read `nova-redis help`, the `-h` of every group and every verb, the nova-redis section of
`docs/CLI.md`, `docs/SPEC-REDIS.md`, the README row and `cmd/nova-redis/README.md`.

READ 8. The banner's first line says what the tool is, the `how it works:`
paragraph names the nouns and where the state lives, and every verb's `-h`
prints its usage, every flag with what it wants, its effect and the exit table.
The spec is normative and mostly matches the code, and the README's first-run
transcript is executed line for line by `cmd/nova-redis/firstrun_test.go`. What
keeps it from 10: the normative verb block still writes `--addr <host:port>` and
never the current `--redis` or the Unix socket the flag help accepts (finding
17), and it omits the four service verbs the binary ships (finding 14); the
dialling-verb sentence omits `acl check` and `acl apply` (finding 16); the CLI
reference's `<login>` line, its JSON envelope and its `acl check` lines promise
shapes the binary does not print (findings 17, 18, 19); the section has no
`### First run` (finding 20) and still names a ticket (finding 21);
`install store -h`'s example is angle-bracket placeholders (finding 22); the
README row sells scratch values only and its first command hits a live store
(finding 23); and one ExitTable is quoted by every verb (finding 11).

USE 8. Used for real in a throwaway store — `redis-server` on a Unix socket in a
private temp directory on the bench, the banner's own recipe — and against a
dead loopback port: spill and `spill --dry-run` in lines and JSON, recall in
lines and JSON, a miss, `fn load`, `fn check`, `acl render`, `acl check`,
`acl apply --dry-run`, `serve --dry-run` (valid, wildcard bind, relative dir),
the install and uninstall dry-run plans, every refusal I could provoke, and the
exact first-run refusals `cmd/nova-redis/README.md` pins. Every real work ran
with no wrong guess and the exit codes held (0 on every plan, 1 on the miss, 2
on every refusal and dead store), except where the dry-run guard breaks them.
What keeps it from 10: `install store --dry-run` and `install bus --dry-run`, and `spill --dry-run` that
refuse before the verb reads `DryRun` print the may-have-written `FAILED` line
at exit 1 instead of a clean `REFUSED` at exit 2 (findings 2 and 3); an
explicitly named `--password-env` whose variable is empty is dropped when no
`--user` is given and the dial still goes out (finding 7); recall and `fn load`
cannot be planned store-free (finding 8); the inspection verbs an AI most wants
to parse take no `--json` (finding 9); `acl check` on an unreachable store gives
a login remedy (finding 6); the effect line calls a store write a local write
(finding 4); the help promises `--max` for a tool with no listing verb (finding
12); the missing-address refusal names the deprecated `--addr` (finding 5); the
quoted two-word verb remedy is self-referential (finding 13); and success is
printed in three grammars (finding 10).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/main.go:156,192 | the banner's `example:` block and `spill`/`recall` `Example`s use `--addr` (the old spelling) and dial `127.0.0.1:6379`, Redis's default port; the `first run:` line above them says `--dry-run` needs no store, and the throwaway recipe beneath uses a socket, so a cold reader who pastes the real spill writes whatever store happens to listen on 6379 | write every example with `--redis` and the recipe's `$d/redis.sock`, and keep only the `--dry-run` line dialling nothing | S |
| 2 | cmd/nova-redis/install.go:203 | `install store --dry-run` (and `install bus`) that refuses before `c.DryRun()` — a missing login field, a bad `--bind`, port or `--dir` — trips the skeleton guard (`pkg/tool/tool.go:587-593`) and prints `INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1, where the honest answer is `INSTALL-STORE REFUSED` at exit 2 | read `c.DryRun()` once at the top of `installRun` (and `uninstallRun`), as `serveRun` does | S |
| 3 | cmd/nova-redis/main.go:211-225 | the same guard on `spill --dry-run`: a login refusal (a named user whose password variable is empty, a malformed `--password-env`) prints `SPILL FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1, while the same line without `--dry-run` is `SPILL REFUSED ...` at exit 2, so the plan claims it may have written when it wrote nothing | read `c.DryRun()` before the login check in `spillRun` | S |
| 4 | cmd/nova-redis/main.go:157; cmd/nova-redis/fn.go:63; cmd/nova-redis/acl.go:211 | `spill -h`, `fn load -h` and `acl apply -h` say `effect: local write: writes files on this machine`, but each writes the store at `--redis`, which may be another machine, and none writes a local file, so an AI judging blast radius from the effect line misjudges it | add a store-write effect (`store write: writes the store at --redis`) to `pkg/tool` and use it on the three verbs | S |
| 5 | cmd/nova-redis/main.go:325 | a missing address is refused as `SPILL REFUSED: --addr is required: the store's address as <host:port> ...`, naming the spelling the tool's own flag help calls old; the retry then prints `SPILL NOTE --addr is --redis`, teaching the deprecated name on the first run | name `--redis` in the refusal and move the pinned transcripts (`cmd/nova-redis/README.md:40`, `docs/TESTS.md:924`) with it | S |
| 6 | cmd/nova-redis/acl.go:290-291 | `acl check --redis 127.0.0.1:1` (a store that never answered) ends `remedy="log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER..."`; the login was never tried, and `fn`'s own remedy sends the unreachable case to the address | choose the remedy by cause: unreachable to `start the store or correct the address`, a refused login to the login | S |
| 7 | cmd/nova-redis/main.go:406-413 | `--password-env NOPE_UNSET` with no `--user` is dropped silently: `spill --redis <store> --password-env NOPE_UNSET ...` dialled as the default user with no password and wrote, while `docs/SPEC-REDIS.md:58` says a user whose password variable is empty is refused before the dial | when `--password-env` was given on the line and its variable is empty, refuse before the dial even with no `--user` | S |
| 8 | cmd/nova-redis/main.go:188-205; cmd/nova-redis/fn.go:62-80 | `recall --dry-run` and `fn load --dry-run` are refused as unknown flags, so the one read verb and the library deploy cannot be planned store-free while spill, serve and `acl apply` can | add `--dry-run` printing the key (or the library digest), the store and the login each would use | M |
| 9 | cmd/nova-redis/fn.go:62-80; cmd/nova-redis/acl.go:177-224; cmd/nova-redis/install.go:52-96 | `fn load`, `fn check`, `acl render`, `acl check`, `acl apply`, `install store`, `install bus`, `uninstall store` and `uninstall bus` take no `--json` (`fn check --json` and `install --json` are refused with the flag list), so the inspection verbs an AI most wants to parse are prose only | build every verb's result as the shared `Out` and accept `--json` | M |
| 10 | cmd/nova-redis/fn.go:122,165,174 | the success, state and failure lines lead with the state — `LOADED nova_sprint ...`, `OK nova_sprint ...`, `FAILED nova_sprint ...` — and never name the verb, their refusals spell `FN-CHECK REFUSED` while the acl failures spell `ACL CHECK FAILED` and `ACL RENDER OK`: three grammars for one tool | lead every line with the verb and one status word in one spelling, as `docs/CLI.md:2628` already promises | S |
| 11 | cmd/nova-redis/main.go:136 | one `ExitTable` is quoted by every verb's `-h`: `version -h` and `acl render -h` list recall's misses and serve's stops, and the table names no outcome of `acl check`, `acl apply`, install or uninstall | give each verb its own `Verb.ExitTable` | S |
| 12 | pkg/tool/tool.go:522 | the banner promises `A verb that lists takes --max <n> ... and says MORE for the rest`, but no nova-redis verb lists; `recall --max 5` is refused as `unknown flag --max`, after the help sent the reader after a flag that exists nowhere here | print the `--max` sentence only for a tool with a listing verb | S |
| 13 | pkg/nsprint/verbflag/verbflag.go:111 | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a remedy identical to the input, saying nothing about the verb being two arguments | match the joined form, or say `fn load is two words: run nova-redis fn load -h` | S |
| 14 | docs/SPEC-REDIS.md:18-27 | the normative verb block omits `install store`, `install bus`, `uninstall store` and `uninstall bus`, although the binary declares them (`cmd/nova-redis/install.go:39-54`) and `docs/CLI.md:2608-2609` documents them; a spec that disagrees with the code is a bug | list the four service verbs with their effects and dry-run behaviour | M |
| 15 | internal/docs/redis_test.go:94 | `TestSpecRedisVerbBlockNamesEveryVerb` passes while the block omits the service verbs because `verbNameRe` matches only a literal `Name: "..."` and the verbs are built as `Name: "install " + kind`; the guard cannot see a dynamically named verb | collect the declared names from the built `Tool` (or match the concatenation), then fix the block | M |
| 16 | docs/SPEC-REDIS.md:50 | the dialling-verb sentence names `spill`, `recall`, `fn load` and `fn check` only; `acl check --redis 127.0.0.1:1` and `acl apply` dial a store too and answer the same unreachable refusal | name all six dialling verbs and state their common address and login contract | S |
| 17 | docs/SPEC-REDIS.md:18-27; docs/CLI.md:2624 | the spec block and the CLI reference's `<login>` line say `--addr <host:port>` only and never the current `--redis`, and call the store address `<host:port>` while the binary accepts the absolute path of a Unix socket (`cmd/nova-redis/main.go:317` says so in the flag help) and the banner's recipe uses `--redis "$d/redis.sock"` | document `--redis` first, `--addr` as the one-release alias, and `<host:port> or the absolute path of a Unix socket` | M |
| 18 | docs/CLI.md:2628 | the reference says the JSON envelope always carries `result.remedy`, `items`, `notes` and `payload`; `spill --dry-run --json` prints only `{"result":{"verb","status","exit"},"facts":{...}}`, and `recall --json` for a miss prints `{"result":{"verb":"recall","status":"failed","exit":1,"word":"MISSING"},"facts":{...}}` with no remedy at all | document the envelope as the renderer emits it, and set `result.remedy` to the classified next step | M |
| 19 | docs/CLI.md:2650 | the reference says `acl check` prints `ACL OK`, `ACL MISSING` or `ACL DRIFT user=<u> role=<r>` with `keys+=` and `commands+=`; on a fresh store it printed per-user `ACL MISSING user=... role=...` lines and `ACL CHECK DRIFT users=4 differ=4`, with no add-or-remove detail | make the reference match the lines the verb prints, or print the fields it promises | M |
| 20 | docs/CLI.md:2604-2671 | the nova-redis section opens with the command block and has no `### First run`, though the standard's onboarding point requires one and the neighbouring sections carry one, and the executed transcript already exists in `docs/TESTS.md:903` | add the `### First run` transcript the repo already runs | S |
| 21 | docs/CLI.md:2671 | the section still names the ticket `nova-tools #3620`, which a cold reader cannot resolve | state the rule in the present tense and drop the number | S |
| 22 | cmd/nova-redis/install.go:69 | `install store -h`'s example is `nova-redis install store --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>`, with literal angle-bracket placeholders, so it is not a command a cold reader can run (and `install bus` has the same line) | give a runnable dry-run example with concrete values | S |
| 23 | README.md:29 | the row calls nova-redis a scratch-value store only, never naming the store-serving, ACL or function-library jobs, and its first command is `nova-redis spill --addr 127.0.0.1:6379 ...` against a live default store rather than the store-free first run | name both jobs in one sentence and make the trial the `--dry-run` plan or the socket recipe | S |

## Good, keep

- `spill --dry-run` is a bounded store-free plan: key, ttl, expiry, bytes,
  store and `written=0` in one line, and the same facts as one JSON object.
- `acl apply --dry-run` is now a real dry run: four `ACL WOULD-SET` lines and
  `ACL APPLY OK dry-run=true users=4 set=0 would=4` at exit 0, with nothing
  listening, where the 1.1.0 rating found it dialling the store.
- `serve --dry-run` refuses a wildcard bind and a relative `--dir` in one run
  and reports its bind, persistence, auth and ACL plan with `created=0 launched=0`; the banner's throwaway recipe works exactly as printed and
  `--redis <socket>` round-trips a real spill and recall.
- Refusals name the problem and carry a next step, and the exit codes tell the
  truth: 0 on every plan, 1 on a miss, 2 on every refusal and dead store, in
  lines and `--json` alike.
- `acl render` is a full store-free inventory of the users and the embedded
  function library.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| `acl apply --dry-run` dialled the store and reported it may have written (USE 1.1.0) | FIXED | `acl apply --dry-run --redis 127.0.0.1:1` printed four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4`, exit 0, with nothing listening |
| `--addr` refused the socket form the throwaway recipe makes (USE 1.1.0) | FIXED, partially documented | `spill --redis /tmp/.../redis.sock` round-tripped through a real store, and `--addr <socket>` works with a NOTE, though the spec and the CLI reference still say `<host:port>` only (finding 17) |
| the spec verb block omitted the ACL verbs (READ 1.1.0) | FIXED for the ACL verbs, still short for the service verbs | `docs/SPEC-REDIS.md:23-25` names `acl render`, `acl check` and `acl apply`; lines 18-27 still omit `install`/`uninstall` (finding 14) |
| the spec's demanded persistence proof opened with an unconditional `t.Skip` (READ 1.1.0) | FIXED | `cmd/nova-redis/serve_functional_test.go:24` runs `TestRestartOnTheSameDirKeepsTheStore` with no skip |
| the effect line calls a store write a local write (1.2.0, both other raters) | STILL THERE | `spill -h`, `fn load -h` and `acl apply -h` say `local write: writes files on this machine` (finding 4) |
| recall and `fn load` have no `--dry-run` (1.2.0, both other raters) | STILL THERE | `recall --dry-run` and `fn load --dry-run` are refused as unknown flags (finding 8) |
| the inspection verbs take no `--json` (1.2.0, both other raters) | STILL THERE | `fn check --json` and `install store --json` are refused with the flag list (finding 9) |
| `--max` promised for a verb that lists (USE 1.1.0) | STILL THERE | the top help still says it and `recall --max 5` is refused (finding 12) |
| `"fn load"` quoted gives a self-referential remedy (USE 1.1.0) | STILL THERE | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?` (finding 13) |
| docs/CLI.md has no First run for nova-redis (READ 1.1.0) | STILL THERE | finding 20 |
| `install store --dry-run` was not rated before this card | NEW DEFECT | the missing-login-flag run prints the may-have-written `FAILED` line at exit 1 (finding 2) |
| an explicitly named empty password variable with no `--user` was not rated before this card | NEW DEFECT | `spill --password-env NOPE_UNSET ...` dialled as the default user with no password and wrote (finding 7) |
