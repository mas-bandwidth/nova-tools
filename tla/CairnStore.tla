------------------------------ MODULE CairnStore ------------------------------
\* nova-cairn's store, as state. nova-tools internal/cairn: storeShape (the one
\* function that reads the shape from the store's contents), Open, openBench,
\* Append, appendBench, Index and Receipt.
\*
\* The state the code owns: top, the session files directly under the store
\* (<id>.md); sess, the session files under sessions/; ownMark, whether any of
\* sessions/, entries/ or log.jsonl exists; ents, the entries filed as
\* (session, entry, text). The shape is not state: it is read from the
\* contents, as the code reads it. A store with a top-level session file and
\* an ownMark is "mixed", and every verb refuses it. A store with only
\* top-level session files is "bench". Anything else, the empty store
\* included, is "own".
\*
\* The verbs are the actions: Open (creates the session's file in the store's
\* shape), Append (files an entry under a session that has a record, a
\* duplicate when the same text is already filed, a refusal on a different text
\* under the same id, a refusal when the session has no record). Index and
\* Receipt read and change nothing, so they are no action. The outside events
\* are a person keeping the store by hand: HandBenchFile and HandOwnDir, either
\* of which can make a store mixed; handed records that a person did.
\*
\* Reserved names are outside the model: a top-level README.md, in any case, is
\* documentation and never a session file (internal/cairn reservedSession). The
\* model's Sessions are an abstract set of ids that are all valid session names,
\* so the rule changes nothing the model states.
\*
\* last is the name of the step just taken, so a property can say which step
\* changed what.
\*
\*
\* Broken = "none" is the design. Every other value is a reversed witness:
\*   "openown"       open on a bench store writes the tool's own shape
\*                   (sessions/, log.jsonl) beside the bench files: the store
\*                   holds two shapes
\*   "appendcreates" an append to a session with no record creates the record
\*   "conflictadds"  the same entry id with different text is filed as a
\*                   second text
\*   "mixedserved"   open on a mixed store proceeds instead of refusing
\* Each is caught by one property below; the design passes all of them.

EXTENDS Naturals, FiniteSets

CONSTANTS Sessions, Entries, Texts, Broken

VARIABLES top, sess, ownMark, ents, handed, last
vars == <<top, sess, ownMark, ents, handed, last>>

Shape ==
  IF top # {} /\ ownMark THEN "mixed"
  ELSE IF top # {} THEN "bench"
  ELSE "own"

\* Where a session's record stands in a store of this shape.
RecordSet == IF Shape = "bench" THEN top ELSE sess

TypeOK ==
  /\ top \subseteq Sessions
  /\ sess \subseteq Sessions
  /\ ownMark \in BOOLEAN
  /\ ents \subseteq (Sessions \X Entries \X Texts)
  /\ handed \in BOOLEAN
  /\ last \in {"Init", "Open", "Append", "Refuse", "Hand"}

Init ==
  /\ top = {}
  /\ sess = {}
  /\ ownMark = FALSE
  /\ ents = {}
  /\ handed = FALSE
  /\ last = "Init"

HasEntry(s, e) == \E t \in Texts : <<s, e, t>> \in ents

\* open: in a bench store the session's file appears at the top level and
\* nothing else appears; in an own-shape store (a new one included) the
\* session file appears under sessions/.
Open(s) ==
  /\ (Shape # "mixed" \/ Broken = "mixedserved")
  /\ last' = "Open"
  /\ UNCHANGED <<ents, handed>>
  /\ IF Shape = "bench" /\ Broken # "openown"
       THEN /\ top' = top \cup {s}
            /\ UNCHANGED <<sess, ownMark>>
       ELSE /\ sess' = sess \cup {s}
            /\ ownMark' = TRUE
            /\ UNCHANGED top

\* append: a new entry under a session that has a record.
AppendNew(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \in RecordSet
  /\ ~HasEntry(s, e)
  /\ ents' = ents \cup {<<s, e, t>>}
  /\ last' = "Append"
  /\ UNCHANGED <<top, sess, ownMark, handed>>

\* append: the same id with the same text succeeds and changes nothing.
AppendDup(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \in RecordSet
  /\ <<s, e, t>> \in ents
  /\ last' = "Append"
  /\ UNCHANGED <<top, sess, ownMark, ents, handed>>

\* append: the same id with different text is refused.
AppendConflict(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \in RecordSet
  /\ \E t2 \in Texts : t2 # t /\ <<s, e, t2>> \in ents
  /\ IF Broken = "conflictadds"
       THEN /\ ents' = ents \cup {<<s, e, t>>}
            /\ last' = "Append"
       ELSE /\ UNCHANGED ents
            /\ last' = "Refuse"
  /\ UNCHANGED <<top, sess, ownMark, handed>>

\* append: a session with no record is refused, naming open.
AppendMissing(s, e, t) ==
  /\ Shape # "mixed"
  /\ s \notin RecordSet
  /\ IF Broken = "appendcreates"
       THEN /\ last' = "Append"
            /\ ents' = ents \cup {<<s, e, t>>}
            /\ IF Shape = "bench"
                 THEN /\ top' = top \cup {s}
                      /\ UNCHANGED <<sess, ownMark>>
                 ELSE /\ sess' = sess \cup {s}
                      /\ ownMark' = TRUE
                      /\ UNCHANGED top
       ELSE /\ last' = "Refuse"
            /\ UNCHANGED <<top, sess, ownMark, ents>>
  /\ UNCHANGED handed

\* every verb on a mixed store is refused, and nothing is written.
RefuseMixed ==
  /\ Shape = "mixed"
  /\ last' = "Refuse"
  /\ UNCHANGED <<top, sess, ownMark, ents, handed>>

\* a person keeps a session file at the top level by hand.
HandBenchFile(s) ==
  /\ s \notin top
  /\ top' = top \cup {s}
  /\ handed' = (handed \/ ownMark)
  /\ last' = "Hand"
  /\ UNCHANGED <<sess, ownMark, ents>>

\* a person makes one of sessions/, entries/ or log.jsonl by hand.
HandOwnDir ==
  /\ ~ownMark
  /\ ownMark' = TRUE
  /\ handed' = (handed \/ top # {})
  /\ last' = "Hand"
  /\ UNCHANGED <<top, sess, ents>>

Next ==
  \/ \E s \in Sessions : Open(s)
  \/ \E s \in Sessions, e \in Entries, t \in Texts :
       \/ AppendNew(s, e, t)
       \/ AppendDup(s, e, t)
       \/ AppendConflict(s, e, t)
       \/ AppendMissing(s, e, t)
  \/ RefuseMixed
  \/ \E s \in Sessions : HandBenchFile(s)
  \/ HandOwnDir

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

=============================================================================
