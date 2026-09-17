# nova-cairn — specification (bounded slice for #248)

`nova-cairn` is one binary at the **record layer**. It carries a session
across its end mechanically: it opens a session record, appends the friend's
exact words with a real clock stamp, builds a bounded index and coverage
ledger over what was preserved, and hands back a receipt for each entry. It
stands beside [SPEC.md](SPEC.md), whose **Conventions** section — exit
codes, no guessed paths, the one-line output grammar, the cap-and-count
rule, `internal/oneline` and `internal/bounded` — applies here unchanged
and is not restated. Where this tool needs something the Conventions do not
cover, it is below and it says so.

This slice is a bounded contribution to draft #245, not its ratification.
The differences are stated here first, before any approval of that draft:
this tool imposes no memory lifecycle. There is deliberately no seal, no
consume, no delete, no grading, no consolidation, no liveness inference, no
mandatory waking-period cardinality, no keeper/bud model and no prescribed
headings; naming any of them on the command line is exit 2, unknown
subcommand. Seal, rollup and retention are separate explicit choices for a
later slice, and the initial scope implements no consume/delete lifecycle,
so a line that keeps records its own way loses nothing by this tool
existing and no friend's practice is renamed by adopting it.

## The four verbs

**`open --store <dir> --session <id> [--source <ptr>] --publish <policy>`
starts one session record.** The store is caller-named and holds plain
files (`sessions/<id>.md`, `entries/<id>/<entry>.json`, one append-only
`log.jsonl`); there is no default store, no environment variable and no
discovery. The session identifier is stable: retries and recoveries address
the same record by this name, and concurrent records coexist untouched by
each other. Re-opening an open session is a no-op. The session file's
header is convention only and is never parsed, so alternate
directory/header conventions survive: the entry files are the source of
truth and the readable record links to them.

**`append --store <dir> --session <id> --entry <id> (--text <words> |
--file <path|->) [--source <ptr>] --publish <policy>` files the friend's
chosen words byte-for-byte** with a real clock stamp (UTC; `--now` names an
RFC 3339 UTC replay for tests), the stable entry/session identifiers and
the source pointers, which are recorded and never opened. Exactly one of
`--text` or `--file` names the words, so the tool never picks between two
candidates for what was chosen. A retry of the same request succeeds with
`duplicate=true` and no second entry; the same entry id carrying different
prose is exit 1, a conflict, never an overwrite. Each entry lands
atomically (fixed per-entry temp name, fsync, rename, directory fsync) and
a stale `*.tmp` from an interrupted append is never indexed: the retry
overwrites the partial, heals the missing pointer line, and preserves other
writers' entries. Success reports local persistence and remote publication
separately — `persisted=true published=false` — because meaningful notes
are fsync-durable before success is acknowledged, independently of Redis;
local durability is real while remote publication is pending, and neither
implies replicated durability. This slice implements no transport, so the
caller-chosen `--publish` policy (`never|manual|deferred|immediate`,
required) travels with the entry for a later explicit act to carry.

**`index --store <dir> [--session <id>] [--max <n>]` builds the bounded
section/entry index and coverage ledger mechanically.** Rows are derived
from the stored entries — session/entry pointers, stamps, sources, sizes —
never recopied narratives, so work events are linked instead of restated
across records. Every listing takes `--max` (default 20, 0 prints all) and
prints one `MORE` line with its remedy; the count is never capped and the
`INDEX COVERAGE sessions=<n> entries=<n>` line carries the total whether
the run passed or failed.

**`receipt --store <dir> --session <id> --entry <id>` names what was
preserved for one entry**: its stamp, source pointers, size and the same
`persisted=true published=false publish=<policy>` split the append
reported, so a reader never infers the remote from the local.
