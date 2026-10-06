---------------------------- MODULE StreamArchive ----------------------------
(* Stream archive (internal/sprint/stream_archive.go, store/stream_archive.go): one
   stream's cards and whether its rows are hidden. Archiving writes the rows alone, never
   a card; the tick archives a stream whose last card landed with nothing queued, unless
   the coordinator unarchived it at the same landed count; it draws again an archived
   stream that holds a card not landed. BROKEN turns the tick's stray rule off: the
   reversed witness, under which an added card can wait where nothing draws it. *)
EXTENDS Integers, FiniteSets

CONSTANTS Cards, BROKEN

VARIABLES card,    \* card[c] \in {"absent", "queued", "open", "landed"}
          hidden,  \* the rows are hidden (archived)
          kept,    \* the landed count at the coordinator's unarchive, or -1
          notes    \* the tick's "stream archived" notes

vars == <<card, hidden, kept, notes>>

Landed == {c \in Cards : card[c] = "landed"}
Open   == {c \in Cards : card[c] \in {"queued", "open"}}
NLanded == Cardinality(Landed)

Init == card = [c \in Cards |-> "absent"] /\ hidden = FALSE /\ kept = -1 /\ notes = 0

\* add queues a card while the machine runs; the pump places it
Add(c)  == card[c] = "absent" /\ card' = [card EXCEPT ![c] = "queued"] /\ UNCHANGED <<hidden, kept, notes>>
Pump(c) == card[c] = "queued" /\ card' = [card EXCEPT ![c] = "open"] /\ UNCHANGED <<hidden, kept, notes>>
Land(c) == card[c] = "open" /\ card' = [card EXCEPT ![c] = "landed"] /\ UNCHANGED <<hidden, kept, notes>>

\* stream archive: the rule reads the work table as the next pump leaves it
Archive   == Open = {} /\ hidden' = TRUE /\ kept' = -1 /\ UNCHANGED <<card, notes>>
Unarchive == hidden /\ hidden' = FALSE /\ kept' = NLanded /\ UNCHANGED <<card, notes>>

\* the tick (keepArchive): the stray rule, then the archive of a stream due
TickStray   == ~BROKEN /\ hidden /\ {c \in Cards : card[c] = "open"} # {} /\ hidden' = FALSE /\ UNCHANGED <<card, kept, notes>>
TickArchive == ~hidden /\ Open = {} /\ NLanded > 0 /\ kept # NLanded
               /\ hidden' = TRUE /\ kept' = -1 /\ notes' = notes + 1 /\ UNCHANGED card

Tick == TickStray \/ TickArchive

Next == \E c \in Cards : Add(c) \/ Pump(c) \/ Land(c)
        \/ Archive \/ Unarchive \/ Tick

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ \A c \in Cards : WF_vars(Pump(c))

TypeOK == card \in [Cards -> {"absent", "queued", "open", "landed"}] /\ hidden \in BOOLEAN
          /\ kept \in -1..Cardinality(Cards) /\ notes \in Nat

\* the ledger: archiving never moves a card, and a landed card stays landed
LedgerKept == [][\A c \in Cards : card[c] = "landed" => card'[c] = "landed"]_vars
ArchiveMovesNoCard == [][(hidden' # hidden) => card' = card]_vars

\* the tick never archives a stream with a card not landed, nor one the coordinator
\* unarchived at this landed count
TickArchivesOnlyLanded == [][TickArchive => (Open = {} /\ kept # NLanded)]_vars

\* one note per archive by the tick: never more notes than landings
NotesBounded == notes <= Cardinality(Cards)

\* an archived stream holding a card not landed is drawn again, unless the card lands
\* first (TLC's counterexample to the stronger form: add, pump and land between two ticks;
\* in the code a landing takes ticks, and the tick that pumps the add draws it, keepArchive)
StrayDrawn == \A c \in Cards : (hidden /\ card[c] = "open") ~> (~hidden \/ card[c] = "landed")

\* a stream whose every card landed, nothing more coming, is archived
LandedArchived == (\A c \in Cards : card[c] = "landed" /\ kept = -1) ~> hidden
=============================================================================
