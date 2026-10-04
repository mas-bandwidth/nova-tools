----------------------------- MODULE CairnStore -----------------------------
\* The cairn store: session records, atomic entry files, append-only event log,
\* index, coverage ledger, and publication states.
\* Models internal/cairn/cairn.go (:1-31, :444-517, :551-668, :818-898)
\* and docs/SPEC-CAIRN.md ("The four verbs", :24-124).
\*
\* THE STATE.
\*   sessions           the store's nested sessions (sessions/<session>.md)
\*   flatFiles          the one-markdown-file-per-session shape read as it
\*                      stands (<store>/<session>.md)
\*   entries            the entries (entries/<session>/<id>.json, one file per
\*                      entry, the source of truth)
\*   log                the append-only log.jsonl event log
\*   index              the index built over stored entries and log
\*   coverage           the coverage ledger tracking session count, entry count,
\*                      and unindexed or unlogged gaps
\*   written            per entry: written to storage
\*   fsynced            per entry: fsynced to media
\*   logged             per entry: recorded in log.jsonl
\*   indexed            per entry: present in the index
\*   published          per entry: published to remote
\*   persisted          per entry: durability reported to caller
\*   reported           per entry: append operation result returned
\*   writtenText        ghost variable: initial prose text written per entry
\*   crashedAfterEntry  ghost variable: crash occurred between entry fsync
\*                      and log append
\*
\* THE ACTIONS.
\*   Open                       starts or re-starts a session idempotently;
\*                              never touches another session (cairn.go:444-447)
\*   Append                     stores exact prose under a stable id with a
\*                              clock stamp; a retry of the same request is
\*                              Duplicate=true and no second entry; the same id
\*                              with different prose is a conflict, never an
\*                              overwrite; persisted=true before success is
\*                              reported, published separately (cairn.go:551-562)
\*   IndexCoverage              bounded read over the log and store entries;
\*                              builds entry index and updates coverage ledger
\*                              (cairn.go:818-898)
\*   CrashAfterEntryBeforeLog   crash after entry file is written and fsynced,
\*                              before log.jsonl append (cairn.go:644-664)
\*   CrashAfterLogBeforeIndex   crash after log.jsonl append, before indexing
\*                              (cairn.go:664-668)
\*   Publish                    remote publication succeeds for a persisted
\*                              entry (cairn.go:8-10, :554-556)
\*   RemoteDown                 remote publication fails; local persistence
\*                              still reported (cairn.go:8-10, :554-556)
\*
\* THE INVARIANTS AND LIVENESS.
\*   NoOverwrite: an entry's prose never changes once written; same id with
\*     different text is refused (SPEC-CAIRN.md:87-88, cairn.go:647-652).
\*   DuplicateIsOne: a retried append adds no entry
\*     (SPEC-CAIRN.md:84-85, cairn.go:551-553).
\*   DurableBeforeReported: success is reported only after the entry file is
\*     fsynced; persisted=true never precedes the write (cairn.go:8-9,
\*     SPEC-CAIRN.md:89-90).
\*   PublishedImpliesPersisted: remote publication requires local persistence,
\*     and persistence never implies publication (cairn.go:9-10,
\*     SPEC-CAIRN.md:89-92).
\*   IndexCoversLog: every logged entry is in the index or the coverage ledger
\*     names the gap (cairn.go:1-3, :19, :876-898).
\*   OpenIdempotent: a second open changes nothing (cairn.go:444-447, :477-495,
\*     SPEC-CAIRN.md:39-42).
\*   Liveness: after a crash between entry and log, the next index run reports
\*     the gap rather than losing the entry silently (cairn.go:27-31, :876-898).
\*
\* REVERSED WITNESSES.
\*   BrokenOverwrite: same id with different prose replaces existing entry
\*     (NoOverwrite fails).
\*   BrokenReportBeforeFsync: success is reported before entry file is fsynced
\*     (DurableBeforeReported fails).
\*   BrokenPublishedWithoutPersisted: entry is published without local
\*     persistence (PublishedImpliesPersisted fails).

EXTENDS Naturals, FiniteSets

LOCAL Seq == INSTANCE Sequences

CONSTANTS Sessions, EntryIDs, Texts, FlatSessions, Broken

None == "none"
Policies == {"immediate", "manual"}
Sources == {"src1", "none"}

VARIABLES
  sessions,           \* nested sessions (sessions/<session>.md)
  flatFiles,          \* flat markdown files (<store>/<session>.md)
  entries,            \* entries (entries/<session>/<id>.json)
  log,                \* append-only log.jsonl event sequence
  index,              \* index built over entries and log
  coverage,           \* coverage ledger [sessions, entries, gaps]
  written,            \* written flag per entry
  fsynced,            \* fsynced flag per entry
  logged,             \* logged flag per entry
  indexed,            \* indexed flag per entry
  published,          \* published flag per entry
  persisted,          \* persisted flag per entry
  reported,           \* reported flag per entry
  writtenText,        \* initial prose text written per entry
  crashedAfterEntry   \* crash occurred between entry fsync and log append

vars == <<sessions, flatFiles, entries, log, index, coverage,
          written, fsynced, logged, indexed, published,
          persisted, reported, writtenText, crashedAfterEntry>>

TypeOK ==
    /\ sessions \in [Sessions -> [open: BOOLEAN, pointers: SUBSET EntryIDs,
                                 source: Sources \cup {None}, publish: Policies \cup {None}]]
    /\ flatFiles \in [Sessions -> [exists: BOOLEAN, entries: [EntryIDs -> Texts \cup {None}]]]
    /\ entries \in [Sessions \X EntryIDs -> Texts \cup {None}]
    /\ index \in SUBSET (Sessions \X EntryIDs)
    /\ coverage \in [sessions: SUBSET Sessions,
                     entries: SUBSET (Sessions \X EntryIDs),
                     gaps: SUBSET (Sessions \X EntryIDs)]
    /\ written \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ fsynced \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ logged \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ indexed \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ published \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ persisted \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ reported \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ writtenText \in [Sessions \X EntryIDs -> Texts \cup {None}]
    /\ crashedAfterEntry \in [Sessions \X EntryIDs -> BOOLEAN]

Init ==
    /\ sessions = [s \in Sessions |-> [open |-> FALSE, pointers |-> {}, source |-> None, publish |-> None]]
    /\ flatFiles = [s \in Sessions |-> [exists |-> s \in FlatSessions, entries |-> [e \in EntryIDs |-> None]]]
    /\ entries = [s \in Sessions, e \in EntryIDs |-> None]
    /\ log = << >>
    /\ index = {}
    /\ coverage = [sessions |-> FlatSessions, entries |-> {}, gaps |-> {}]
    /\ written = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ fsynced = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ logged = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ indexed = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ published = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ persisted = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ reported = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ writtenText = [s \in Sessions, e \in EntryIDs |-> None]
    /\ crashedAfterEntry = [s \in Sessions, e \in EntryIDs |-> FALSE]

\* Open: starts or re-starts a session idempotently; never touches another session (cairn.go:444-517).
Open(s, pub, src) ==
    IF s \in FlatSessions
    THEN UNCHANGED vars
    ELSE IF sessions[s].open
         THEN UNCHANGED vars
         ELSE /\ sessions' = [sessions EXCEPT ![s] = [open |-> TRUE, pointers |-> {}, source |-> src, publish |-> pub]]
              /\ log' = Seq!Append(log, [event |-> "open", session |-> s, entry |-> None, text |-> None])
              /\ coverage' = [coverage EXCEPT !.sessions = coverage.sessions \cup {s}]
              /\ UNCHANGED <<flatFiles, entries, index, written, fsynced, logged, indexed,
                             published, persisted, reported, writtenText, crashedAfterEntry>>

\* Append: stores exact prose under stable id; retry is Duplicate=true; conflicting prose refused;
\* persisted=true before success reported, published separately (cairn.go:551-668).
Append(s, e, text) ==
    /\ \/ sessions[s].open
       \/ flatFiles[s].exists
    /\ IF flatFiles[s].exists /\ ~sessions[s].open
       THEN IF flatFiles[s].entries[e] /= None
            THEN IF flatFiles[s].entries[e] = text
                 THEN /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                      /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                      /\ UNCHANGED <<sessions, flatFiles, entries, log, index, coverage,
                                     written, fsynced, logged, indexed, published,
                                     writtenText, crashedAfterEntry>>
                 ELSE IF Broken = "BrokenOverwrite"
                      THEN /\ flatFiles' = [flatFiles EXCEPT ![s].entries[e] = text]
                           /\ UNCHANGED <<sessions, entries, log, index, coverage,
                                          written, fsynced, logged, indexed, published,
                                          persisted, reported, writtenText, crashedAfterEntry>>
                      ELSE UNCHANGED vars
            ELSE /\ flatFiles' = [flatFiles EXCEPT ![s].entries[e] = text]
                 /\ written' = [written EXCEPT ![s, e] = TRUE]
                 /\ fsynced' = [fsynced EXCEPT ![s, e] = TRUE]
                 /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                 /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                 /\ writtenText' = [writtenText EXCEPT ![s, e] = text]
                 /\ UNCHANGED <<sessions, entries, log, index, coverage, logged, indexed,
                                published, crashedAfterEntry>>
       ELSE IF entries[s, e] /= None
            THEN IF entries[s, e] = text
                 THEN /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                      /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                      /\ sessions' = [sessions EXCEPT ![s].pointers = sessions[s].pointers \cup {e}]
                      /\ UNCHANGED <<flatFiles, entries, log, index, coverage,
                                     written, fsynced, logged, indexed, published,
                                     writtenText, crashedAfterEntry>>
                 ELSE IF Broken = "BrokenOverwrite"
                      THEN /\ entries' = [entries EXCEPT ![s, e] = text]
                           /\ UNCHANGED <<sessions, flatFiles, log, index, coverage,
                                          written, fsynced, logged, indexed, published,
                                          persisted, reported, writtenText, crashedAfterEntry>>
                      ELSE UNCHANGED vars
            ELSE /\ entries' = [entries EXCEPT ![s, e] = text]
                 /\ writtenText' = [writtenText EXCEPT ![s, e] = text]
                 /\ written' = [written EXCEPT ![s, e] = TRUE]
                 /\ IF Broken = "BrokenReportBeforeFsync"
                    THEN /\ fsynced' = [fsynced EXCEPT ![s, e] = FALSE]
                         /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                    ELSE /\ fsynced' = [fsynced EXCEPT ![s, e] = TRUE]
                         /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                 /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                 /\ sessions' = [sessions EXCEPT ![s].pointers = sessions[s].pointers \cup {e}]
                 /\ log' = Seq!Append(log, [event |-> "append", session |-> s, entry |-> e, text |-> text])
                 /\ logged' = [logged EXCEPT ![s, e] = TRUE]
                 /\ coverage' = [coverage EXCEPT !.gaps = coverage.gaps \cup {<<s, e>>}]
                 /\ UNCHANGED <<flatFiles, index, indexed, published, crashedAfterEntry>>

\* CrashAfterEntryBeforeLog: entry file is written and fsynced, but crash precedes log append.
\* The caller never observes success (no Persisted/Reported); the crashedAfterEntry ghost
\* variable marks the gap so IndexCoverage reports it (cairn.go:644-667).
CrashAfterEntryBeforeLog(s, e, text) ==
    /\ sessions[s].open
    /\ entries[s, e] = None
    /\ entries' = [entries EXCEPT ![s, e] = text]
    /\ writtenText' = [writtenText EXCEPT ![s, e] = text]
    /\ written' = [written EXCEPT ![s, e] = TRUE]
    /\ fsynced' = [fsynced EXCEPT ![s, e] = TRUE]
    /\ sessions' = [sessions EXCEPT ![s].pointers = sessions[s].pointers \cup {e}]
    /\ crashedAfterEntry' = [crashedAfterEntry EXCEPT ![s, e] = TRUE]
    /\ UNCHANGED <<flatFiles, log, index, coverage, logged, indexed, published,
                   persisted, reported>>

\* CrashAfterLogBeforeIndex: entry logged, but crash precedes index / coverage processing.
CrashAfterLogBeforeIndex(s, e) ==
    /\ logged[s, e]
    /\ ~indexed[s, e]
    /\ coverage' = [coverage EXCEPT !.gaps = coverage.gaps \cup {<<s, e>>}]
    /\ UNCHANGED <<sessions, flatFiles, entries, log, index, written, fsynced,
                   logged, indexed, published, persisted, reported,
                   writtenText, crashedAfterEntry>>

\* IndexCoverage: bounded read over log and store; builds entry index and coverage ledger (cairn.go:818-898).
IndexCoverage ==
    LET nestedEntries == {<<s, e>> \in Sessions \X EntryIDs : logged[s, e]}
        flatEntries == {<<s, e>> \in Sessions \X EntryIDs : flatFiles[s].entries[e] /= None}
        newIndex == index \cup nestedEntries \cup flatEntries
        crashedGaps == {<<s, e>> \in Sessions \X EntryIDs : fsynced[s, e] /\ ~logged[s, e]}
        loggedGaps == {<<s, e>> \in Sessions \X EntryIDs : logged[s, e] /\ <<s, e>> \notin newIndex}
        newGaps == (coverage.gaps \cup crashedGaps \cup loggedGaps) \ newIndex
    IN
        /\ index' = newIndex
        /\ indexed' = [s \in Sessions, e \in EntryIDs |-> <<s, e>> \in newIndex]
        /\ coverage' = [
               sessions |-> {s \in Sessions : sessions[s].open \/ flatFiles[s].exists},
               entries  |-> newIndex,
               gaps     |-> newGaps \cup crashedGaps
           ]
        /\ UNCHANGED <<sessions, flatFiles, entries, log, written, fsynced, logged,
                       published, persisted, reported, writtenText, crashedAfterEntry>>

\* Publish: publishes a locally persisted entry to remote.
Publish(s, e) ==
    /\ IF Broken = "BrokenPublishedWithoutPersisted"
       THEN ~published[s, e] /\ ~persisted[s, e]
       ELSE ~published[s, e] /\ persisted[s, e]
    /\ published' = [published EXCEPT ![s, e] = TRUE]
    /\ UNCHANGED <<sessions, flatFiles, entries, log, index, coverage,
                   written, fsynced, logged, indexed, persisted, reported,
                   writtenText, crashedAfterEntry>>

\* RemoteDown: publish fails; local persistence still reported (cairn.go:8-10, :554-556).
RemoteDown(s, e) ==
    /\ persisted[s, e]
    /\ ~published[s, e]
    /\ UNCHANGED vars

Next ==
    \/ \E s \in Sessions, pub \in Policies, src \in Sources : Open(s, pub, src)
    \/ \E s \in Sessions, e \in EntryIDs, text \in Texts : Append(s, e, text)
    \/ \E s \in Sessions, e \in EntryIDs, text \in Texts : CrashAfterEntryBeforeLog(s, e, text)
    \/ \E s \in Sessions, e \in EntryIDs : CrashAfterLogBeforeIndex(s, e)
    \/ IndexCoverage
    \/ \E s \in Sessions, e \in EntryIDs : Publish(s, e)
    \/ \E s \in Sessions, e \in EntryIDs : RemoteDown(s, e)

Spec == Init /\ [][Next]_vars /\ WF_vars(IndexCoverage)

\* NoOverwrite: an entry's prose never changes once written (SPEC-CAIRN.md:87-88, cairn.go:647-652).
NoOverwrite ==
    \A s \in Sessions, e \in EntryIDs :
        /\ (entries[s, e] /= None => entries[s, e] = writtenText[s, e])
        /\ (flatFiles[s].entries[e] /= None => flatFiles[s].entries[e] = writtenText[s, e])

\* DuplicateIsOne: a retried append adds no entry (SPEC-CAIRN.md:84-85, cairn.go:551-553).
DuplicateIsOne ==
    \A s \in Sessions, e \in EntryIDs :
        Cardinality({i \in DOMAIN log : log[i].event = "append" /\ log[i].session = s /\ log[i].entry = e}) <= 1

\* DurableBeforeReported: success is reported only after the entry file is fsynced (cairn.go:8-9, SPEC-CAIRN.md:89-90).
DurableBeforeReported ==
    \A s \in Sessions, e \in EntryIDs :
        persisted[s, e] => fsynced[s, e]

\* PublishedImpliesPersisted: remote publication requires local persistence (cairn.go:9-10, SPEC-CAIRN.md:89-92).
PublishedImpliesPersisted ==
    \A s \in Sessions, e \in EntryIDs :
        published[s, e] => persisted[s, e]

\* IndexCoversLog: every logged entry is in the index or coverage names the gap (cairn.go:1-3, :19, :876-898).
IndexCoversLog ==
    \A s \in Sessions, e \in EntryIDs :
        logged[s, e] => (<<s, e>> \in index \/ <<s, e>> \in coverage.gaps)

\* OpenIdempotent: a second open changes nothing (cairn.go:444-447, :477-495, SPEC-CAIRN.md:39-42).
OpenIdempotent ==
    \A s \in Sessions :
        Cardinality({i \in DOMAIN log : log[i].event = "open" /\ log[i].session = s}) <= 1

\* Liveness: after a crash between entry and log, the next index run reports the gap rather than losing the entry silently (cairn.go:27-31, :876-898).
Liveness ==
    \A s \in Sessions, e \in EntryIDs :
        (crashedAfterEntry[s, e] /\ ~logged[s, e]) ~> (<<s, e>> \in coverage.gaps)

=============================================================================
