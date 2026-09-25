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

**The store's shape is read, never imposed.** Beside the layout above, a
store may keep **one markdown file per session directly under it** —
`<store>/<session>.md`, which is how a friend appending by hand already
keeps a record. Both verbs read it: `open` on such a record is a no-op, and
`append` lands a dated `## <stamp> — <entry>` section at the end of the
file in the file's own shape, one blank line between sections, the words
byte-for-byte beneath the heading. Nothing appears beside the file — no
`entries/`, no `log.jsonl`, no index — because the file IS the record; the
duplicate and conflict rules below read that section instead of an entry
file, and `index`/`receipt`, which report on stored entries, cover the
first shape only while the coverage ledger counts the file. The nested
record wins when a store somehow holds both. The hurt this is written from
(2026-09-18): an append into a bench store refused `no such session
"b9395d11"; open first` with `cairns/b9395d11.md` in place, and running the
named remedy would have written a second record and split one session in
two. **A refusal names the remedy verb whole** — `open first: nova-cairn
open --store <dir> --session <id> --publish <policy>` — rather than a verb
the reader must reconstruct.

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

## Tests this spec demands

One numbered line per Go test function: 28 lines, 28 tests. 16 exist in `internal/cairn` or
`cmd/nova-cairn`; 12 (lines 5, 12, 13, 18, 19 and 21–27) are named here and not yet written.
Where one test holds several behaviours of the spec, they share its line; where two tests hold
one rule of the spec (lines 9–10 and 15–16), each test has its own line.
Every test that touches a store uses a throwaway `t.TempDir()` store named on the command line
(or in the `cairn` package's `Open`/`Append` calls) — no network, no Redis, no secret.
Not every test writes entries: lines 2, 14, 17 and 28 are refusal-only, as 19 and 22 will be,
and assert an exit code or an error with nothing stored.
Each of the 12 unwritten tests must be shown red before it is green when it lands; this section
makes no red-first claim for the 16 that exist.

1. `TestOpenAppendIndexReceiptRoundTrip` — `open` starts one session record under a caller-named store; the record is written to and read back; `index` builds the bounded section/entry index and coverage ledger mechanically.
2. `TestMissingFlagsAreRefusedNeverGuessed` — there is no default store, no environment variable and no discovery; a missing `--store` is a refusal.
3. `TestDuplicateAppendIsIdempotentAndConflictingEntryRefused` — the session identifier is stable: retries and recoveries address the same record by this name; a retry of the same request succeeds with `duplicate=true` and no second entry; the same entry id carrying different prose is exit 1, a conflict, never an overwrite.
4. `TestConcurrentRecordsAndAlternateHeaders` — concurrent records coexist untouched by each other.
5. `TestReOpenOfANestedSessionIsANoOp` — re-opening an open session is a no-op.
6. `TestOpenConcurrentRecordsAndAlternateHeaders` — the session file's header is convention only and is never parsed; alternate header conventions survive.
7. `TestAppendLandsInTheBenchFileStore` — a store may keep one markdown file per session directly under it (`<store>/<session>.md`), and both verbs read it; bench `append` lands a dated `## <stamp> — <entry>` section at the end, one blank line between sections, words byte-for-byte; nothing appears beside the bench file: no `entries/`, no `log.jsonl`, no index.
8. `TestOpenOnABenchFileIsANoOpAndNeverSplitsTheRecord` — `open` on a bench record is a no-op.
9. `TestAppendToTheBenchFileRetriesAsADuplicate` — the duplicate rule reads the bench section instead of an entry file: a retry of the same request is `duplicate=true` and adds no second section.
10. `TestAppendToTheBenchFileRefusesDifferentProseUnderTheSameID` — the conflict rule reads the bench section instead of an entry file: the same entry id carrying different prose is a conflict.
11. `TestCoverageCountsTheBenchSessionFiles` — the coverage ledger counts the bench file.
12. `TestIndexAndReceiptCoverTheFirstShapeOnly` — `index`/`receipt`, which report on stored entries, cover the first shape only.
13. `TestNestedRecordWinsWhenStoreHoldsBoth` — the nested record wins when a store somehow holds both shapes.
14. `TestAppendWithNoRecordAnywhereNamesTheOpenVerb` — a refusal names the remedy verb whole (`nova-cairn open --store … --session … --publish …`).
15. `TestAppendKeepsExactProseAndReportsPersistenceSeparately` — `append` files the friend's chosen words byte-for-byte; success reports local persistence and remote publication separately (`persisted=true published=false`).
16. `TestAppendViaFileAndStdinKeepsExactBytes` — words named by `--file <path|->`, from a file or from stdin, are filed byte-for-byte.
17. `TestBadClockIsRefused` — the stamp is a real clock in UTC; `--now` names an RFC 3339 UTC replay and a non-RFC 3339 value is exit 2.
18. `TestSourcePointerIsRecordedNeverOpened` — the source pointers are recorded and never opened.
19. `TestBothTextAndFileAreRefused` — exactly one of `--text` or `--file` names the words; giving both is refused.
20. `TestInterruptedAppendRecoversAndPreservesOtherWriters` — each entry lands atomically (fixed temp name, fsync, rename, dir fsync); a stale `*.tmp` is never indexed and the retry overwrites the partial; the retry preserves other writers' entries.
21. `TestRetryHealsTheMissingPointerLine` — the retry heals the missing pointer line.
22. `TestInvalidPublishPolicyIsRefused` — the caller-chosen `--publish` policy (`never|manual|deferred|immediate`, required) travels with the entry; an invalid value is refused.
23. `TestIndexRowCarriesStampSourceAndSize` — index rows are derived from the stored entries: session/entry pointers, stamps, sources, sizes — never recopied narratives.
24. `TestIndexMaxDefaultTwentyAndZeroPrintsAll` — every listing takes `--max` (default 20, 0 prints all).
25. `TestIndexPrintsMORELineWithRemedy` — index prints one `MORE` line with its remedy.
26. `TestCoverageCarriesTheTotalWhenCapped` — the count is never capped and the `INDEX COVERAGE` line carries the total whether the run passed or failed.
27. `TestReceiptReportsStampSourceSizeAndPublish` — `receipt` names stamp, source pointers, size and the `persisted=true published=false publish=<policy>` split.
28. `TestLifecycleVerbsStayRefused` — there is deliberately no seal/consume/delete/grade/consolidate/wake/rollup/retention verb; naming one on the command line is exit 2, unknown subcommand.
