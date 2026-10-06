------------------------------- MODULE Cairn -------------------------------
\* The cairn entry, as appenders see it: an entry is absent, then published,
\* then duplicate or conflict on a re-read. internal/cairn/cairn.go appendFlat
\* (the flat bench record, a dated section added at the end of <session>.md) and
\* the nested store's entries/<session>/<id>.json published through atomicfile
\* (CairnStore.tla models the nested shape's files, log and index; this module
\* models the one thing two appenders can do to each other, the read-decide-
\* append of the flat record). docs/SPEC-CAIRN.md; security#73 finding 1
\* (the flat append was not exclusive) and finding 4 (no model beside it).
\*
\* THE STATE.
\*   file    the record: a sequence of sections, each <<id, text>>
\*   holder  the writer holding the exclusive lock on the sibling lock file
\*           (internal/filelock), or NoWriter
\*   pc      per writer: "idle", "locked", "read", "decided", "done", "dead"
\*   want    per writer: the <<id, text>> it files (chosen at Start)
\*   seen    per writer: what its read of the record found for its id:
\*           "none", "same" (same words) or "other" (different words)
\*   res     per writer: what it reported: "none", "appended", "duplicate",
\*           "conflict"
\*   crashes how many crashes have happened (bounded by MaxCrashes)
\*
\* THE ACTIONS, one writer each, a step apiece (the read and the write are
\* separate steps, which is the whole point).
\*   Start      the writer picks the id and the words it files
\*   Lock       flock the sibling lock file (cairn.go:293); the design takes it
\*              before the read, so a second writer waits
\*   Read       os.ReadFile and findFlat (cairn.go:301-307): what the record
\*              holds for the id, as of this step
\*   Decide     found with the same words: a duplicate, nothing to add; found
\*              with other words: a conflict; absent: go on to the write
\*   Write      one atomic replace of the record with the old text and the new
\*              section at its end (atomicfile.WriteFile, cairn.go:314-316);
\*              persisted is reported only after it, then the lock is dropped
\*   Finish     report a duplicate or a conflict and drop the lock
\*   Crash      the outside event: the process dies at any step before it
\*              reported; the OS drops its flock when the file closes, so the
\*              next take finds it free. A crash after Write leaves the
\*              section and no report
\*   Retry      a dead writer starts again with the same id and words
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "flatappend"  the read-then-append of cairn.go:293-316 with no exclusive
\*                 lock: Lock takes nothing and Write needs none, so two
\*                 writers read "none" and both append, and the record holds
\*                 two sections of one id; each reports success
\* It is caught by OneSectionPerID; the design passes every invariant below.

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Writers, Ids, Texts, MaxCrashes, Broken

NoWriter == "(none)"
Sections == Ids \X Texts
Pcs == {"idle", "locked", "read", "decided", "done", "dead"}
Results == {"none", "appended", "duplicate", "conflict"}

VARIABLES file, holder, pc, want, seen, res, crashes
vars == <<file, holder, pc, want, seen, res, crashes>>

TypeOK ==
  /\ file \in Seq(Sections)
  /\ holder \in Writers \cup {NoWriter}
  /\ pc \in [Writers -> Pcs]
  /\ want \in [Writers -> Sections \cup {<<>>}]
  /\ seen \in [Writers -> {"none", "same", "other"}]
  /\ res \in [Writers -> Results]
  /\ crashes \in 0..MaxCrashes

SectionsOf(i) == {k \in 1..Len(file) : file[k][1] = i}
Holds(i, x) == \E k \in SectionsOf(i) : file[k][2] = x

Found(w) ==
  LET i == want[w][1] IN
  IF SectionsOf(i) = {} THEN "none"
  ELSE IF Holds(i, want[w][2]) THEN "same" ELSE "other"

Init ==
  /\ file = <<>>
  /\ holder = NoWriter
  /\ pc = [w \in Writers |-> "idle"]
  /\ want = [w \in Writers |-> <<>>]
  /\ seen = [w \in Writers |-> "none"]
  /\ res = [w \in Writers |-> "none"]
  /\ crashes = 0

Start(w) ==
  /\ pc[w] = "idle" /\ want[w] = <<>>
  /\ \E s \in Sections : want' = [want EXCEPT ![w] = s]
  /\ UNCHANGED <<file, holder, pc, seen, res, crashes>>

Lock(w) ==
  /\ pc[w] = "idle" /\ want[w] # <<>>
  /\ IF Broken = "flatappend"
        THEN holder' = holder
        ELSE /\ holder = NoWriter
             /\ holder' = w
  /\ pc' = [pc EXCEPT ![w] = "locked"]
  /\ UNCHANGED <<file, want, seen, res, crashes>>

Read(w) ==
  /\ pc[w] = "locked"
  /\ seen' = [seen EXCEPT ![w] = Found(w)]
  /\ pc' = [pc EXCEPT ![w] = "read"]
  /\ UNCHANGED <<file, holder, want, res, crashes>>

Decide(w) ==
  /\ pc[w] = "read"
  /\ pc' = [pc EXCEPT ![w] = "decided"]
  /\ UNCHANGED <<file, holder, want, seen, res, crashes>>

Release(w) == IF holder = w THEN NoWriter ELSE holder

Write(w) ==
  /\ pc[w] = "decided" /\ seen[w] = "none"
  /\ Broken = "flatappend" \/ holder = w
  /\ file' = Append(file, want[w])
  /\ res' = [res EXCEPT ![w] = "appended"]
  /\ pc' = [pc EXCEPT ![w] = "done"]
  /\ holder' = Release(w)
  /\ UNCHANGED <<want, seen, crashes>>

Finish(w) ==
  /\ pc[w] = "decided" /\ seen[w] # "none"
  /\ res' = [res EXCEPT ![w] = IF seen[w] = "same" THEN "duplicate" ELSE "conflict"]
  /\ pc' = [pc EXCEPT ![w] = "done"]
  /\ holder' = Release(w)
  /\ UNCHANGED <<file, want, seen, crashes>>

Crash(w) ==
  /\ crashes < MaxCrashes
  /\ pc[w] \in {"locked", "read", "decided"}
  /\ pc' = [pc EXCEPT ![w] = "dead"]
  /\ holder' = Release(w)
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<file, want, seen, res>>

\* A crash after Write is the same event once the writer reached "done" before
\* it reported to its caller: the section stands and the report is lost.
CrashAfterWrite(w) ==
  /\ crashes < MaxCrashes
  /\ pc[w] = "done" /\ res[w] = "appended"
  /\ pc' = [pc EXCEPT ![w] = "dead"]
  /\ res' = [res EXCEPT ![w] = "none"]
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<file, holder, want, seen>>

Retry(w) ==
  /\ pc[w] = "dead"
  /\ pc' = [pc EXCEPT ![w] = "idle"]
  /\ UNCHANGED <<file, holder, want, seen, res, crashes>>

Next == \E w \in Writers :
  Start(w) \/ Lock(w) \/ Read(w) \/ Decide(w) \/ Write(w) \/ Finish(w)
  \/ Crash(w) \/ CrashAfterWrite(w) \/ Retry(w)

Spec == Init /\ [][Next]_vars

\* At most one section per id: a re-read never finds a second one.
OneSectionPerID == \A i \in Ids : Cardinality(SectionsOf(i)) <= 1

\* A reported-persisted append is readable: the record holds the section.
ReportedIsReadable ==
  \A w \in Writers :
    res[w] \in {"appended", "duplicate"} => Holds(want[w][1], want[w][2])

\* A retry of the same words is a duplicate: a writer reports an append only
\* if no section of its id and words stood when it read, and at most one
\* writer's append of an id is reported per section.
RetryIsDuplicate ==
  /\ \A w \in Writers : res[w] = "appended" => seen[w] = "none"
  /\ \A w \in Writers : res[w] = "duplicate" => seen[w] = "same"
  /\ \A i \in Ids : Cardinality({w \in Writers : res[w] = "appended" /\ want[w][1] = i}) <= 1
=============================================================================
