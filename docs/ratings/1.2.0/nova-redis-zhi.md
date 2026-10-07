# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: Zhi, OpenAI GPT-6 Luna (openai/gpt-6-luna), harness OpenCode
Build: c76fcb249cc1
READ: 7/10
USE: 7/10

## Reasons

Built the current checkout and read `nova-redis help`, every listed verb's `-h`,
and `docs/SPEC-REDIS.md` cold. The binary identifies as
`nova-redis v1.0.1-0.20261007015429-c76fcb249cc1`. Exercised it against a fresh
throwaway directory: `spill --dry-run --json`, `serve --dry-run`, `acl render`,
`acl apply --dry-run`, and `install` and `uninstall --dry-run`. No Redis server
was started and no live store was contacted; successful store reads, writes,
function deployment and ACL checks therefore remain untested.

READ 7. The banner is short, its first line says what the tool does, and the
individual help pages describe flags, effects and refusal behavior in useful
detail. The spec explains the persistence, auth and ACL model and records the
major operational guarantees. What keeps it from 10: the spec's verb list
omits the four installed service verbs, its list of verbs that dial omits both
ACL verbs, and a socket address the binary accepts is undocumented; top-level
help also promises `--max` despite no listing verb, and repeats one exit table
that does not describe the ACL or unit verbs. The install examples contain
literal angle-bracket placeholders and are not runnable, while `docs/CLI.md`
has no `### First run` for this tool.

USE 7. The scratch dry run is genuinely store-free and emits the same useful
facts in JSON; `acl apply --dry-run` returned four `ACL WOULD-SET` plans and a
successful summary without dialing, and `serve --dry-run` explicitly reported
`created=0 launched=0`. Install and uninstall dry runs also described their
unit path without changing it. The score is limited because the tool labels
store writes as local writes, `fn load` has no dry run before a replacing
deployment, and the inspection and write verbs for functions and ACLs have no
JSON result. I did not claim a real store operation worked: the card forbids
starting a server here and there was no throwaway store already available.

## Findings

| # | tool | where | defect | fix |
|---|---|---|---|---|
| 1 | nova-redis | docs/SPEC-REDIS.md:15-28 | the normative verb block omits `install store`, `install bus`, `uninstall store` and `uninstall bus`, although the binary ships them | list all four service verbs and specify their effects and dry-run behavior |
| 2 | nova-redis | docs/SPEC-REDIS.md:50 | the list of verbs that dial a store names spill, recall and fn only; `acl check` and `acl apply` also dial | include both ACL verbs and state the common address, login and failure contract |
| 3 | nova-redis | internal/docs/redis_test.go:94-110 | `TestSpecRedisVerbBlockNamesEveryVerb` passes despite the omitted install/uninstall verbs because its name regex misses the dynamically constructed names from `installVerbs` | derive the checked verb names from the tool or explicitly include generated install and uninstall verbs |
| 4 | nova-redis | internal/tool/tool.go:522 | top-level help promises `--max` for a listing verb, but no nova-redis verb lists and none accepts that flag | emit the `--max` guidance only for tools with a listing verb |
| 5 | nova-redis | cmd/nova-redis/main.go:131 | each verb's help repeats an exit table about spill, recall, functions and serve; it omits ACL and unit outcomes and says a store refusal is exit 1 although an unreachable store exits 2 | provide an accurate per-verb exit table, including ACL and service-unit outcomes |
| 6 | nova-redis | cmd/nova-redis/main.go:152; cmd/nova-redis/fn.go:63; cmd/nova-redis/acl.go:211 | spill, fn load and acl apply write the remote store but their effect help says `local write: writes files on this machine`, misrepresenting the blast radius | add a store-write effect and use it on these verbs |
| 7 | nova-redis | cmd/nova-redis/fn.go:57-68 | fn load can replace the store's function library but offers no `--dry-run`, unlike the other write verbs | add a store-free preview of the target and embedded library digest |
| 8 | nova-redis | cmd/nova-redis/fn.go:64-81; cmd/nova-redis/acl.go:183-216 | fn and ACL verbs reject or omit `--json`, leaving AI callers without the structured result supported by spill, recall and version | route these verbs through the shared result renderer and expose `--json` |
| 9 | nova-redis | cmd/nova-redis/install.go:63 | `install store -h` and `install bus -h` show `<seat>`, `<file>`, `<path>` and `<NAME>` literally, so the advertised example is not a command a reader can run | replace the placeholders with a runnable dry-run example using safe concrete values |
| 10 | nova-redis | docs/CLI.md:2498 | the nova-redis command reference has no `### First run` transcript or first-run recoveries | add a transcript generated from a safe dry run and explain its output and common first-run mistakes |
| 11 | nova-redis | `spill --addr <absolute-socket-path>`; cmd/nova-redis/main.go:150 | an absolute path is accepted and dialed as a Unix socket, while help and the spec promise only `<host:port>`; my missing throwaway socket reached the dial path and failed as `invalid argument` | either reject socket paths before dialing or document the absolute Unix-socket form and its behavior |

## Good, keep

- `spill --dry-run --json` performs validation without a store and reports the
  key, TTL, expiry, byte count, address and `written=0` as typed fields.
- `acl apply --dry-run` does not dial: it rendered four intended users and
  returned exit 0 with `set=0` and `would=4`.
- `serve --dry-run` states its bind, persistence and auth plan while confirming
  it created no directory and launched no process.
- Refusals name the bad input and an actionable next command; the missing
  socket test was classified as unreachable instead of being mistaken for a
  successful write.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| `acl apply --dry-run` dialed the store and failed rather than planning (USE 1.1.0) | FIXED | `acl apply --dry-run --addr 127.0.0.1:16379` printed four `ACL WOULD-SET` lines and `ACL APPLY OK ... set=0 would=4`, exit 0, without a server |
| the spec omitted the ACL verbs (READ 1.1.0) | FIXED, partly | `docs/SPEC-REDIS.md:23-25` now names ACL render, check and apply in the verb block; its separate dialing list at line 50 still omits check and apply (finding 2) |
| the spec verb-list test should catch every shipped verb | NOT CAUGHT | `TestSpecRedisVerbBlockNamesEveryVerb` passes while install/uninstall are absent because `internal/docs/redis_test.go:94` does not parse their dynamic names (finding 3) |
| help and spec say host:port, while absolute Unix-socket paths are accepted (USE 1.1.0) | STILL THERE | a spill with the absolute path of a missing throwaway socket reached the Unix dial path, despite the `<host:port>` help (finding 11) |
| `recall` and `fn load` lack `--dry-run` (USE 1.1.0) | CHANGED | recall is read-only; fn load still has no dry-run (finding 6) |
