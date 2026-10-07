# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: Alex, model inception/mercury-2.5, harness opencode
Build: 38890605d34d6ce6dc7ed0838006a34fd99c501a
READ: 8/10
USE: 8/10

## Reasons

Read cold, then used for real on a throwaway socket store. The build is commit 38890605d34d6ce6dc7ed0838006a34fd99c501a (sprint/mechanical-2026-10-02 tip), built from source on vision; the binary reads `nova-redis devel linux/amd64 go1.26.6`.

READ 8. The banner says what the tool does in one sentence, how it works in four lines, and the first run needs no store. Every verb has `-h` with flags, usage, effect and exit table. The spec lists the ACL verbs. What keeps it from 10: the spec omits install/uninstall service verbs (6), dialling-verb sentence omits ACL verbs (8), documents `--addr` not `--redis` (9), doesn't document Unix sockets (10), CLI has no `### First run` (20); the effect line says "writes files on this machine" for spill/fn load/acl apply (2), `--max` sentence appears for a tool with no listing verb (4), exit tables repeat for all verbs and don't include ACL outcomes (19).

USE 8. Real work succeeded store-free: spill --dry-run, acl render, acl apply --dry-run, serve --dry-run; against a throwaway: spill, recall, fn load, fn check, acl check. Exit codes held (0 on plans, 2 on refusals and dead stores). What keeps it from 10: fn load/recall have no --dry-run (11,12), seven verbs refuse --json (13), acl check give wrong remedy on unreachable (7), success lines use wrong grammar (15).

## Findings

| # | tool | where | finding | fix |
|---|---|---|---|---|
| 1 | nova-redis | cmd/nova-redis/main.go:157; cmd/nova-redis/fn.go:63; cmd/nova-redis/acl.go:211 | `spill -h`, `fn load -h` and `acl apply -h` say `effect: local write: writes files on this machine` but each writes the store at `--redis`, which may be another machine | add a store-write effect (`writes the store at --redis`) for these three verbs |
| 2 | nova-redis | cmd/nova-redis/main.go:325 | a missing address is refused as `SPILL REFUSED: --addr is required: the store's address as <host:port>` naming the deprecated `--addr` instead of `--redis` | name `--redis` in the refusal |
| 3 | nova-redis | internal/tool/tool.go:522 | the top help says `A verb that lists takes --max <n>` but no nova-redis verb lists | print the `--max` sentence only for a tool with a listing verb |
| 4 | nova-redis | cmd/nova-redis/main.go:136 | every verb's `-h` quotes the whole tool's exit table and names no acl apply/check, install or uninstall outcome | give each verb its own exit table |
| 5 | nova-redis | docs/SPEC-REDIS.md:18-28 | the normative verb block omits `install store`, `install bus`, `uninstall store` and `uninstall bus` | list the four service verbs |
| 6 | nova-redis | docs/SPEC-REDIS.md:50 | the dialling-verb sentence names spill, recall, fn load and fn check only; `acl check` and `acl apply` dial a store too | name all six dialling verbs |
| 7 | nova-redis | docs/SPEC-REDIS.md:19-25; docs/CLI.md:2617,2624 | the spec block and CLI reference use `--addr <host:port>` only and never the current `--redis` | document `--redis` first and `--addr` as the one-release alias |
| 8 | nova-redis | docs/SPEC-REDIS.md:19-25; docs/CLI.md:2624 | both documents call the store address `<host:port>`, while the binary accepts the absolute path of a Unix socket | say `<host:port> or the absolute path of a Unix socket` in the flag, spec and CLI |
| 9 | nova-redis | cmd/nova-redis/fn.go:58-71 | `fn load` writes the store's function library and takes no `--dry-run` | add `--dry-run` printing the binary's digest, the store's state and the login it would use |
| 10 | nova-redis | cmd/nova-redis/main.go:188-206 | `recall` has no `--dry-run`, so the read path cannot be planned store-free | add `recall --dry-run` printing the key, the store and the login |
| 11 | nova-redis | cmd/nova-redis/fn.go:65,80; cmd/nova-redis/acl.go:184,198,214; cmd/nova-redis/install.go:65,88 | several inspection and service verbs take no `--json` | give these verbs `--json` through the shared result value |
| 12 | nova-redis | cmd/nova-redis/acl.go:271 | `acl check` on a dead store gives a login remedy, not "start the store or correct the address" | choose the remedy by cause as fn.go does |

## Good, keep

- spill --dry-run is a bounded store-free plan with key, ttl, expiry, bytes, store and `written=0` in one line.
- acl apply --dry-run is now a real dry run: four `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true`, exit 0, no dial.
- Refusals name every problem in one run and say why.
- Exit codes are honoured exactly: 0 on every plan, 2 on every refusal and every dead store.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| acl apply --dry-run dials the store | FIXED | `acl apply --dry-run` printed `ACL WOULD-SET` lines and `ACL APPLY OK dry-run=true`, exit 0 |
| --addr refuses the socket nova-table makes | FIXED, undocumented | `spill --addr <dir>/redis.sock` dialled the unix socket |
| spec verb list omits the acl verbs | FIXED | docs/SPEC-REDIS.md:23-25 lists acl render, check and apply |
| --max promised for a verb that lists | STILL THERE | finding 3 |
| recall has no --dry-run | STILL THERE | finding 10 |
