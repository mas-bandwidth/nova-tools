------------------------------- MODULE Cairn -------------------------------
\* One cairn id, two appenders. The read and the write are separate steps,
\* and a crash can fall between them.
\*
\* internal/cairn/cairn.go. A session is one shape (locateRecord, :189-200).
\* Flat: appendFlat holds the sibling file lock across the read, the
\* duplicate-or-conflict decision and the append (:301-306, :335-369;
\* internal/filelock, tla/FileLock.tla). The section is one dated heading
\* per id (flatHeading, :227-229; findFlat returns the first, :263-270).
\* Nested: appendEntry reads the entry file, then publishes with
\* atomicfile.NoReplace (:685-700). Two writers may both read the id
\* absent; exactly one publish lands, and the loser re-reads and reports
\* duplicate or conflict (:695-698, existingAppend :720-742). A crash
\* after the bytes are durable and before the result is reported leaves
\* them readable (:366-369, :694-708). A retry of the same words is
\* Duplicate and adds nothing (:349-357, :740-742).
\*
\* The brief cites cairn.go:293-316 for the non-exclusive read-then-append.
\* At this tip those lines are the dry run (write false): no lock and no
\* write. The passing spec follows the code. Broken = "flatappend" is the
\* reversed witness for security#73 finding 1: the read and the append
\* take no lock and do not re-check, so two writers who both read the id
\* absent each append a section, and AtMostOneSectionOrEntry fails.
\*
\* Not here: publish policy, the log, the index and coverage
\* (tla/CairnStore.tla), stamps, the pointer line, a torn write
\* (atomicfile is all or nothing). The flat sequence is capped at one
\* section per writer so the instance is finite; the code has no such cap.
\* Two writers are enough for the witness.

EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Writers, Ids, Texts, Broken

ASSUME Cardinality(Ids) = 1
ASSUME Broken \in {"none", "flatappend"}

None == "none"
TheId == CHOOSE i \in Ids : TRUE

VARIABLES
  shape,    \* "flat", "nested", or None until the first read
  flat,     \* the flat record: a sequence of sections, each one id and its words
  nested,   \* the nested entry file: one text per id, or None
  lock,     \* the flat lock's holder, or None (the kernel lock, not the bytes)
  phase,    \* per writer: idle, read, write, written, done
  text,     \* the words this attempt would store
  saw,      \* the words the read found under the id, or None
  report,   \* none, persisted, duplicate, conflict
  wrote     \* this attempt appended a section or published an entry

vars == <<shape, flat, nested, lock, phase, text, saw, report, wrote>>

TypeOK ==
  /\ shape \in {"flat", "nested", None}
  /\ flat \in Seq([id: Ids, text: Texts])
  /\ Len(flat) <= Cardinality(Writers)
  /\ nested \in [Ids -> Texts \cup {None}]
  /\ lock \in Writers \cup {None}
  /\ phase \in [Writers -> {"idle", "read", "write", "written", "done"}]
  /\ text \in [Writers -> Texts \cup {None}]
  /\ saw \in [Writers -> Texts \cup {None}]
  /\ report \in [Writers -> {"none", "persisted", "duplicate", "conflict"}]
  /\ wrote \in [Writers -> BOOLEAN]

Sections(i) == Cardinality({n \in DOMAIN flat : flat[n].id = i})
Entries(i) == IF nested[i] = None THEN 0 ELSE 1

\* findFlat: the first section's words, or absent.
FlatSeen == IF flat = <<>> THEN None ELSE flat[1].text

Init ==
  /\ shape = None
  /\ flat = <<>>
  /\ nested = [i \in Ids |-> None]
  /\ lock = None
  /\ phase = [w \in Writers |-> "idle"]
  /\ text = [w \in Writers |-> None]
  /\ saw = [w \in Writers |-> None]
  /\ report = [w \in Writers |-> "none"]
  /\ wrote = [w \in Writers |-> FALSE]

\* The design takes the lock before the read and holds it until the
\* decision or the report. The witness never takes it.
TakeLock(w) ==
  IF Broken = "flatappend" THEN UNCHANGED lock
  ELSE /\ lock = None
       /\ lock' = w

DropLock(w) ==
  IF lock = w THEN lock' = None ELSE UNCHANGED lock

FlatExclusive(w) ==
  \/ Broken = "flatappend" /\ lock = None
  \/ lock = w

\* A crash drops the flock (the kernel releases it when the holder dies).
\* A write that has landed stays; a report that has not been made is not made.
Crash(w) ==
  /\ phase[w] \in {"read", "write", "written"}
  /\ phase' = [phase EXCEPT ![w] = "idle"]
  /\ text' = [text EXCEPT ![w] = None]
  /\ saw' = [saw EXCEPT ![w] = None]
  /\ report' = [report EXCEPT ![w] = "none"]
  /\ wrote' = [wrote EXCEPT ![w] = FALSE]
  /\ DropLock(w)
  /\ UNCHANGED <<shape, flat, nested>>

\* ---- flat: read, decide, append, report (appendFlat :341-369)

FlatRead(w, t) ==
  /\ phase[w] = "idle"
  /\ shape \in {None, "flat"}
  /\ TakeLock(w)
  /\ shape' = "flat"
  /\ phase' = [phase EXCEPT ![w] = "read"]
  /\ text' = [text EXCEPT ![w] = t]
  /\ saw' = [saw EXCEPT ![w] = FlatSeen]
  /\ UNCHANGED <<flat, nested, report, wrote>>

FlatDecide(w) ==
  /\ phase[w] = "read"
  /\ shape = "flat"
  /\ FlatExclusive(w)
  /\ IF saw[w] = None
       THEN /\ phase' = [phase EXCEPT ![w] = "write"]
            /\ UNCHANGED <<lock, report>>
       ELSE /\ phase' = [phase EXCEPT ![w] = "done"]
            /\ report' = [report EXCEPT ![w] =
                 IF saw[w] = text[w] THEN "duplicate" ELSE "conflict"]
            /\ DropLock(w)
  /\ UNCHANGED <<shape, flat, nested, text, saw, wrote>>

\* The design appends while it still holds the lock, so the file is the
\* snapshot it read. The witness appends on the stale decision anyway.
FlatWrite(w) ==
  /\ phase[w] = "write"
  /\ shape = "flat"
  /\ FlatExclusive(w)
  /\ Len(flat) < Cardinality(Writers)
  /\ flat' = Append(flat, [id |-> TheId, text |-> text[w]])
  /\ phase' = [phase EXCEPT ![w] = "written"]
  /\ wrote' = [wrote EXCEPT ![w] = TRUE]
  /\ UNCHANGED <<shape, nested, lock, text, saw, report>>

FlatReport(w) ==
  /\ phase[w] = "written"
  /\ shape = "flat"
  /\ FlatExclusive(w)
  /\ phase' = [phase EXCEPT ![w] = "done"]
  /\ report' = [report EXCEPT ![w] = "persisted"]
  /\ DropLock(w)
  /\ UNCHANGED <<shape, flat, nested, text, saw, wrote>>

\* ---- nested: read, decide, publish or re-read, report (appendEntry :656-708)

NestedRead(w, t) ==
  /\ phase[w] = "idle"
  /\ shape \in {None, "nested"}
  /\ lock = None
  /\ shape' = "nested"
  /\ phase' = [phase EXCEPT ![w] = "read"]
  /\ text' = [text EXCEPT ![w] = t]
  /\ saw' = [saw EXCEPT ![w] = nested[TheId]]
  /\ UNCHANGED <<flat, nested, lock, report, wrote>>

NestedDecide(w) ==
  /\ phase[w] = "read"
  /\ shape = "nested"
  /\ IF saw[w] = None
       THEN /\ phase' = [phase EXCEPT ![w] = "write"]
            /\ UNCHANGED report
       ELSE /\ phase' = [phase EXCEPT ![w] = "done"]
            /\ report' = [report EXCEPT ![w] =
                 IF saw[w] = text[w] THEN "duplicate" ELSE "conflict"]
  /\ UNCHANGED <<shape, flat, nested, lock, text, saw, wrote>>

\* NoReplace: the publish lands only while the id is still absent.
\* Otherwise this writer re-reads and decides again (:695-698).
NestedWrite(w) ==
  /\ phase[w] = "write"
  /\ shape = "nested"
  /\ IF nested[TheId] = None
       THEN /\ nested' = [nested EXCEPT ![TheId] = text[w]]
            /\ phase' = [phase EXCEPT ![w] = "written"]
            /\ wrote' = [wrote EXCEPT ![w] = TRUE]
            /\ UNCHANGED saw
       ELSE /\ saw' = [saw EXCEPT ![w] = nested[TheId]]
            /\ phase' = [phase EXCEPT ![w] = "read"]
            /\ UNCHANGED <<nested, wrote>>
  /\ UNCHANGED <<shape, flat, lock, text, report>>

NestedReport(w) ==
  /\ phase[w] = "written"
  /\ shape = "nested"
  /\ phase' = [phase EXCEPT ![w] = "done"]
  /\ report' = [report EXCEPT ![w] = "persisted"]
  /\ UNCHANGED <<shape, flat, nested, lock, text, saw, wrote>>

Next ==
  \E w \in Writers :
    \/ Crash(w)
    \/ \E t \in Texts : FlatRead(w, t) \/ NestedRead(w, t)
    \/ FlatDecide(w) \/ FlatWrite(w) \/ FlatReport(w)
    \/ NestedDecide(w) \/ NestedWrite(w) \/ NestedReport(w)

Spec == Init /\ [][Next]_vars

\* ---------------------------------------------------------------- the rules

\* At most one flat section and one nested entry for the id.
AtMostOneSectionOrEntry ==
  \A i \in Ids : Sections(i) <= 1 /\ Entries(i) <= 1

\* A result that says the words were persisted can be read back.
ReportedPersistedIsReadable ==
  \A w \in Writers :
    report[w] = "persisted" =>
      \/ /\ shape = "flat"
         /\ \E n \in DOMAIN flat : flat[n].id = TheId /\ flat[n].text = text[w]
      \/ /\ shape = "nested"
         /\ nested[TheId] = text[w]

\* A writer who read the same words reports duplicate and did not append.
RetrySameWordsIsDuplicate ==
  \A w \in Writers :
    (phase[w] = "done" /\ saw[w] # None /\ saw[w] = text[w]) =>
      /\ report[w] = "duplicate"
      /\ ~wrote[w]

=============================================================================
