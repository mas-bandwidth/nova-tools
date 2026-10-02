# nova-cairn — specification

`nova-cairn` is one binary at the **record layer**. It carries a session
across its end mechanically: it opens a session record, appends the friend's
exact words with a real clock stamp, builds a bounded index and coverage
ledger over what was preserved, and hands back a receipt for each entry. It
stands beside [SPEC.md](SPEC.md), whose **Conventions** section — exit
codes, no guessed paths, the one-line output grammar, the cap-and-count
rule, `internal/oneline` and `internal/bounded` — applies here unchanged
and is not restated. Where this tool needs something the Conventions do not
cover, it is below and it says so.

This slice is a bounded contribution, not a ratification.
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
file. `index` and `receipt` read those same dated sections. Their byte counts
measure the whitespace-trimmed section body, matching duplicate detection;
they do not reconstruct the original append's trailing newlines. The flat
format stores no source or publication policy: receipts print `source=-`
and `publish=unknown`. Ordinary prose without machine-form entry headings
is not an indexed entry. Invalid stamps, invalid entry identifiers and duplicate
entry headings refuse rather than produce an ambiguous receipt. The nested
record wins when a store holds both shapes for a session, which counts once. The hurt this is written from
An append into a bench store refuses `no such session`
"b9395d11"; open first` with `cairns/b9395d11.md` in place, and running the
named remedy would have written a second record and split one session in
two. **A refusal names the remedy verb whole** — `open first: nova-cairn
open --store <dir> --session <id> --publish <policy>` — rather than a verb
the reader must reconstruct. The remedy quotes the caller's store and session
for a POSIX shell. Control bytes use octal decoding inside a subshell with a
sentinel to preserve trailing newlines, so the printed command stays one line
and opens exactly the named record.

**`append --store <dir> --session <id> --entry <id> (--text <words> |
--file <path|->) [--source <ptr>] --publish <policy>` files the friend's
chosen words byte-for-byte** with a real clock stamp (UTC; `--now` names an
RFC 3339 UTC replay for tests), the stable entry/session identifiers and
the source pointers, which are recorded and never opened. An append with no
`--source` carries the session's `open --source`, read back from the open
record in `log.jsonl`; a log that exists and cannot be read, or an open record
for the session that does not decode, refuses at exit 2 naming the log before
anything is written, because corrupt provenance never reads as none. So the entry, its index row and its receipt name where
it came from; every line prints `source=<ptr>`, and `source=-` is an entry with
no pointer. Exactly one of
`--text` or `--file` names the words, so the tool never picks between two
candidates for what was chosen. A retry of the same request succeeds with
`duplicate=true`, the original stored timestamp, and no second entry. The
reported stamp uses the stored precision (whole seconds for a bench heading);
a malformed stored stamp refuses rather than inventing a time. The same entry
id carrying different prose is exit 1, a conflict, never an overwrite. Each entry lands atomically through internal/atomicfile: a unique sibling temp file honors the process umask, the file is synced and renamed, and parent-directory sync is attempted on a best-effort basis. Stale random-sibling temp files from interrupted appends are never indexed or overwritten by a retry. The retry writes the complete entry and heals a missing pointer line without touching another writer's files. Success reports local persistence and remote publication
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
across records. A missing store or a missing explicitly named session refuses
at exit 2; an existing empty store or session is a successful empty index.
Every listing takes `--max` (default 20, 0 prints all) and
prints one `MORE` line with its remedy; the count is never capped and the
first line, `INDEX OK sessions=<n> entries=<n>`, carries the total whether
or not entries were elided.

**`receipt --store <dir> --session <id> --entry <id> [--text]` names what was
preserved for one entry**: its stamp, source pointers, size and the same
`persisted=true published=false publish=<policy>` split the append
reported for nested entries, so a reader never infers the remote from the local.
Flat records report `publish=unknown` because the policy was not stored.
With `--text`, the result includes the stored words as a `text` fact. For a
nested entry this is the exact stored text; for a flat record it is the
whitespace-trimmed indexed section body, because the flat format does not retain
the original append's trailing newlines. JSON carries the same text value.
A missing store, session or entry refuses at exit 2, naming what is absent.

## Tests this spec demands

The numbered cases name this contract's checks. A named case is a requirement,
not a claim that it has been implemented. Store tests use a caller-named
throwaway `t.TempDir()`; they use no live records, network, Redis or secrets.
The open-remedy shell regression is in the functional tier on POSIX systems.
New regression cases must demonstrate the defect before the repair.

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
12. `TestIndexAndReceiptReadFlatRecordsWithoutChangingThem` — index and receipt read the dated sections in a flat record, preserve its bytes, and create no sidecars. `TestFlatReadMetadataOrderingAndNestedPrecedence` checks ordering, metadata and the shared coverage count.
13. `TestNestedRecordWinsWhenStoreHoldsBoth` — the nested record wins when a store somehow holds both shapes.
14. `TestAppendWithNoRecordAnywhereNamesTheOpenVerb` — a refusal names the remedy verb whole (`nova-cairn open --store … --session … --publish …`). `TestAppendOpenRemedyRoundTripsThroughShell` executes the printed command through a POSIX shell and verifies the exact store and session.
15. `TestAppendKeepsExactProseAndReportsPersistenceSeparately` — `append` files the friend's chosen words byte-for-byte; success reports local persistence and remote publication separately (`persisted=true published=false`).
16. `TestAppendViaFileAndStdinKeepsExactBytes` — words named by `--file <path|->`, from a file or from stdin, are filed byte-for-byte.
17. `TestBadClockIsRefused` — the stamp is a real clock in UTC; `--now` names an RFC 3339 UTC replay and a non-RFC 3339 value is exit 2.
18. `TestSourcePointerIsRecordedNeverOpened` — the source pointers are recorded and never opened; an append with no `--source` carries the session's, and every verb prints `source=` (`-` for none).
19. `TestBothTextAndFileAreRefused` — exactly one of `--text` or `--file` names the words; giving both is refused.
20. `TestInterruptedAppendRecoversAndPreservesOtherWriters` — each entry lands atomically through internal/atomicfile (unique temp file, fsync, rename, parent dir fsync); a stale random-sibling `.*.tmp*` is never indexed and the retry writes the entry atomically; the retry preserves other writers' entries.
21. `TestRetryHealsTheMissingPointerLine` — the retry heals the missing pointer line.
22. `TestInvalidPublishPolicyIsRefused` — the caller-chosen `--publish` policy (`never|manual|deferred|immediate`, required) travels with the entry; an invalid value is refused.
23. `TestIndexRowCarriesStampSourceAndSize` — index rows are derived from the stored entries: session/entry pointers, stamps, sources, sizes — never recopied narratives.
24. `TestIndexMaxDefaultTwentyAndZeroPrintsAll` — every listing takes `--max` (default 20, 0 prints all).
25. `TestIndexPrintsMORELineWithRemedy` — index prints one `MORE` line with its remedy.
26. `TestCoverageCarriesTheTotalWhenCapped` — the count is never capped and the `INDEX OK` line carries the total whether or not entries were elided.
27. `TestReceiptReportsStampSourceSizeAndPublish` — `receipt` names stamp, source pointers, size and the `persisted=true published=false publish=<policy>` split.
28. `TestLifecycleVerbsStayRefused` — there is deliberately no seal/consume/delete/grade/consolidate/wake/rollup/retention verb; naming one on the command line is exit 2, unknown subcommand.
29. `TestAnUnreadableLogRefusesTheAppendAndWritesNothing` — a `log.jsonl` that exists and cannot be read is not a store with no source: an append that would inherit the session's pointer refuses at exit 2 naming the log, and writes no entry and no pointer line (skipped on windows, as root, and wherever a 0200 file stays readable).
30. `TestAMalformedOpenRecordRefusesTheAppendAndWritesNothing` — an open record for the session that does not decode is corrupt provenance and never reads as none: the append refuses at exit 2 naming the log and writes nothing; another session's malformed line does not block this one.

31. `TestReadCommandsRefuseMissingStoreAndSession` — absent inputs refuse, while existing empty stores and sessions succeed.
32. `TestFlatReadersRefuseCorruptAndAmbiguousHeadings` — invalid stamps, invalid identifiers and duplicate entry headings refuse.
33. `TestFlatReadersKeepUnstructuredProseAndMissingEntriesDistinct` — ordinary prose is preserved without inventing entries.
