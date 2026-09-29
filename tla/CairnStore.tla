------------------------------ MODULE CairnStore ------------------------------
\* nova-cairn's store, as state. nova-tools internal/cairn: storeShape (the one
\* function that reads the shape from the store's contents), recordState (the
\* judgement of a session's record path), Open, openBench, Append, appendBench,
\* Index and Receipt.
\*
\* The state the code owns: top, the regular session files directly under the
\* store (<id>.md); blocked, the session paths directly under the store that
\* hold something that is not a record (a symlink to a directory, to nothing or to
\* somewhere outside the store, or a device, named <id>.md; a plain directory of that name counts for no
\* shape and is outside the model); sess, the session files under sessions/; ownMark, whether
\* any of sessions/, entries/ or log.jsonl exists; ents, the entries filed as
\* (session, entry, text). The shape is not state: it is read from the
\* contents, as the code reads it. A store with a top-level session path and an
\* ownMark is "mixed", and every verb refuses it. A store with only top-level
\* session paths is "bench". Anything else, the empty store included, is "own".
\*
\* The verbs are the actions: Open (creates the session's file in the store's
\* shape, and refuses a path holding a non-record), Append (files an entry under
\* a session that has a record, a duplicate when the same text is already
\* filed, a refusal on a different text under the same id, a refusal when the
\* session has no record). Append is three steps by any of several writers on
\* one machine: Begin (take the store's lock), Read (read what the entry id
\* holds and decide), Finish (write what was decided, and let go of the lock);
\* the write trusts the decision, as the code's does once it holds the lock.
\* Index and Receipt read and change nothing, so they are no action. The outside events are a person keeping the store by hand:
\* HandBenchFile, HandBlockFile and HandOwnDir, any of which can make a store
\* mixed, and HandRemoveBlock, HandMoveBenchAway and HandMoveOwnAway, which take
\* paths away again (the person's own act after a mixed store's refusal, which
\* names the next action in words; the tool moves and deletes nothing);
\* handed records that a person mixed the shapes.
\*
\* Outside the model: torn writes (a write that stops half way; the model's
\* Finish is one step), an older-version writer (a nova-cairn older than this one
\* that writes the own shape into a bench store; the model's tool never does),
\* and two machines (the lock serialises writers on one machine; two machines
\* writing one store through git are outside it).
\*
\* Reserved names (README) and letter case are outside the model: a top-level
\* README.md, in any case, is documentation and never a session file
\* (internal/cairn reservedSession), and only the exact ".md" name is a session
\* file. The model's Sessions are an abstract set of ids that are all valid
\* session names, so those rules change nothing the model states.
\*
\* last is the name of the step just taken and subj the session it addressed,
\* so a property can say which step changed what.
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "openown"       open on a bench store writes the tool's own shape
\*                   (sessions/, log.jsonl) beside the bench files: the store
\*                   holds two shapes
\*   "appendcreates" an append to a session with no record creates the record
\*   "conflictadds"  the same entry id with different text is filed as a
\*                   second text
\*   "mixedserved"   open on a mixed store proceeds instead of refusing
\*   "openblocked"   open reports success over a path that holds a non-record,
\*                   leaving no record behind
\*   "nolock"        append takes no lock: two writers read "new" for one entry
\*                   id with different texts and both write, so one id maps to
\*                   two texts
\* Each is caught by one property below; the design passes all of them.

EXTENDS Naturals, FiniteSets

CONSTANTS Sessions, Entries, Texts, Writers, Broken

VARIABLES top, blocked, sess, ownMark, ents, handed, last, subj, lock, pc, arg, dec
vars == <<top, blocked, sess, ownMark, ents, handed, last, subj, lock, pc, arg, dec>>

NoWriter == "none"
Idle == "idle"
Args == Sessions \X Entries \X Texts
NoArg == CHOOSE a \in Args : TRUE
Decisions == {"none", "write", "dup", "conflict", "missing", "mixed"}

BenchPaths == top \cup blocked

Shape ==
  IF BenchPaths # {} /\ ownMark THEN "mixed"
  ELSE IF BenchPaths # {} THEN "bench"
  ELSE "own"

\* Where a session's record stands in a store of this shape.
RecordSet == IF Shape = "bench" THEN top ELSE sess

TypeOK ==
  /\ top \subseteq Sessions
  /\ blocked \subseteq Sessions
  /\ top \cap blocked = {}
  /\ sess \subseteq Sessions
  /\ ownMark \in BOOLEAN
  /\ ents \subseteq (Sessions \X Entries \X Texts)
  /\ handed \in BOOLEAN
  /\ last \in {"Init", "Open", "Begin", "Read", "Append", "Refuse", "Hand"}
  /\ subj \in Sessions
  /\ lock \in Writers \cup {NoWriter}
  /\ pc \in [Writers -> {Idle, "began", "decided"}]
  /\ arg \in [Writers -> Args]
  /\ dec \in [Writers -> Decisions]

Init ==
  /\ top = {}
  /\ blocked = {}
  /\ sess = {}
  /\ ownMark = FALSE
  /\ ents = {}
  /\ handed = FALSE
  /\ last = "Init"
  /\ subj \in Sessions
  /\ lock = NoWriter
  /\ pc = [w \in Writers |-> Idle]
  /\ arg = [w \in Writers |-> NoArg]
  /\ dec = [w \in Writers |-> "none"]

HasEntry(s, e) == \E t \in Texts : <<s, e, t>> \in ents

\* open: in a bench store the session's file appears at the top level and
\* nothing else appears; in an own-shape store (a new one included) the
\* session file appears under sessions/.
Open(s) ==
  /\ (Shape # "mixed" \/ Broken = "mixedserved")
  /\ (s \notin blocked \/ Broken = "openblocked")
  /\ last' = "Open"
  /\ subj' = s
  /\ UNCHANGED <<ents, handed, blocked, lock, pc, arg, dec>>
  /\ IF s \in blocked
       THEN UNCHANGED <<top, sess, ownMark>>
     ELSE IF Shape = "bench" /\ Broken # "openown"
       THEN /\ top' = top \cup {s}
            /\ UNCHANGED <<sess, ownMark>>
       ELSE /\ sess' = sess \cup {s}
            /\ ownMark' = TRUE
            /\ UNCHANGED top

\* open on a path that holds a non-record is refused and changes nothing.
OpenRefusesNonRecord(s) ==
  /\ Shape = "bench"
  /\ s \in blocked
  /\ last' = "Refuse"
  /\ subj' = s
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed, lock, pc, arg, dec>>

\* append, step one: a writer takes the store's lock for a request. With the
\* lock a second writer waits; without it (the nolock witness) it does not.
Begin(w, s, e, t) ==
  /\ pc[w] = Idle
  /\ (lock = NoWriter \/ Broken = "nolock")
  /\ lock' = IF Broken = "nolock" THEN lock ELSE w
  /\ pc' = [pc EXCEPT ![w] = "began"]
  /\ arg' = [arg EXCEPT ![w] = <<s, e, t>>]
  /\ last' = "Begin"
  /\ subj' = s
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed, dec>>

\* what a read of the store decides for a request: a mixed store is refused, a
\* session with no record is refused (naming open), the same words are a
\* duplicate, different words under the id are a conflict, otherwise it is new.
Decide(a) ==
  IF Shape = "mixed" THEN "mixed"
  ELSE IF a[1] \notin RecordSet THEN "missing"
  ELSE IF a \in ents THEN "dup"
  ELSE IF HasEntry(a[1], a[2]) THEN "conflict"
  ELSE "write"

\* append, step two: the writer reads what the entry id holds and decides.
Read(w) ==
  /\ pc[w] = "began"
  /\ dec' = [dec EXCEPT ![w] = Decide(arg[w])]
  /\ pc' = [pc EXCEPT ![w] = "decided"]
  /\ last' = "Read"
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed, subj, lock, arg>>

\* append, step three: the writer writes what it decided, without reading again,
\* and lets go of the lock. A decision the store no longer supports (a person
\* mixed the shapes or took the record away in between) is refused.
Finish(w) ==
  /\ pc[w] = "decided"
  /\ LET a == arg[w]
         d == dec[w]
         intact == Shape # "mixed" /\ a[1] \in RecordSet
     IN /\ subj' = a[1]
        /\ pc' = [pc EXCEPT ![w] = Idle]
        /\ arg' = [arg EXCEPT ![w] = NoArg]
        /\ dec' = [dec EXCEPT ![w] = "none"]
        /\ lock' = IF lock = w THEN NoWriter ELSE lock
        /\ IF d = "write" /\ intact
             THEN /\ ents' = ents \cup {a}
                  /\ last' = "Append"
                  /\ UNCHANGED <<top, blocked, sess, ownMark>>
           ELSE IF d = "dup" /\ Shape # "mixed"
             THEN /\ last' = "Append"
                  /\ UNCHANGED <<top, blocked, sess, ownMark, ents>>
           ELSE IF d = "conflict" /\ Broken = "conflictadds" /\ intact
             THEN /\ ents' = ents \cup {a}
                  /\ last' = "Append"
                  /\ UNCHANGED <<top, blocked, sess, ownMark>>
           ELSE IF d = "missing" /\ Broken = "appendcreates" /\ Shape # "mixed"
             THEN /\ last' = "Append"
                  /\ ents' = ents \cup {a}
                  /\ IF Shape = "bench"
                       THEN /\ top' = top \cup {a[1]}
                            /\ UNCHANGED <<sess, ownMark, blocked>>
                       ELSE /\ sess' = sess \cup {a[1]}
                            /\ ownMark' = TRUE
                            /\ UNCHANGED <<top, blocked>>
           ELSE /\ last' = "Refuse"
                /\ UNCHANGED <<top, blocked, sess, ownMark, ents>>
  /\ UNCHANGED handed

\* every verb on a mixed store is refused, and nothing is written.
RefuseMixed ==
  /\ Shape = "mixed"
  /\ last' = "Refuse"
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed, subj, lock, pc, arg, dec>>

\* a person keeps a session file at the top level by hand.
HandBenchFile(s) ==
  /\ s \notin BenchPaths
  /\ top' = top \cup {s}
  /\ handed' = (handed \/ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<blocked, sess, ownMark, ents, subj, lock, pc, arg, dec>>

\* a person leaves a directory or a dangling link named <id>.md at the top level.
HandBlockFile(s) ==
  /\ s \notin BenchPaths
  /\ blocked' = blocked \cup {s}
  /\ handed' = (handed \/ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<top, sess, ownMark, ents, subj, lock, pc, arg, dec>>

\* a person removes such a path.
HandRemoveBlock(s) ==
  /\ s \in blocked
  /\ blocked' = blocked \ {s}
  /\ handed' = (handed /\ (top # {} \/ blocked \ {s} # {}) /\ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<top, sess, ownMark, ents, subj, lock, pc, arg, dec>>

\* a person makes one of sessions/, entries/ or log.jsonl by hand.
HandOwnDir ==
  /\ ~ownMark
  /\ ownMark' = TRUE
  /\ handed' = (handed \/ BenchPaths # {})
  /\ last' = "Hand"
  /\ UNCHANGED <<top, blocked, sess, ents, subj, lock, pc, arg, dec>>

\* a person moves the top-level session paths out of the store, and the entries
\* filed in them go with them.
HandMoveBenchAway ==
  /\ Shape = "mixed"
  /\ top' = {}
  /\ blocked' = {}
  /\ ents' = {x \in ents : x[1] \in sess}
  /\ handed' = FALSE
  /\ last' = "Hand"
  /\ UNCHANGED <<sess, ownMark, subj, lock, pc, arg, dec>>

\* a person moves sessions/, entries/ and log.jsonl out of the store.
HandMoveOwnAway ==
  /\ Shape = "mixed"
  /\ sess' = {}
  /\ ownMark' = FALSE
  /\ ents' = {x \in ents : x[1] \in top}
  /\ handed' = FALSE
  /\ last' = "Hand"
  /\ UNCHANGED <<top, blocked, subj, lock, pc, arg, dec>>

Next ==
  \/ \E s \in Sessions : Open(s) \/ OpenRefusesNonRecord(s)
  \/ \E w \in Writers : Read(w) \/ Finish(w)
  \/ \E w \in Writers, s \in Sessions, e \in Entries, t \in Texts : Begin(w, s, e, t)
  \/ RefuseMixed
  \/ \E s \in Sessions : HandBenchFile(s) \/ HandBlockFile(s) \/ HandRemoveBlock(s)
  \/ HandOwnDir
  \/ HandMoveBenchAway
  \/ HandMoveOwnAway

Spec == Init /\ [][Next]_vars

\* One shape per store: the tool never makes a mixed store; only a person does.
OneShapeUnlessHanded == Shape = "mixed" => handed

\* An entry id maps to one text.
OneTextPerEntry ==
  \A s \in Sessions, e \in Entries :
    Cardinality({t \in Texts : <<s, e, t>> \in ents}) <= 1

\* The lock is the critical section: at most one writer is between Begin and
\* Finish, and it is the one that holds the lock.
OneWriterInTheSection ==
  /\ Cardinality({w \in Writers : pc[w] # Idle}) <= 1
  /\ \A w \in Writers : pc[w] # Idle => lock = w

\* An entry belongs to a session that has a record.
EntriesBelongToRecords == \A x \in ents : x[1] \in top \cup sess

\* sessions/ exists as soon as a session is opened there.
SessionsMarkOwn == sess # {} => ownMark

\* Append never creates a session record.
AppendCreatesNoSession ==
  [][(last' = "Append") => (top' = top /\ sess' = sess)]_vars

\* No verb succeeds on a mixed store.
MixedIsRefusedByEveryVerb ==
  [][~(Shape = "mixed" /\ last' \in {"Open", "Append"})]_vars

\* Open and Append keep the shape they found: a bench store stays bench, and
\* an empty store becomes the tool's own shape and stays it.
ToolKeepsTheShape ==
  [][(last' \in {"Open", "Append"}) => Shape' = Shape]_vars

\* A successful open leaves a record for the session it addressed: it never
\* reports success over a path that holds a non-record.
OpenLeavesARecord ==
  [][(last' = "Open") => subj' \in RecordSet']_vars

=============================================================================
