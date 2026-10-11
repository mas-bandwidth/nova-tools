----------------------------- MODULE CairnStore -----------------------------
\* The cairn store: session records, atomic entry files, append-only event log,
\* and the index and coverage count read back from them.
\* Models internal/cairn/cairn.go (:1-31, :497-582, :632-733, :891-959)
\* and docs/SPEC-CAIRN.md ("The four verbs", :24-124).
\*
\* THE STATE.
\*   sessions           the store's nested sessions (sessions/<session>.md)
\*   flatFiles          the one-markdown-file-per-session shape read as it
\*                      stands (<store>/<session>.md)
\*   entries            the entries (entries/<session>/<id>.json, one file per
\*                      entry, the source of truth)
\*   log                the append-only log.jsonl event log
\*   index              the index built over stored entries
\*   coverage           the coverage ledger: the session and entry counts the
\*                      index reports (Ledger holds counts only, no gaps)
\*   written            per entry: written to storage
\*   fsynced            per entry: fsynced to media
\*   logged             per entry: recorded in log.jsonl
\*   indexed            per entry: present in the index
\*   persisted          per entry: durability reported to caller
\*   reported           per entry: append operation result returned
\*   writtenText        ghost variable: initial prose text written per entry
\*
\* THE ACTIONS.
\*   Open                       starts or re-starts a session idempotently; a
\*                              re-open naming another publish policy is
\*                              refused, and one whose initial open was
\*                              interrupted before logging heals the open
\*                              (cairn.go:497-582); never touches another
\*                              session
\*   OpenCrash                  crash after the session file is written, before
\*                              the open record reaches the log
\*                              (cairn.go:526-541)
\*   Append                     stores exact prose under a stable id with a
\*                              clock stamp; a retry of the same request is
\*                              Duplicate=true and no second entry; the same id
\*                              with different prose is a conflict, never an
\*                              overwrite; persisted=true before success is
\*                              reported (cairn.go:632-733)
\*   IndexCoverage              bounded read over the store entries; builds the
\*                              entry index and the coverage counts
\*                              (cairn.go:891-959)
\*
\* The code implements no transport: an append reports published=false and the
\* publish policy travels with the entry, so this module has no Publish or
\* RemoteDown action and no publication invariant (cairn.go:51-54).
\*
\* THE INVARIANTS.
\*   NoOverwrite: an entry's prose never changes once written; same id with
\*     different text is refused (SPEC-CAIRN.md:87-88, cairn.go:764-766).
\*   DuplicateIsOne: a retried append adds no entry
\*     (SPEC-CAIRN.md:84-85, cairn.go:680-683).
\*   DurableBeforeReported: success is reported only after the entry file is
\*     fsynced; persisted=true never precedes the write (cairn.go:128-131,
\*     SPEC-CAIRN.md:89-90).
\*   OpenIdempotent: a second open changes nothing (cairn.go:493-500,
\*     SPEC-CAIRN.md:39-42).
\*   OneOpenPolicy: a session is opened with one publish policy; a re-open
\*     naming another is refused (cairn.go:542-558).
\*
\* REVERSED WITNESSES.
\*   BrokenOverwrite: same id with different prose replaces existing entry
\*     (NoOverwrite fails).
\*   BrokenReportBeforeFsync: success is reported before entry file is fsynced
\*     (DurableBeforeReported fails).
\*   BrokenReopenOther: a re-open naming another publish policy is accepted and
\*     changes the session's policy (OneOpenPolicy fails).

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
  index,              \* index built over entries
  coverage,           \* coverage ledger [sessions, entries]
  written,            \* written flag per entry
  fsynced,            \* fsynced flag per entry
  logged,             \* logged flag per entry
  indexed,            \* indexed flag per entry
  persisted,          \* persisted flag per entry
  reported,           \* reported flag per entry
  writtenText         \* initial prose text written per entry

vars == <<sessions, flatFiles, entries, log, index, coverage,
          written, fsynced, logged, indexed, persisted,
          reported, writtenText>>

TypeOK ==
    /\ sessions \in [Sessions -> [open: BOOLEAN, pointers: SUBSET EntryIDs,
                                 source: Sources \cup {None}, publish: Policies \cup {None}]]
    /\ flatFiles \in [Sessions -> [exists: BOOLEAN, entries: [EntryIDs -> Texts \cup {None}]]]
    /\ entries \in [Sessions \X EntryIDs -> Texts \cup {None}]
    /\ index \in SUBSET (Sessions \X EntryIDs)
    /\ coverage \in [sessions: SUBSET Sessions,
                     entries: SUBSET (Sessions \X EntryIDs)]
    /\ written \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ fsynced \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ logged \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ indexed \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ persisted \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ reported \in [Sessions \X EntryIDs -> BOOLEAN]
    /\ writtenText \in [Sessions \X EntryIDs -> Texts \cup {None}]

Init ==
    /\ sessions = [s \in Sessions |-> [open |-> FALSE, pointers |-> {}, source |-> None, publish |-> None]]
    /\ flatFiles = [s \in Sessions |-> [exists |-> s \in FlatSessions, entries |-> [e \in EntryIDs |-> None]]]
    /\ entries = [s \in Sessions, e \in EntryIDs |-> None]
    /\ log = << >>
    /\ index = {}
    /\ coverage = [sessions |-> FlatSessions, entries |-> {}]
    /\ written = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ fsynced = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ logged = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ indexed = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ persisted = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ reported = [s \in Sessions, e \in EntryIDs |-> FALSE]
    /\ writtenText = [s \in Sessions, e \in EntryIDs |-> None]

\* openLogged: the session's open record reached log.jsonl.
openLogged(s) == \E i \in DOMAIN log : log[i].event = "open" /\ log[i].session = s

\* Open: starts a session, heals an interrupted open, or refuses a re-open that
\* names another publish policy (cairn.go:497-582).
Open(s, pub, src) ==
    IF s \in FlatSessions
    THEN UNCHANGED vars
    ELSE IF ~sessions[s].open
         THEN /\ sessions' = [sessions EXCEPT ![s] = [open |-> TRUE, pointers |-> {}, source |-> src, publish |-> pub]]
              /\ log' = Seq!Append(log, [event |-> "open", session |-> s, entry |-> None, text |-> None, publish |-> pub])
              /\ coverage' = [coverage EXCEPT !.sessions = coverage.sessions \cup {s}]
              /\ UNCHANGED <<flatFiles, entries, index, written, fsynced, logged, indexed,
                             persisted, reported, writtenText>>
         ELSE IF ~openLogged(s)
              THEN \* heal: the file exists but the open record never logged
                   /\ log' = Seq!Append(log, [event |-> "open", session |-> s, entry |-> None, text |-> None, publish |-> sessions[s].publish])
                   /\ UNCHANGED <<sessions, flatFiles, entries, index, coverage, written, fsynced,
                                  logged, indexed, persisted, reported, writtenText>>
              ELSE IF pub /= sessions[s].publish
                   THEN IF Broken = "reopenother"
                        THEN /\ sessions' = [sessions EXCEPT ![s].publish = pub]
                             /\ log' = Seq!Append(log, [event |-> "open", session |-> s, entry |-> None, text |-> None, publish |-> pub])
                             /\ UNCHANGED <<flatFiles, entries, index, coverage, written, fsynced,
                                            logged, indexed, persisted, reported, writtenText>>
                        ELSE UNCHANGED vars
                   ELSE UNCHANGED vars

\* OpenCrash: the session file is written, then the crash precedes the open
\* log record; a later Open heals it (cairn.go:526-541).
OpenCrash(s, pub, src) ==
    /\ s \notin FlatSessions
    /\ ~sessions[s].open
    /\ sessions' = [sessions EXCEPT ![s] = [open |-> TRUE, pointers |-> {}, source |-> src, publish |-> pub]]
    /\ UNCHANGED <<flatFiles, entries, log, index, coverage, written, fsynced,
                   logged, indexed, persisted, reported, writtenText>>

\* Append: stores exact prose under stable id; retry is Duplicate=true; conflicting prose refused;
\* persisted=true before success reported (cairn.go:632-733).
Append(s, e, text) ==
    /\ \/ sessions[s].open
       \/ flatFiles[s].exists
    /\ IF flatFiles[s].exists /\ ~sessions[s].open
       THEN IF flatFiles[s].entries[e] /= None
            THEN IF flatFiles[s].entries[e] = text
                 THEN /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                      /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                      /\ UNCHANGED <<sessions, flatFiles, entries, log, index, coverage,
                                     written, fsynced, logged, indexed, writtenText>>
                 ELSE IF Broken = "BrokenOverwrite"
                      THEN /\ flatFiles' = [flatFiles EXCEPT ![s].entries[e] = text]
                           /\ UNCHANGED <<sessions, entries, log, index, coverage,
                                          written, fsynced, logged, indexed,
                                          persisted, reported, writtenText>>
                      ELSE UNCHANGED vars
            ELSE /\ flatFiles' = [flatFiles EXCEPT ![s].entries[e] = text]
                 /\ written' = [written EXCEPT ![s, e] = TRUE]
                 /\ fsynced' = [fsynced EXCEPT ![s, e] = TRUE]
                 /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                 /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                 /\ writtenText' = [writtenText EXCEPT ![s, e] = text]
                 /\ UNCHANGED <<sessions, entries, log, index, coverage, logged, indexed>>
       ELSE IF entries[s, e] /= None
            THEN IF entries[s, e] = text
                 THEN /\ persisted' = [persisted EXCEPT ![s, e] = TRUE]
                      /\ reported' = [reported EXCEPT ![s, e] = TRUE]
                      /\ sessions' = [sessions EXCEPT ![s].pointers = sessions[s].pointers \cup {e}]
                      /\ UNCHANGED <<flatFiles, entries, log, index, coverage,
                                     written, fsynced, logged, indexed, writtenText>>
                 ELSE IF Broken = "BrokenOverwrite"
                      THEN /\ entries' = [entries EXCEPT ![s, e] = text]
                           /\ UNCHANGED <<sessions, flatFiles, log, index, coverage,
                                          written, fsynced, logged, indexed,
                                          persisted, reported, writtenText>>
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
                 /\ UNCHANGED <<flatFiles, index, indexed, coverage>>

\* IndexCoverage: bounded read over the store entries; builds the entry index
\* and the coverage counts (cairn.go:891-959).
IndexCoverage ==
    LET nestedEntries == {<<s, e>> \in Sessions \X EntryIDs : entries[s, e] /= None}
        flatEntries == {<<s, e>> \in Sessions \X EntryIDs : flatFiles[s].entries[e] /= None}
        newIndex == nestedEntries \cup flatEntries
    IN
        /\ index' = newIndex
        /\ indexed' = [s \in Sessions, e \in EntryIDs |-> <<s, e>> \in newIndex]
        /\ coverage' = [
               sessions |-> {s \in Sessions : sessions[s].open \/ flatFiles[s].exists},
               entries  |-> newIndex
           ]
        /\ UNCHANGED <<sessions, flatFiles, entries, log, written, fsynced, logged,
                       persisted, reported, writtenText>>

Next ==
    \/ \E s \in Sessions, pub \in Policies, src \in Sources : Open(s, pub, src)
    \/ \E s \in Sessions, pub \in Policies, src \in Sources : OpenCrash(s, pub, src)
    \/ \E s \in Sessions, e \in EntryIDs, text \in Texts : Append(s, e, text)
    \/ IndexCoverage

Spec == Init /\ [][Next]_vars /\ WF_vars(IndexCoverage)

\* NoOverwrite: an entry's prose never changes once written (SPEC-CAIRN.md:87-88, cairn.go:764-766).
NoOverwrite ==
    \A s \in Sessions, e \in EntryIDs :
        /\ (entries[s, e] /= None => entries[s, e] = writtenText[s, e])
        /\ (flatFiles[s].entries[e] /= None => flatFiles[s].entries[e] = writtenText[s, e])

\* DuplicateIsOne: a retried append adds no entry (SPEC-CAIRN.md:84-85, cairn.go:680-683).
DuplicateIsOne ==
    \A s \in Sessions, e \in EntryIDs :
        Cardinality({i \in DOMAIN log : log[i].event = "append" /\ log[i].session = s /\ log[i].entry = e}) <= 1

\* DurableBeforeReported: success is reported only after the entry file is fsynced (cairn.go:128-131, SPEC-CAIRN.md:89-90).
DurableBeforeReported ==
    \A s \in Sessions, e \in EntryIDs :
        persisted[s, e] => fsynced[s, e]

\* OpenIdempotent: a second open changes nothing (cairn.go:493-500, SPEC-CAIRN.md:39-42).
OpenIdempotent ==
    \A s \in Sessions :
        Cardinality({i \in DOMAIN log : log[i].event = "open" /\ log[i].session = s}) <= 1

\* OneOpenPolicy: a session is opened with one publish policy; a re-open naming
\* another is refused (cairn.go:542-558).
OneOpenPolicy ==
    \A s \in Sessions, i \in DOMAIN log, j \in DOMAIN log :
        ((log[i].event = "open" /\ log[i].session = s)
         /\ (log[j].event = "open" /\ log[j].session = s))
        => log[i].publish = log[j].publish

=============================================================================
