-------------------------------- MODULE Land --------------------------------
\* nova-sprint land (cmd/nova-sprint/land.go): the coordinator's landing step,
\* which pushes a batch of a stream's merge queue to a remote base and then
\* reports it to the store through the merge step. The push and the report
\* are two operations on two systems; this module is that sequence, the
\* outside events that can come between them, and the fences land keeps.
\*
\* THE STATE.
\*   queue    the stream's merge queue in the store, in work order (the cards
\*            queued before the first stuck one)
\*   landed   the cards the store records landed, at the store's epoch
\*   epoch    the store's epoch (a clear moves it and empties the tables)
\*   base     the cards whose heads the remote base holds
\*   tip      the remote base's tip, a counter: every change to the base moves it
\*   lphase   the lander's step: idle, read, built, pushed (reportfirst: reported)
\*   lq       the queue as the lander read it
\*   lbatch   the batch it built: a prefix of lq (a card that stops the batch,
\*            a conflict or a missing head, ends it, so any non-empty prefix)
\*   lep      the epoch the lander holds (the caller's --epoch, else the one read)
\*   ltip     the base's tip the batch was built on
\*   tries    the rebuilds on a moved base (one is allowed)
\*   events   the outside events so far, bounded by MaxEvents
\*   badpush  a ghost: a push was made by a lander whose epoch is not the store's
\*
\* THE ACTIONS. The lander's: Read (the queue, the epoch and the tip; a caller's
\* epoch the store has left is refused here, before any git), Build, Push (the
\* queue head and the epoch read again just before it, Fresh; a moved tip is
\* rejected, rebuilt once, then given up), Report (one store step that lands
\* the batch only while the queue still starts with it at the epoch held).
\* The outside's: Accept (a card queued anywhere, by its score), Return (a card
\* taken off the queue), OtherLand (another lander lands the queue's head,
\* correctly), Clear (the epoch moves, the store's tables empty), MoveBase
\* (another commit on the base), Crash (the lander stops at any step, between
\* the push and the report among them).
\*
\* THE RULES. LandedInBase: the store never records a card landed whose head
\* the base does not hold. LandsInOrder: a card lands only with every card
\* ahead of it in the queue. PushHoldsTheEpoch: no push is made for an epoch the
\* store has left. Recovers: a batch pushed and not reported (a crash, or a
\* report the guard refused) is recorded by running the lander again: under
\* fairness, once the outside is quiet, no queued card stays in the base.
\* ReachStranded is a reversed witness: the stranded state is reached, so
\* Recovers is not vacuous.
\*
\* Broken: "none" is the design. "reportfirst" reports the batch before it is
\* pushed (LandedInBase fails). "latepoch" checks the caller's epoch only at
\* the report, after the push (PushHoldsTheEpoch fails: finding 2 of the
\* review of #5020). "noguard" reports `merge --batch n` with no guard that the
\* queue still starts with the batch (LandedInBase fails: a card accepted ahead
\* of the batch between the push and the report lands unpushed).
\*
\* WHAT IS NOT MODELLED. A card's head is its id: a card re-added after a
\* clear has the same head. The check (--check), the facts that stop a stream
\* (conflict, red, rejected) and resume: a refusal is the lander going idle
\* with the store unchanged. The pre-push re-read and the push are one step
\* here; in the code they are two calls, and a change between them is the same
\* window as the one between the push and the report, which the report's
\* guard closes and Recovers recovers. One stream, one base.
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Cards, MaxEpoch, MaxEvents, Broken

VARIABLES queue, landed, epoch, base, tip, lphase, lq, lbatch, lep, ltip, tries, events, badpush

store == <<queue, landed, epoch>>
remote == <<base, tip>>
lander == <<lphase, lq, lbatch, lep, ltip, tries>>
vars == <<queue, landed, epoch, base, tip, lphase, lq, lbatch, lep, ltip, tries, events, badpush>>

Range(s) == {s[i] : i \in 1..Len(s)}

\* The queue still starts with the batch, at the epoch the lander holds.
Fresh(b) == epoch = lep /\ Len(queue) >= Len(b) /\ SubSeq(queue, 1, Len(b)) = b

\* The lander's steps in the order the variant takes them.
PushFrom == IF Broken = "reportfirst" THEN "reported" ELSE "built"
PushTo == IF Broken = "reportfirst" THEN "idle" ELSE "pushed"
ReportFrom == IF Broken = "reportfirst" THEN "built" ELSE "pushed"
ReportTo == IF Broken = "reportfirst" THEN "reported" ELSE "idle"

TypeOK ==
  /\ queue \in Seq(Cards) /\ Len(queue) <= Cardinality(Cards)
  /\ landed \subseteq Cards
  /\ epoch \in 0..MaxEpoch
  /\ base \subseteq Cards
  /\ tip \in Nat
  /\ lphase \in {"idle", "read", "built", "pushed", "reported"}
  /\ lep \in 0..MaxEpoch
  /\ tries \in 0..1
  /\ events \in 0..MaxEvents
  /\ badpush \in BOOLEAN

Init ==
  /\ queue = <<>> /\ landed = {} /\ epoch = 0
  /\ base = {} /\ tip = 0
  /\ lphase = "idle" /\ lq = <<>> /\ lbatch = <<>> /\ lep = 0 /\ ltip = 0 /\ tries = 0
  /\ events = 0 /\ badpush = FALSE

\* ---- the lander (land.go) ----

\* Read: the queue, the epoch and the tip. The caller's epoch cep (its --epoch,
\* or the store's when it gives none) is held to the store's here, before any
\* git (cmdLand's epoch check); latepoch skips it.
Read ==
  \E cep \in 0..MaxEpoch :
    /\ lphase = "idle" /\ Len(queue) > 0
    /\ Broken = "latepoch" \/ cep = epoch
    /\ lphase' = "read" /\ lq' = queue /\ lep' = cep /\ ltip' = tip /\ tries' = 0
    /\ UNCHANGED <<queue, landed, epoch, base, tip, lbatch, events, badpush>>

\* Build: the heads merged in queue order, ended by the first card that stops it.
Build ==
  /\ lphase = "read"
  /\ \E n \in 1..Len(lq) : lbatch' = SubSeq(lq, 1, n)
  /\ lphase' = "built"
  /\ UNCHANGED <<queue, landed, epoch, base, tip, lq, lep, ltip, tries, events, badpush>>

\* Push: the queue head and the epoch read again (queueHead), refused when
\* stale; a moved tip is rejected and the batch rebuilt on the new tip once,
\* then given up (the rejected fact); else the base takes the batch (a batch
\* whose heads the base holds already is a push of nothing).
Push ==
  /\ lphase = PushFrom
  /\ UNCHANGED <<queue, landed, epoch, lq, lep, events>>
  /\ IF Broken \notin {"latepoch", "reportfirst"} /\ ~Fresh(lbatch)
     THEN /\ lphase' = "idle"
          /\ UNCHANGED <<base, tip, lbatch, ltip, tries, badpush>>
     ELSE IF ltip # tip
     THEN IF tries < 1
          THEN /\ ltip' = tip /\ tries' = tries + 1
               /\ UNCHANGED <<lphase, base, tip, lbatch, badpush>>
          ELSE /\ lphase' = "idle"
               /\ UNCHANGED <<base, tip, lbatch, ltip, tries, badpush>>
     ELSE /\ base' = base \cup Range(lbatch)
          /\ tip' = IF Range(lbatch) \subseteq base THEN tip ELSE tip + 1
          /\ ltip' = tip'
          /\ badpush' = (badpush \/ lep # epoch)
          /\ lphase' = PushTo
          /\ UNCHANGED <<lbatch, tries>>

\* Report: one store step (landStep), fenced to the epoch held, that lands the
\* batch only while the queue starts with it; refused, it writes nothing and
\* the lander says LAND FAILED. noguard lands the first Len(lbatch) queued,
\* whatever they are (merge --batch n alone).
Report ==
  /\ lphase = ReportFrom
  /\ LET n == Len(lbatch) IN
       IF Broken = "noguard"
       THEN IF epoch = lep /\ Len(queue) >= n
            THEN /\ landed' = landed \cup Range(SubSeq(queue, 1, n))
                 /\ queue' = SubSeq(queue, n + 1, Len(queue))
            ELSE UNCHANGED <<queue, landed>>
       ELSE IF Fresh(lbatch)
            THEN /\ landed' = landed \cup Range(lbatch)
                 /\ queue' = SubSeq(queue, n + 1, Len(queue))
            ELSE UNCHANGED <<queue, landed>>
  /\ lphase' = ReportTo
  /\ UNCHANGED <<epoch, base, tip, lq, lbatch, lep, ltip, tries, events, badpush>>

Land == Read \/ Build \/ Push \/ Report

\* ---- the outside, each event counted ----

Accept ==
  \E c \in Cards, i \in 1..(Len(queue) + 1) :
    /\ c \notin Range(queue) /\ c \notin landed
    /\ queue' = SubSeq(queue, 1, i - 1) \o <<c>> \o SubSeq(queue, i, Len(queue))
    /\ UNCHANGED <<landed, epoch>> /\ UNCHANGED remote /\ UNCHANGED lander

Return ==
  \E c \in Range(queue) :
    /\ queue' = SelectSeq(queue, LAMBDA x : x # c)
    /\ UNCHANGED <<landed, epoch>> /\ UNCHANGED remote /\ UNCHANGED lander

OtherLand ==
  /\ Len(queue) > 0
  /\ base' = base \cup {Head(queue)} /\ tip' = tip + 1
  /\ landed' = landed \cup {Head(queue)} /\ queue' = Tail(queue)
  /\ UNCHANGED epoch /\ UNCHANGED lander

Clear ==
  /\ epoch < MaxEpoch
  /\ epoch' = epoch + 1 /\ queue' = <<>> /\ landed' = {}
  /\ UNCHANGED remote /\ UNCHANGED lander

MoveBase ==
  /\ tip' = tip + 1
  /\ UNCHANGED store /\ UNCHANGED base /\ UNCHANGED lander

Crash ==
  /\ lphase # "idle"
  /\ lphase' = "idle" /\ tries' = 0
  /\ UNCHANGED store /\ UNCHANGED remote /\ UNCHANGED <<lq, lbatch, lep, ltip>>

Outside ==
  /\ events < MaxEvents
  /\ events' = events + 1
  /\ UNCHANGED badpush
  /\ (Accept \/ Return \/ OtherLand \/ Clear \/ MoveBase \/ Crash)

Next == Land \/ Outside

Spec == Init /\ [][Next]_vars

\* The lander runs again whenever it can: the coordinator re-runs land.
FairSpec == Spec /\ WF_vars(Land)

\* ---- the rules ----

LandedInBase == landed \subseteq base

PushHoldsTheEpoch == ~badpush

LandsInOrder ==
  [][LET new == landed' \ landed IN
       /\ new \subseteq Range(queue)
       /\ \A i \in 1..Len(queue) : queue[i] \in new => \A j \in 1..(i - 1) : queue[j] \in new
    ]_vars

Recovers == <>[](\A c \in Range(queue) : c \notin base)

\* Reachability (a reversed witness, written to be false where the design must
\* reach): a batch in the base and still queued, the lander idle: stranded.
ReachStranded == ~(lphase = "idle" /\ \E c \in Range(queue) : c \in base)
=============================================================================
