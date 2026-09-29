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
files; there is no default store, no environment variable and no discovery.
The session identifier is stable: retries and recoveries address the same
record by this name, and concurrent records coexist untouched by each other.
Re-opening an open session is a no-op. The record is created in the store's
shape (below): `sessions/<id>.md` with `entries/<id>/<entry>.json` and one
append-only `log.jsonl` in the tool's own shape, `<id>.md` directly under the
store in a bench store. The session file's header is convention only and is
never parsed, so alternate directory/header conventions survive: in the tool's
own shape the entry files are the source of truth and the readable record links
to them.

**The store's shape is read from its contents, once, and never imposed.** A
store is in one of three shapes, decided by one function from what the
directory holds:

- **bench**: at least one top-level `<id>.md` session file and no `sessions/`,
  `entries/` or `log.jsonl`. This is how a friend appending by hand keeps a
  record.
- **own**: the tool's own layout, `sessions/<id>.md`, `entries/<id>/<entry>.json`
  and `log.jsonl`. An empty directory and a directory that does not exist yet
  are also this shape, so a new store gets the tool's own layout.
- **mixed**: a top-level `<id>.md` together with any of `sessions/`, `entries/`
  or `log.jsonl`. Every verb refuses it at exit 2 with one line naming the
  operation, the cause (the store holds two shapes), the paths found of each
  shape (the top-level files as a bounded list, then "and N more") and the next
  action in words: keep one shape by moving the other shape's paths out of the
  store, then run the same command again. Nothing is written, and the tool
  moves and deletes nothing: it prints no command that does.

**Mixed versions.** A store is written by one version family. A `nova-cairn`
older than this one, run on a bench store, writes the own shape beside the
records when `open` is called for a new session; this version then refuses the
store as mixed until those paths are moved out. Every writer of a shared store
upgrades together.

A top-level `README.md`, in any case, is documentation and never a session file
in either shape: it counts for no shape, `index` and the coverage ledger do not
list it, and `README` (in any case) is refused as a session id by every verb
with a message naming the reserved name. Any other top-level `<id>.md` beside
`sessions/`, `entries/` or `log.jsonl` is a mixed store.

A symlink named `sessions`, `entries` or `log.jsonl` counts as an own-shape
marker whatever it points at, and a symlink named `<id>.md` counts as a session
file. A session record is a regular file, or a symlink that resolves to one
inside the store; the tool never reads or appends a session record that lies
outside the directory it was given. The check is made on the open file, not
only on the path: a record link re-pointed between the check and the open
is refused before anything is read or written through it. (Only the record is
confined. A symlinked `log.jsonl` or `entries/` is an own-shape marker and is
followed.) The answer is the same however `--store` is spelled, relative or
absolute. Where a verb finds anything else at a session's record path (a
directory, a symlink to a directory, to nothing or to somewhere outside the
store, a device), it refuses at exit 2 naming the path, what is there and the
next action (move or remove it, or choose another session id); `open` never
reports success over it, and no verb answers "no such session" for it.

**A defect in one session's record is that session's.** Two kinds, and each
has its own answer.

- The record's path holds no record (a directory, a dangling or outside link,
  a device): the verbs addressed to that session (`open`,
  `append`, `receipt`, `index --session`) refuse at exit 2.
- The record is a file but its content is damaged (an invalid heading, a
  duplicate entry): `index --session` prints that session's flagged row and exits
  1, `receipt` for it refuses at exit 2, and `open` and `append` still work,
  because they read the record only to find their own entry and add a section
  after it; they rewrite nothing.

In both, `index` over a bench store reports the session as one flagged row,
`INDEX FLAGGED session=<id> cause=<why>`, while every other session is listed;
`index` prints all its rows and the coverage line first and then exits 1 when any
row was flagged. In the tool's own shape `index` reads the entry files, a damaged
one refuses the whole call at exit 2, and a session file that is a dangling link
is met by the verbs addressed to it, not by a flagged row. Only a store-level
condition (two shapes, a store path that is not a directory, an unreadable
directory) refuses every verb.

A directory named `<id>.md` is not a session file, and neither is `<id>.MD`:
`index` and the coverage ledger skip them and flag nothing. A verb addressed to
the directory refuses, as above. Two links inside the store to one file are two
sessions over one record, and its entries are listed under both.

Only the exact name `<id>.md` is a session file: `s1.MD` is not one on any
file system, and `index` and the coverage ledger skip it. On a disk that folds
letter case, a verb addressing session `s1` (or `S1`) that would reach an
existing `s1.MD` (or `s1.md`) through the fold refuses at exit 2 naming both
names, and writes nothing through it; on a disk that keeps case they are two
different names.

On a bench store `open` creates `<store>/<id>.md` when none exists and is a
no-op when one does. The new file holds a short header, fsynced before success
is reported: a `# Cairn <first 8 characters of the id>` title line, a line
naming the full session id and the open stamp in RFC 3339 UTC, and a `Source:`
line when `--source` is given (escaped to one line, so a pointer never forms a
section heading). It creates no `sessions/`, `entries/` or `log.jsonl`, and the
flat format stores no publication policy, so `--publish` is validated and not
written. `append` lands a dated `## <stamp> — <entry>` section at the end of the
file in the file's own shape, one blank line between sections, the words
byte-for-byte beneath the heading. An append to a session with no file refuses
with the remedy verb, and appends only after that `open`. Nothing appears
beside the file: no `entries/`, no `log.jsonl`, no index. The duplicate and
conflict rules below read the section instead of an entry file. `index` and
`receipt` read the dated sections; they see a session as soon as `open` has
made its file. Their byte counts measure the whitespace-trimmed section body,
matching duplicate detection; they do not reconstruct the original append's
trailing newlines. The flat format stores no publication policy, so receipts
print `publish=unknown`, and it stores a source only in the header `open` writes:
`open --source <ptr>` prints the pointer it recorded, an `append` with no
`--source` carries it, and `receipt` and `index` report it. A section stores no
pointer of its own, so what an `append` and its receipt print is the session's
(`source=-` for a hand-kept record with no header pointer). Ordinary prose without
machine-form entry headings is not an indexed entry. Invalid stamps, invalid
entry identifiers and duplicate entry headings refuse rather than produce an
ambiguous receipt. The coverage ledger counts the session files of the store's
shape.

**A refusal names the remedy verb whole** — `open first: nova-cairn open --store
<dir> --session <id> --publish <policy>` — rather than a verb the reader must
reconstruct. The remedy quotes the caller's store and session for a POSIX shell.
Control bytes use octal decoding inside a subshell with a sentinel to preserve
trailing newlines, so the printed command stays one line and opens exactly the
named record, in the shape the store already has.

The lifecycle of a store (shape, open, append, duplicate, conflict, refusal) is
modelled in `tla/CairnStore.tla`: one shape per store, an entry id maps to one
text, and append never creates a session. The reserved name is outside the
model, whose sessions are an abstract set of ids.

**`append --store <dir> --session <id> --entry <id> (--text <words> |
--file <path|->) [--source <ptr>] --publish <policy>` files the friend's
chosen words byte-for-byte** with a real clock stamp (UTC; `--now` names an
RFC 3339 UTC replay for tests), the stable entry/session identifiers and
the source pointers, which are recorded and never opened. An append with no
`--source` carries the session's `open --source`, read back from the open
record in `log.jsonl` (in a bench store, from the header `open` wrote); a log or a bench header that exists and cannot be read, or an open record
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
`INDEX COVERAGE sessions=<n> entries=<n>` line carries the total whether
the run passed or failed. The store's directory is read once per verb, not
once per record.

**`receipt --store <dir> --session <id> --entry <id>` names what was
preserved for one entry**: its stamp, source pointers, size and the same
`persisted=true published=false publish=<policy>` split the append
reported for nested entries, so a reader never infers the remote from the local.
Flat records report `publish=unknown` because the policy was not stored.
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
12. `TestIndexAndReceiptReadFlatRecordsWithoutChangingThem` — index and receipt read the dated sections in a flat record, preserve its bytes, and create no sidecars. `TestFlatReadMetadataAndOrdering` checks ordering, metadata and the shared coverage count.
13. `TestMixedShapeStoreIsRefusedByEveryVerb` — a store holding a top-level `<id>.md` together with `sessions/`, `entries/` or `log.jsonl` is refused by `open`, `append`, `index` and `receipt` with one line naming the operation, the cause, the paths of each shape and the next action, and nothing is written. `TestMixedShapeMessageNamesThePathsOfEachShape` fixes the wording; `TestStoreShapeFromContents` fixes the shape rule.
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
26. `TestCoverageCarriesTheTotalWhenCapped` — the count is never capped and the `INDEX COVERAGE` line carries the total whether the run passed or failed.
27. `TestReceiptReportsStampSourceSizeAndPublish` — `receipt` names stamp, source pointers, size and the `persisted=true published=false publish=<policy>` split.
28. `TestLifecycleVerbsStayRefused` — there is deliberately no seal/consume/delete/grade/consolidate/wake/rollup/retention verb; naming one on the command line is exit 2, unknown subcommand.
29. `TestAnUnreadableLogRefusesTheAppendAndWritesNothing` — a `log.jsonl` that exists and cannot be read is not a store with no source: an append that would inherit the session's pointer refuses at exit 2 naming the log, and writes no entry and no pointer line (skipped on windows, as root, and wherever a 0200 file stays readable).
30. `TestAMalformedOpenRecordRefusesTheAppendAndWritesNothing` — an open record for the session that does not decode is corrupt provenance and never reads as none: the append refuses at exit 2 naming the log and writes nothing; another session's malformed line does not block this one.

31. `TestReadCommandsRefuseMissingStoreAndSession` — absent inputs refuse, while existing empty stores and sessions succeed.
32. `TestFlatReadersRefuseCorruptAndAmbiguousHeadings` — invalid stamps, invalid identifiers and duplicate entry headings refuse.
33. `TestFlatReadersKeepUnstructuredProseAndMissingEntriesDistinct` — ordinary prose is preserved without inventing entries.
34. `TestBenchStoreOpenThenAppendKeepsTheBenchShape` — on a bench store holding several sessions and none for a new one, `append` refuses with the `open` remedy, `open` creates `<id>.md` and nothing else (the exact directory listing is asserted), and the next `append` lands the dated section with no `sessions/`, `entries/` or `log.jsonl`.
35. `TestBenchOpenWritesTheHeader` — the header is the title line, the session line with the open stamp, and the `Source:` line when given; a source never forms a section heading.
36. `TestBenchOpenTwiceIsANoOp` — a second `open` changes nothing.
37. `TestBenchAppendBeforeOpenRefusesWithTheRemedy` and `TestBenchLifecycleDuplicateAndConflict` — the refusal names the remedy and writes nothing; after `open`, the same id with the same words is a duplicate and the same id with different words is a conflict.
38. `TestBenchIndexAndReceiptSeeTheOpenedSession` — `index`, `receipt` and the coverage ledger see a session as soon as `open` has made its file.
39. `TestEmptyAndAbsentStoresGetTheOwnShape` and `TestEmptyStoreOpenGetsTheOwnShapeAtTheCLI` — an empty or absent store directory gets the tool's own layout.
40. `TestBenchStoreOpenRemedyKeepsTheBenchShape` and `TestMixedShapeStoreIsRefusedAtExitTwoByEveryVerb` — the same behaviour at the command line: exit codes, one-line refusals, the listing after each verb. `TestAppendOpenRemedyOnABenchStoreCreatesOnlyTheSessionFile` runs the printed remedy on a bench store in the functional tier.
41. `TestReadmeIsNeverASessionFile`, `TestReadmeIsRefusedAsASessionIDByEveryVerb`, `TestReadmeBesideOwnMarkersAndAnotherSessionFileStaysMixed` and `TestOpenReadmeRefusesAtExitTwo` — a top-level `README.md` in any case is ignored by the shape function in both shapes, is not an indexed or counted session, and is refused as a session id at exit 2; another `<id>.md` beside own-shape markers stays a mixed store; an entry may be called README.
42. `TestNonRegularRecordPathIsRefusedByEveryVerb`, `TestIndexOfAStoreHoldingADanglingLinkRefusesNamingIt`, `TestSymlinkToARegularFileIsARecord`, `TestSymlinkedOwnShapeMarkersCountAsMarkers` and `TestOpenOverADanglingSymlinkRefusesAtExitTwo` — a directory, a symlink to a directory and a dangling symlink at a record path are refused by `open`, `append`, `index` and `receipt` naming the path and what is there; a symlink to a regular file is a record; a symlinked own-shape marker is a marker.
43. `TestBenchSourceIsRecordedInheritedAndReported` — on a bench store `open --source` prints the pointer it recorded, an `append` with no `--source` carries it, and `receipt` and `index` report it; a hand-kept record reports `source=-`; a source never forms a section.
    `TestSessionSourceOfABenchRecordIsConfined` reads the header through the same check as every read of a record, and `TestAnUnreadableBenchHeaderIsAnErrorNotNoSource` refuses a header that exists and cannot be read.
44. `TestMixedShapeMessageNamesThePathsOfEachShape` and `TestIDRefusalsNameTheRuleBroken` — the mixed-store refusal names the paths of each shape and the next action in words, and prints no `mv`, `rm` or `find`; an identifier refusal names the rule broken (empty, over 128 bytes, a directory name, `..`, whitespace, a control character, a slash) and never echoes an over-long id whole.
45. `TestCaseFoldedRecordNamesAreNotSessionFiles` — a file named `s1.MD` is not a session file: the store-wide readers skip it, and on a case-folding disk `open`, `append`, `receipt` and `index --session` refuse naming the fold and write nothing through it (on a case-keeping disk `open s1` creates `s1.md` beside it; the test probes the disk and asserts the outcome for whichever it is).
46. `TestStoreHoldingOnlyReadmeIsAnEmptyStore` and `TestConcurrentBenchOpensWriteOneHeader` — a store holding only a `README.md` is an empty store in the tool's own shape; many concurrent `open`s of one new bench session write exactly one header and leave no temporary file. `TestAppendOpenRemedyOnABenchStoreCreatesOnlyTheSessionFile` (functional tier) builds the binary and runs the printed remedy through `sh -c` with it first on `PATH`.
49. `TestSymlinkResolvingOutsideTheStoreIsRefused` and `TestSymlinkToARegularFileInsideTheStoreIsARecord` — a link that resolves outside the store is refused by every verb naming the link and its target and nothing is written through it; a link to a regular file inside the store is a record.
    `TestAppendNeverWritesOutsideTheStoreWhileTheLinkIsSwapped` re-points a record link between a file inside and a file outside while appends run, in both shapes, and the outside file is never touched; `TestAnOpenedRecordMustBeTheFileTheNameLeadsToInsideTheStore` fixes the check on the open file; `TestTheSpellingOfTheStoreDoesNotChangeTheAnswer` runs the same store named relative and absolute.
50. `TestIndexReadsTheStoreDirectoryOnce` — through a counting seam, `index` over 40 sessions reads the store directory once.
51. `TestIndexFlagsOneBadSessionAndListsTheOthers` and `TestIndexAtTheCLIFlagsOneBadSessionAmongFive` — one damaged session among five is one flagged row, the other four are listed, `index` exits 1 after printing everything, and naming the damaged session refuses at exit 2.
    `TestADamagedRecordIsFlaggedByIndexRefusedByReceiptAndStillAppendable` fixes the two kinds of damage (a path with no record refuses; a damaged file is flagged, refused by `receipt`, and still opened and appended), `TestADirectoryAndACaseTwinAreNotSessionsAndAreNotFlagged` fixes what index skips, and `TestAnOwnShapeDamagedEntryRefusesTheIndex` fixes the own shape.
52. `TestLongIDIsShownCutAtARuneBoundary` — an over-long id is never cut inside a character.
