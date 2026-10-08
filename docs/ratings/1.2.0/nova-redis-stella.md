# nova-redis READ and USE rating, nova-tools 1.2.0

Rater: Stella (external gpt-5.6-terra harness)
Build: 0d56536c3d61bfeadcb70dfb614861367f5dcd51
READ: 5/10
USE: 6/10

## Reasons

This is a cold rating of the installed artifact, which identified itself as
`nova-redis v1.2.0-dev.0d56536c darwin/arm64 go1.27.1`; the Build line names
that exact source revision, not this rating branch's staged tip. I read
`nova-redis help` and every group and verb `-h`, then read the matching
`docs/SPEC-REDIS.md`. The package has unusually clear nouns and safe
pre-dial refusals, but the top-level help asks a cold reader to paste a live
write to 127.0.0.1:6379. Its generic exit table and inaccurate effects also
force an AI to infer risk instead of being told it.

The actual use was store-free as required: `spill --dry-run` against a
throwaway socket path and `serve --dry-run` against a throwaway directory both
returned `OK` with `written=0` and `launched=0`; `acl render` also completed
without opening a store. The missing-input `spill` refusal named all five
problems in one invocation. This made the ordinary scratch planning path easy
to use, but it did not make a safe real write runnable: starting a server was
out of scope, and the published help's non-dry-run examples name a potentially
live default store.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-redis help` / `cmd/nova-redis/main.go:156` | The example block labels itself a first run, but its third and fourth commands write to and read from `127.0.0.1:6379` without an isolation step. A cold paste can alter whichever local store already owns Redis's default port. | Make every pasted first-run command store-free, or create and use a throwaway socket directory in the example before any write. | M |
| 2 | `nova-redis spill -h`, `fn load -h`, and `acl apply -h`; `cmd/nova-redis/main.go:157`, `fn.go:63`, `acl.go:211` | Each reports `effect: local write: writes files on this machine`, while each can write the store named by `--redis`, including a remote store; `fn load` and `acl apply` can make consequential shared-store changes. | Describe these as store writes and name the addressed store; reserve `local write` for filesystem-only effects. | M |
| 3 | `nova-redis help`; `internal/tool/tool.go:522` | The banner says a verb that lists takes `--max`, though no listed nova-redis verb is a listing verb. A reader is sent to a flag no command accepts. | Print the `--max` sentence only when this tool has a listing verb, or name the verb that accepts it. | S |
| 4 | `nova-redis install store -h` and `install bus -h`; `cmd/nova-redis/install.go:63` | The only install examples contain literal `<seat>`, `<file>`, `<path>`, and `<NAME>` placeholders, so they are not the runnable examples the help contract promises. | Provide a safe concrete dry-run fixture, or mark the commands as a template rather than an executable example. | S |
| 5 | `docs/SPEC-REDIS.md:18-27,50-64` | The normative verb block documents deprecated `--addr`, omits all `install` and `uninstall` verbs, and its dialling list omits `acl check` and `acl apply`; the binary's help instead leads with `--redis` and ships those verbs. | Bring the spec's complete verb inventory and address spelling in line with the binary, including the ACL dialling verbs. | M |
| 6 | every verb `-h`; `cmd/nova-redis/main.go:136` | Every verb repeats one tool-wide exit table containing outcomes from unrelated verbs (for example, `version -h` explains a recall miss). An AI cannot learn a command's own success and nonzero meanings locally. | Give each verb or group its own exit table, with only its outcomes and remedies. | S |
| 7 | `nova-redis fn load -h`, `fn check -h`, `acl render -h`, and `acl check -h` | These verbs do not offer `--json`, despite the banner presenting JSON as the normal parallel rendering. Their operational output must be parsed as prose. | Route each through the shared result renderer and offer a documented JSON shape. | M |

## Good, keep

The no-store planning paths are concrete and honest. `spill --dry-run` printed
the exact key, TTL, expiry, byte count, socket path, `written=0`, and
`dry_run=true`; `serve --dry-run` named the binding, persistence, ACL file,
and `created=0 launched=0`. The multi-error refusal from a bare `spill` named
the missing address, value, owner, name, and TTL in one run and ended each line
with the help remedy. `acl render` is a useful store-free inspection and
reported its user and function totals.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0 complained that `--addr` rejected a Unix socket | IMPROVED | `spill -h` documents `--redis` as accepting an absolute Unix socket path, and the throwaway-socket dry run succeeded. |
| 1.1.0 found a missing explicit owner or TTL unsafe | KEPT GOOD | the bare `spill` refusal named both requirements before any dial or write. |
| 1.1.0 found the generic `--max` promise misleading | STILL THERE | `nova-redis help` still advertises it while no listed verb accepts it. |
