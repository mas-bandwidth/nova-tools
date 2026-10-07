# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: deepseek/deepseek-v4.1-flash, harness opencode (a friend's re-rate card)
Build: 0eee1142d7d4
READ: 8/10
USE: 8/10

## Reasons

READ. Read cold from `nova-redis help`, the `-h` of every group and every verb,
and docs/SPEC-REDIS.md, on the branch head built from source
(`nova-redis v1.0.1-0.20261007230400-0eee1142d7d4 linux/amd64 go1.26.6`). The
banner answers the three onboarding questions before its usage lines: what the
tool is, where the state lives (store, ACL file, function library), and that the
first run needs no store. Every verb's `-h` carries its usage line, every flag
with what it wants, an effect line and the exit table, and the refusals are the
best part of the tool: one line, every independent problem at once, a reason and
a next step. The spec is honest about bind, auth, persistence and the ACL file,
and the scratch invariant (owner prefix plus a required TTL) is enforced at the
parser and again at the package seam. What keeps READ from 10: the normative
spec still teaches the deprecated `--addr <host:port>` spelling only, omits the
Unix-socket form, and its verb block omits the four service verbs the binary
ships (findings 6 and 7); the banner's example block uses `--addr`, and its
second spill line would write a real store on Redis's own default port if pasted
(finding 3); `serve -h`'s own example is the literal `version`, so the block
prints `nova-redis version` a second time (finding 4); the effect line calls a
store write `local write: writes files on this machine` (finding 10); the top
help promises `--max` for a listing verb that no verb here is (finding 8); five
verbs carry an empty or placeholder example (finding 9); and `version --json`
hides the version in `payload` with empty `facts` (finding 11).

USE. Used for real with no store started on the machine (the card forbids one
here; the store already listening on the bench was never dialled). Everything
that needs no store ran for real: `spill --dry-run` in lines and in JSON,
`acl render`, `acl apply --dry-run`, `serve --dry-run`, `install store
--dry-run`, `uninstall store --dry-run`, `version`, and every refusal class,
with the dialling verbs provoked against a dead loopback port and a missing
socket path. Two jobs went end to end with no wrong guess from this tool's help:
planning a spill (`spill --dry-run` prints key, ttl, expiry, bytes, store and
`written=0` in one line, the same facts as one JSON object) and planning the ACL
(`acl render`, then `acl apply --dry-run`, which prints four `ACL WOULD-SET`
lines at exit 0 without dialling). `serve --dry-run` refused a wildcard bind and
a relative `--dir` in one run; `install store --dry-run` prints the whole unit
and changes nothing; exit codes held (0 on a plan, 2 on every refusal and every
dead store). What keeps USE from 10: `install store --dry-run` (and `install
bus`) with any login flag missing prints `INSTALL-STORE FAILED: --dry-run was
given and the verb never read it (Call.DryRun); it may have written` at exit 1,
a false may-have-written on a write verb's own dry run (finding 1); a missing
address is refused as `--addr is required`, naming the deprecated flag (finding
2); `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn
load?` (finding 5); the inspection verbs `fn check`, `acl check`, `acl render`
and `serve` take no `--json`, so an AI cannot parse the answers it acts on
(finding 12); `fn load` and `recall` have no `--dry-run` (findings 13); and an
unreachable store gets a login remedy from `acl check` and `acl apply` (finding
14).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/install.go:203 | `install store --dry-run` (and `install bus`) with any of `--secrets`, `--as`, `--key`, `--sops`, `--secret` missing reaches the login refusal at install.go:176, which returns before `c.DryRun()` is called at install.go:203, so the skeleton's check at internal/tool/tool.go:589 replaces the clean refusal with `INSTALL-STORE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1; a write verb's own dry run is told it may have written when it wrote nothing | call `c.DryRun()` at the top of `installRun` and `uninstallRun` before any refusal, so every refusal stays a clean `REFUSED` at exit 2 | M |
| 2 | cmd/nova-redis/main.go:325 | a missing address is refused as `SPILL REFUSED: --addr is required: the store's address as <host:port> ...`, naming the deprecated `--addr` rather than `--redis`, the flag the usage, the flag list and the family's one shape use; the reader then has to learn the alias to retry | name `--redis` in the refusal and keep `--addr` as the accepted old spelling | S |
| 3 | cmd/nova-redis/main.go:156,192 | the banner's `example:` block and the spill/recall `-h` examples use `--addr`; its second spill line (`spill --addr 127.0.0.1:6379 ...`) dials Redis's own default port and writes a real store, so a reader who copies the first run spills into whatever store answers there | use `--redis` in the examples and make the real write the throwaway Unix-socket recipe the `UsageNote` already carries | S |
| 4 | cmd/nova-redis/serve.go:98 | the serve verb's `Example` is the string `version`, so the banner's example block prints `nova-redis version` a second time and shows no serve line at all | replace it with a real serve plan such as `serve --dry-run --bind 127.0.0.1 --port 6379 --dir <store-dir>` | S |
| 5 | internal/nsprint/verbflag/verbflag.go:111 | `nova-redis "fn load" -h` answers `unknown verb "fn load"; did you mean fn load?`, a remedy identical to the input and saying nothing about the verb being two arguments, so an AI that quotes the verb is sent in a circle | answer that `fn load` is two words and give the runnable form, or match the joined spelling | S |
| 6 | docs/SPEC-REDIS.md:18-28 | the normative verb block lists only serve, spill, recall, the fn verbs, the acl verbs, version and help; it omits `install store`, `install bus`, `uninstall store` and `uninstall bus`, which the binary ships (cmd/nova-redis/install.go:36-54) and docs/CLI.md:2608-2609 documents | add the four service verbs with their effects and dry-run behaviour | S |
| 7 | docs/SPEC-REDIS.md:19-25 | the spec's usage lines teach `--addr <host:port>` only: never the current `--redis`, never the Unix-socket form the binary accepts (main.go:317) and the banner's own recipe uses, so a cold reader following the normative document learns the deprecated spelling and the wrong address shape | write `--redis` first, note `--addr` as the one-release alias, and say `<host:port> or the absolute path of a Unix socket` | S |
| 8 | internal/tool/tool.go:522 | the top help says `A verb that lists takes --max <n> (default 20, 0 lists all) ...`; no nova-redis verb lists, and `recall --max 5` is refused as an unknown flag after the help sent the reader after a flag that exists nowhere here | print the `--max` sentence only for a tool that has a listing verb | S |
| 9 | cmd/nova-redis/fn.go:62,77; cmd/nova-redis/acl.go:181,195,210 | `fn load`, `fn check`, `acl check` and `acl apply` declare an empty `Example`, so their `-h` and the banner show no first command though each has a natural one; `install store -h`'s own example (install.go:69) is angle-bracket placeholders that do not run | add one runnable example each, using `--redis` and the `--dry-run` where the verb has it | S |
| 10 | cmd/nova-redis/main.go:157; cmd/nova-redis/fn.go:63; cmd/nova-redis/acl.go:211 | `spill -h`, `fn load -h` and `acl apply -h` say `effect: local write: writes files on this machine`, but each writes the store at `--redis`, which may be another machine, and none writes a local file; an AI judging blast radius from the effect line misjudges it | add a store-write effect to `internal/tool` (`store write: writes the store at --redis`) and use it on the three verbs | S |
| 11 | internal/tool/tool.go:452 | `version --json` prints `{"facts":{},"payload":"nova-redis v1.0.1-0.20261007230400-0eee1142d7d4 linux/amd64 go1.26.6"}`, so the version, OS, arch and Go version are one unparsed string in `payload` instead of structured facts | render them as individual facts (`version`, `os`, `arch`, `go`) while keeping the line | S |
| 12 | cmd/nova-redis/fn.go:80; cmd/nova-redis/acl.go:182,196; cmd/nova-redis/serve.go:99 | `fn check`, `acl check`, `acl render` and `serve` refuse `--json` (`unknown flag --json; the flags of fn check are ...`), so the inspection verbs whose answers an AI acts on are prose only while spill, recall and version give one object | give every verb `--json` through the shared result value | M |
| 13 | cmd/nova-redis/main.go:188; cmd/nova-redis/fn.go:58 | `recall` and `fn load` take no `--dry-run` (both are refused as an unknown flag), so the read path and the library deploy cannot be planned store-free, unlike spill, serve and acl apply | add `--dry-run` printing the key or the library digest, the store and the login each would use, dialling nothing | M |
| 14 | cmd/nova-redis/acl.go:291 | `acl check --redis 127.0.0.1:1` and `acl apply --redis 127.0.0.1:1` answer `connection refused` with `remedy="log in as a user that may run ACL GETUSER, ACL CAT and ACL SETUSER..."`, sending the reader to fix a login that was never tried, where spill says `start the store or correct the address` | choose the remedy by cause as fn.go does: unreachable gets the address remedy, a refused login the login one | S |

## Good, keep

- `spill --dry-run` is a bounded store-free plan: key, ttl, expiry, bytes, store and `written=0` in one line, and the same facts as one JSON object; a real spill refuses a missing owner, a missing TTL and a missing value each in its own run, naming every problem at once with the reason.
- `acl apply --dry-run` is a real dry run: four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4` at exit 0, with nothing listening.
- `acl render` opens no store and prints the build's key families and `ACL SETUSER` lines, so the ACL can be planned before any store exists.
- `serve --dry-run` validates `--bind`, `--port` and the absolute `--dir` and refuses a public interface before anything starts, reporting the binding, port, ACL file and rules with `created=0 launched=0`.
- The refusals name every independent problem in one run and carry a next step; exit codes tell the truth (0 on every plan, 2 on every refusal and every dead store); passwords never appear in arguments or output, the default user is the password's SHA-256.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| `acl apply --dry-run` dialled the store and reported it may have written (1.1.0 USE) | FIXED | `acl apply --dry-run --redis 127.0.0.1:1` printed four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true users=4 set=0 would=4`, exit 0, with nothing listening |
| `--addr` refused the Unix-socket form the throwaway recipe makes (1.1.0 USE) | FIXED, undocumented | `spill --dry-run --redis <dir>/redis.sock ...` printed the plan and dialled nothing; the flag help now says `or the absolute path of a Unix socket`, though the spec still says `<host:port>` (finding 7) |
| the spec's verb block omitted the ACL verbs (1.1.0 READ, both raters) | FIXED | docs/SPEC-REDIS.md:23-25 names acl render, check and apply; the service verbs are still omitted (finding 6) |
| the restart test opened with an unconditional `t.Skip` (1.1.0 READ) | FIXED | cmd/nova-redis/serve_functional_test.go:25 runs the restart against a real `redis-server` |
| `--max` promised for a verb that lists (1.1.0 USE) | STILL THERE | finding 8 |
| `"fn load"` quoted gives a self-referential remedy (1.1.0 USE) | STILL THERE | finding 5 |
| recall has no `--dry-run` (1.1.0 USE) | STILL THERE | finding 13 |
| the banner's example block shows no `--dry-run` first run | STILL THERE, now with `--addr` | finding 3 |
| the inspection verbs an AI acts on take no `--json` (1.1.0 READ) | STILL THERE | finding 12 |
| `install store --dry-run` on a missing login flag was not rated before | NEW DEFECT | finding 1 |
