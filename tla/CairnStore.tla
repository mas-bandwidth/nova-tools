------------------------------ MODULE CairnStore ------------------------------
\* nova-cairn's store, as state. nova-tools internal/cairn: storeShape (the one
\* function that reads the shape from the store's contents), recordState (the
\* judgement of a session's record path), Open, openBench, Append, appendBench,
\* Index and Receipt.
\*
\* The state the code owns: top, the regular session files directly under the
\* store (<id>.md); blocked, the session paths directly under the store that
\* hold something that is not a record (a symlink to a directory or to nothing,
\* or a device, named <id>.md; a plain directory of that name counts for no
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
\* session has no record). Index and Receipt read and change nothing, so they
\* are no action. The outside events are a person keeping the store by hand:
\* HandBenchFile, HandBlockFile and HandOwnDir, any of which can make a store
\* mixed, and HandRemoveBlock, MoveBenchAway and MoveOwnAway, which take files
\* away again (MoveBenchAway and MoveOwnAway are the two next actions a mixed
\* store's refusal names); handed records that a person mixed the shapes.
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
\*   "moveleaks"     the next action for a mixed store leaves a non-record
\*                   path behind, so the store is still mixed
\* Each is caught by one property below; the design passes all of them.

EXTENDS Naturals, FiniteSets

CONSTANTS Sessions, Entries, Texts, Broken

VARIABLES top, blocked, sess, ownMark, ents, handed, last, subj
vars == <<top, blocked, sess, ownMark, ents, handed, last, subj>>

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
  /\ last \in {"Init", "Open", "Append", "Refuse", "Hand", "MoveBench", "MoveOwn"}
  /\ subj \in Sessions

Init ==
  /\ top = {}
  /\ blocked = {}
  /\ sess = {}
  /\ ownMark = FALSE
  /\ ents = {}
  /\ handed = FALSE
  /\ last = "Init"
  /\ subj \in Sessions

HasEntry(s, e) == \E t \in Texts : <<s, e, t>> \in ents

\* open: in a bench store the session's file appears at the top level and
\* nothing else appears; in an own-shape store (a new one included) the
\* session file appears under sessions/.
Open(s) ==
  /\ (Shape # "mixed" \/ Broken = "mixedserved")
  /\ (s \notin blocked \/ Broken = "openblocked")
  /\ last' = "Open"
  /\ subj' = s
  /\ UNCHANGED <<ents, handed, blocked>>
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
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed>>

\* append: a new entry under a session that has a record.
AppendNew(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \in RecordSet
  /\ ~HasEntry(s, e)
  /\ ents' = ents \cup {<<s, e, t>>}
  /\ last' = "Append"
  /\ subj' = s
  /\ UNCHANGED <<top, blocked, sess, ownMark, handed>>

\* append: the same id with the same text succeeds and changes nothing.
AppendDup(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \in RecordSet
  /\ <<s, e, t>> \in ents
  /\ last' = "Append"
  /\ subj' = s
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed>>

\* append: the same id with different text is refused.
AppendConflict(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \in RecordSet
  /\ \E t2 \in Texts : t2 # t /\ <<s, e, t2>> \in ents
  /\ subj' = s
  /\ IF Broken = "conflictadds"
       THEN /\ ents' = ents \cup {<<s, e, t>>}
            /\ last' = "Append"
       ELSE /\ UNCHANGED ents
            /\ last' = "Refuse"
  /\ UNCHANGED <<top, blocked, sess, ownMark, handed>>

\* append: a session with no record is refused, naming open.
AppendMissing(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \notin RecordSet
  /\ subj' = s
  /\ IF Broken = "appendcreates"
       THEN /\ last' = "Append"
            /\ ents' = ents \cup {<<s, e, t>>}
            /\ IF Shape = "bench"
                 THEN /\ top' = top \cup {s}
                      /\ UNCHANGED <<sess, ownMark, blocked>>
                 ELSE /\ sess' = sess \cup {s}
                      /\ ownMark' = TRUE
                      /\ UNCHANGED <<top, blocked>>
       ELSE /\ last' = "Refuse"
            /\ UNCHANGED <<top, blocked, sess, ownMark, ents>>
  /\ UNCHANGED handed

\* every verb on a mixed store is refused, and nothing is written.
RefuseMixed ==
  /\ Shape = "mixed"
  /\ last' = "Refuse"
  /\ UNCHANGED <<top, blocked, sess, ownMark, ents, handed, subj>>

\* a person keeps a session file at the top level by hand.
HandBenchFile(s) ==
  /\ s \notin BenchPaths
  /\ top' = top \cup {s}
  /\ handed' = (handed \/ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<blocked, sess, ownMark, ents, subj>>

\* a person leaves a directory or a dangling link named <id>.md at the top level.
HandBlockFile(s) ==
  /\ s \notin BenchPaths
  /\ blocked' = blocked \cup {s}
  /\ handed' = (handed \/ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<top, sess, ownMark, ents, subj>>

\* a person removes such a path.
HandRemoveBlock(s) ==
  /\ s \in blocked
  /\ blocked' = blocked \ {s}
  /\ handed' = (handed /\ (top # {} \/ blocked \ {s} # {}) /\ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<top, sess, ownMark, ents, subj>>

\* a person makes one of sessions/, entries/ or log.jsonl by hand.
HandOwnDir ==
  /\ ~ownMark
  /\ ownMark' = TRUE
  /\ handed' = (handed \/ BenchPaths # {})
  /\ last' = "Hand"
  /\ UNCHANGED <<top, blocked, sess, ents, subj>>

\* the first next action of a mixed store's refusal: the top-level session paths
\* are moved out of the store, and the entries filed in them go with them.
MoveBenchAway ==
  /\ Shape = "mixed"
  /\ top' = {}
  /\ blocked' = IF Broken = "moveleaks" THEN blocked ELSE {}
  /\ ents' = {x \in ents : x[1] \in sess}
  /\ handed' = FALSE
  /\ last' = "MoveBench"
  /\ UNCHANGED <<sess, ownMark, subj>>

\* the second: sessions/, entries/ and log.jsonl are moved out of the store.
MoveOwnAway ==
  /\ Shape = "mixed"
  /\ sess' = {}
  /\ ownMark' = FALSE
  /\ ents' = {x \in ents : x[1] \in top}
  /\ handed' = FALSE
  /\ last' = "MoveOwn"
  /\ UNCHANGED <<top, blocked, subj>>

Next ==
  \/ \E s \in Sessions : Open(s) \/ OpenRefusesNonRecord(s)
  \/ \E s \in Sessions, e \in Entries, t \in Texts :
       \/ AppendNew(s, e, t)
       \/ AppendDup(s, e, t)
       \/ AppendConflict(s, e, t)
       \/ AppendMissing(s, e, t)
  \/ RefuseMixed
  \/ \E s \in Sessions : HandBenchFile(s) \/ HandBlockFile(s) \/ HandRemoveBlock(s)
  \/ HandOwnDir
  \/ MoveBenchAway
  \/ MoveOwnAway

Spec == Init /\ [][Next]_vars

\* One shape per store: the tool never makes a mixed store; only a person does.
OneShapeUnlessHanded == Shape = "mixed" => handed

\* An entry id maps to one text.
OneTextPerEntry ==
  \A s \in Sessions, e \in Entries :
    Cardinality({t \in Texts : <<s, e, t>> \in ents}) <= 1

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

\* Either next action of a mixed store's refusal leaves one shape.
MovingAwayLeavesOneShape ==
  [][(last' \in {"MoveBench", "MoveOwn"}) => Shape' # "mixed"]_vars

=============================================================================
