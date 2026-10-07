-------------------------------- MODULE Land --------------------------------
\* nova-sprint land (cmd/nova-sprint/land.go): the coordinator's landing step,
\* which pushes a batch of a stream's merge queue to a remote base and then
\* reports it to the store through the merge step. The push and the report
\* are two operations on two systems; this module is that sequence, the
\* outside events that can come between them, and the fences land keeps.
\*
\* A HEAD. A card keeps its id and its epoch across attempts: a return, a
\* rework and a new accept put the same id back in the queue at a new head.
\* A head is the pair <<card, attempt>>; the base holds heads, the store
\* records a card landed at a head, and the lander pins the heads it read.
\*
\* THE STATE.
\*   queue     the stream's merge queue in the store: card ids in work order
\*             (the cards queued before the first stuck one)
\*   att       each card's current attempt in the store
\*   landed    the heads the store records landed, at the store's epoch
\*   epoch     the store's epoch (a clear moves it and empties the tables)
\*   base      the heads the remote base holds
\*   tip       the remote base's tip, a counter: every change to the base moves it
\*   lphase    the lander's step: idle, read, built, checked, pushed
\*             (reportfirst: idle, read, built, reported)
\*   lq        the queue as the lander read it, as heads (pinned)
\*   lbatch    the batch it built: a prefix of lq (a card that stops the batch,
\*             a conflict or a missing head, ends it, so any prefix)
\*   lref      the head that ended the batch, refused, and lkind whose refusal
\*             it is: "own" (a file of the card's own conflicts, the lander's
\*             checks, the tree gate its base passes: sprint.RefusalWay) or
\*             "lander" (a ledger it could not resolve, a head origin does not
\*             hold); lref is <<>> when no card refused the batch
\*   ready     the cards a refusal of their own head reworked: off the queue,
\*             their next attempt waiting (sprint's landRefused)
\*   review    the cards a refusal of their own head found at their bound:
\*             back in review with the bound's judgment
\*   stop      the stream's stop by a refusal: "none", or "lander" (the
\*             lander's own failure, for a mind; resumed by the outside)
\*   carried   a ghost: the heads a rework carries, the refused attempt's,
\*             which staging puts on the tip (sprint.BaseOf)
\*   lep       the epoch the lander holds (the caller's --epoch, else the one read)
\*   lrep      the store's epoch when the lander read it
\*   ltip      the base's tip the batch was built on
\*   tries     the rebuilds on a moved base (one is allowed)
\*   events    the outside events so far, bounded by MaxEvents
\*   ownstop   a ghost: a refusal of a card's own head stopped the stream
\*   badcaller a ghost: a push was made for a caller whose epoch the store was
\*             not at when the lander read it
\*   stalepush a ghost: a push was made after the store left the epoch the
\*             lander holds (a clear between the check and the push)
\*   stalerec  a ghost: a report recorded a landing at an epoch other than
\*             the one the lander holds
\*   lpushed   a ghost: the heads this lander itself pushed for the store's
\*             epoch (a clear empties it, and a push made for an epoch the
\*             store has left adds nothing), so the stranded witness names the
\*             lander's own push in this epoch and not another lander's
\*             landing re-queued, nor a stale push
\*
\* THE ACTIONS. The lander's, each a call in land.go: Read (the queue with its
\* heads, the epoch and the tip; a caller's epoch the store is not at is
\* refused here, before any git: cmdLand), Build, Check (the queue's heads and
\* the epoch read again just before the push: queueHead), Push (git push: a
\* moved tip is rejected and the batch rebuilt and checked again once, then
\* given up), Report (one store step, lander.step, fenced to the epoch held,
\* that lands the batch only while the queue still starts with exactly the
\* heads pushed), Refuse (the conflict fact of the card that ended the batch,
\* one store step fenced and guarded as the report is: lander.conflict; a
\* refusal of the card's own head takes it off the queue, reworked at a new
\* attempt with the refused head carried, or into review at its bound, and
\* the lander goes on to the rest of the queue in the same pass; the lander's
\* own failure stops the stream). The outside's: Accept (a card queued anywhere, by its
\* score), Return (a card taken off the queue), Rework (a queued card replaced
\* by its next attempt, keeping its id, its epoch and its place: return,
\* rework, deal, take, finish, ask, reads and accept between two of the
\* lander's calls), OtherLand (another lander lands the queue's head,
\* correctly), Clear (the epoch moves, the store's tables empty), MoveBase
\* (another commit on the base), Crash (the lander stops at any step), Resume
\* (a mind resumes a stream the lander's own failure stopped).
\*
\* THE RULES.
\*   LandedInBase: the store records a card landed only at a head the base
\*     holds.
\*   LandsInOrder: a card lands only with every card ahead of it in the queue.
\*   CallerEpochCheckedBeforePush: no push is made for a caller whose epoch
\*     the store was not at when the lander read it.
\*   ReportHoldsTheEpoch: no report records a landing at an epoch the lander
\*     does not hold.
\*   LeavesMergeRightly: a step of the lander takes a card off the queue only
\*     landed, reworked (ready at its next attempt, the refused head carried)
\*     or into review at its bound.
\*   NeverStoppedByOwn: the stream is never stopped by a refusal of its own
\*     card's head.
\*   CarriedHead: a reworked card's next attempt carries the head refused.
\*   Recovers: a batch pushed and not reported (a crash, or a report the guard
\*     refused) is recorded by running the lander again: under fairness, once
\*     the outside is quiet, no queued card's current head stays in the base
\*     while the stream is not stopped.
\* WHAT IS NOT ATTAINABLE, AND IS NOT CLAIMED. The check and the push are two
\* calls, and a git push cannot be fenced by the store: a clear between them
\* makes a push for an epoch the store has just left (ReachStalePush, a reversed
\* witness, reaches it). What is attained is that nothing is recorded for it
\* (ReportHoldsTheEpoch, LandedInBase); the external push itself is not undone,
\* and after a clear the store holds nothing to report it to. The same window
\* lets a rework replace a card's head after the check: the report's guard,
\* which compares heads and not ids, refuses it.
\* Reversed witnesses: ReachStranded (the lander's own push left unreported,
\* so Recovers is not vacuous), ReachStalePush, ReachLandsOn (a card behind
\* a reworked one landed) and ReachReview (a card at its bound in review).
\*
\* WHAT RECOVERS DOES NOT COVER. It is proved once the outside goes quiet
\* (the instance caps outside events at MaxEvents), so it says nothing of an
\* outside that never stops: (a) a lander that crashes between the push and
\* the report on every run never records the batch (its heads stay in the base
\* and its cards stay queued, so the first run that is not cut short records
\* it); (b) one rebuild on a moved base and then the lander gives up (tries <
\* 1): a base that moves twice inside every read-to-push window makes every
\* run give up, with nothing pushed and the cards still queued, so running it
\* again loses nothing; (c) a stream the lander's own failure stopped waits
\* for a mind to resume it, and until then lands nothing, so Recovers is
\* claimed for the stream while it is not stopped.
\*
\* Broken: "none" is the design.
\*   "reportfirst" reports the batch before it is pushed (LandedInBase fails).
\*   "latepoch" checks the caller's epoch only at the report, after the push
\*     (CallerEpochCheckedBeforePush fails).
\*   "noguard" reports `merge --batch n` with no guard that the queue still
\*     starts with the batch (LandedInBase fails: a card accepted ahead of the
\*     batch between the push and the report lands unpushed).
\*   "idguard" guards with the cards' ids and not their heads (LandedInBase
\*     fails: a card reworked between the check and the report lands at the
\*     new head while the base holds the old one).
\*   "noepoch" reports with the heads guarded and no fence on the epoch
\*     (ReportHoldsTheEpoch fails: a push, a clear, the same head accepted
\*     again in the new epoch, and the old run's report records it there).
\*   "ownstops" stops the stream on a refusal of the card's own head, as the
\*     merge step did before 2026-10-06 (NeverStoppedByOwn fails).
\*   "nocarry" reworks a refused card without carrying its head
\*     (CarriedHead fails).
\*
\* WHAT IS NOT MODELLED. The check (--check), the red and rejected facts:
\* those refusals are the lander going idle with the store unchanged. The
\* bound is MaxAttempts, the attempts a card's brief may run, with no brief
\* replaced and no same-refusal-twice bound (refmodel and the engine's tests
\* hold those). Files outside a card's PATHS go back to review for the widen
\* rule in the code; here every refusal of the card's own head is reworked
\* or bound. A card's attempts are counted across a clear, so a head is never
\* reused by another card. One stream, one base.
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Cards, MaxAttempts, MaxEpoch, MaxEvents, Broken

VARIABLES queue, att, landed, epoch, base, tip,
          lphase, lq, lbatch, lep, lrep, ltip, tries,
          events, badcaller, stalepush, stalerec, lpushed,
          lref, lkind, ready, review, stop, carried, ownstop

store == <<queue, att, landed, epoch, ready, review, stop>>
remote == <<base, tip>>
lander == <<lphase, lq, lbatch, lep, lrep, ltip, tries, lref, lkind>>
ghosts == <<badcaller, stalepush, stalerec, lpushed, carried, ownstop>>
vars == <<queue, att, landed, epoch, base, tip, lphase, lq, lbatch, lep, lrep, ltip, tries,
          events, badcaller, stalepush, stalerec, lpushed,
          lref, lkind, ready, review, stop, carried, ownstop>>

Heads == Cards \X (1..MaxAttempts)

Range(s) == {s[i] : i \in 1..Len(s)}

\* A card's current head in the store.
Current(c) == <<c, att[c]>>

\* The queue as heads, and a sequence of heads as ids.
QHeads == [i \in 1..Len(queue) |-> Current(queue[i])]
Ids(b) == [i \in 1..Len(b) |-> b[i][1]]

\* The queue still starts with exactly these heads, at the epoch the lander
\* holds (headWhy: ids, heads and attempts).
Fresh(b) == epoch = lep /\ Len(queue) >= Len(b) /\ SubSeq(QHeads, 1, Len(b)) = b

\* idguard's check: the ids only.
FreshIds(b) == epoch = lep /\ Len(queue) >= Len(b) /\ SubSeq(queue, 1, Len(b)) = Ids(b)

Guard(b) == IF Broken = "idguard" THEN FreshIds(b) ELSE Fresh(b)

\* noepoch's report guard: the heads, no epoch fence.
FreshAnyEpoch(b) == Len(queue) >= Len(b) /\ SubSeq(QHeads, 1, Len(b)) = b

\* The lander's steps in the order the variant takes them.
PushFrom == IF Broken = "reportfirst" THEN "reported" ELSE "checked"
PushTo == IF Broken = "reportfirst" THEN "idle" ELSE "pushed"
RebuildTo == IF Broken = "reportfirst" THEN "reported" ELSE "built"
ReportFrom == IF Broken = "protected-early" THEN "pr-open" ELSE IF Broken = "reportfirst" THEN "built" ELSE "pushed"

\* Protected landing pushes/opens a PR; only the outside merge puts heads in base.
Protected == Broken \in {"protected", "protected-early"}
ReportTo == IF Broken = "reportfirst" THEN "reported" ELSE "idle"

TypeOK ==
  /\ queue \in Seq(Cards) /\ Len(queue) <= Cardinality(Cards)
  /\ att \in [Cards -> 1..MaxAttempts]
  /\ landed \subseteq Heads
  /\ epoch \in 0..MaxEpoch
  /\ base \subseteq Heads
  /\ tip \in Nat
  /\ lphase \in {"idle", "read", "built", "checked", "pushed", "reported", "refusing", "pr-open"}
  /\ lep \in 0..MaxEpoch
  /\ lrep \in 0..MaxEpoch
  /\ tries \in 0..1
  /\ events \in 0..MaxEvents
  /\ badcaller \in BOOLEAN
  /\ stalepush \in BOOLEAN
  /\ stalerec \in BOOLEAN
  /\ lpushed \subseteq Heads
  /\ lref \in {<<>>} \cup Heads
  /\ lkind \in {"own", "lander"}
  /\ ready \subseteq Cards /\ review \subseteq Cards
  /\ stop \in {"none", "lander", "own"}
  /\ carried \subseteq Heads
  /\ ownstop \in BOOLEAN

Init ==
  /\ queue = <<>> /\ att = [c \in Cards |-> 1] /\ landed = {} /\ epoch = 0
  /\ base = {} /\ tip = 0
  /\ lphase = "idle" /\ lq = <<>> /\ lbatch = <<>> /\ lep = 0 /\ lrep = 0 /\ ltip = 0 /\ tries = 0
  /\ events = 0 /\ badcaller = FALSE /\ stalepush = FALSE /\ stalerec = FALSE /\ lpushed = {}
  /\ lref = <<>> /\ lkind = "own" /\ ready = {} /\ review = {} /\ stop = "none"
  /\ carried = {} /\ ownstop = FALSE

\* ---- the lander (land.go) ----

\* Read: the queue with its heads, the epoch and the tip. The caller's epoch
\* cep (its --epoch, or the store's when it gives none) is held to the
\* store's here, before any git (cmdLand); latepoch skips it.
Read ==
  \E cep \in 0..MaxEpoch :
    /\ lphase = "idle" /\ Len(queue) > 0 /\ stop = "none"
    /\ Broken = "latepoch" \/ cep = epoch
    /\ lphase' = "read" /\ lq' = QHeads /\ lep' = cep /\ lrep' = epoch /\ ltip' = tip /\ tries' = 0
    /\ UNCHANGED store /\ UNCHANGED remote /\ UNCHANGED <<lbatch, lref, lkind>>
    /\ UNCHANGED events /\ UNCHANGED ghosts

\* Build: the pinned heads merged in queue order, ended by the first card
\* that stops it: refused (lref, its own or the lander's failure, lkind) or
\* none. A head the base holds merges as nothing, so it is never refused.
\* With no card merged before the refused one there is nothing to push: the
\* refusal is reported at once (lander.batch, the conflict after the loop).
Build ==
  /\ lphase = "read"
  /\ \E n \in 0..Len(lq), r \in BOOLEAN, k \in {"own", "lander"} :
       /\ lbatch' = SubSeq(lq, 1, n)
       /\ r => n < Len(lq) /\ lq[n + 1] \notin base /\ Broken # "reportfirst"
       /\ n > 0 \/ r
       /\ lref' = IF r THEN lq[n + 1] ELSE <<>>
       /\ lkind' = k
       /\ lphase' = IF n = 0 THEN "refusing" ELSE "built"
  /\ UNCHANGED store /\ UNCHANGED remote /\ UNCHANGED <<lq, lep, lrep, ltip, tries>>
  /\ UNCHANGED events /\ UNCHANGED ghosts

\* Check: the queue's heads and the epoch read again just before the push
\* (queueHead); a stale batch is refused, nothing pushed. latepoch checks
\* nothing before the push; reportfirst has no check.
Check ==
  /\ lphase = "built" /\ Broken # "reportfirst"
  /\ lphase' = IF Broken = "latepoch" \/ Guard(lbatch) THEN "checked" ELSE "idle"
  /\ UNCHANGED store /\ UNCHANGED remote /\ UNCHANGED <<lq, lbatch, lep, lrep, ltip, tries, lref, lkind>>
  /\ UNCHANGED events /\ UNCHANGED ghosts

\* Push: git push, which the store cannot fence. A moved tip is rejected and
\* the batch rebuilt on the new tip, to be checked again, once; then given up
\* (the rejected fact). Else the base takes the batch's pinned heads (a batch
\* the base holds already is a push of nothing).
Push ==
  /\ lphase = PushFrom
  /\ UNCHANGED store /\ UNCHANGED <<lq, lep, lrep, lbatch, lref, lkind>> /\ UNCHANGED events
  /\ UNCHANGED <<stalerec, carried, ownstop>>
  /\ IF ltip # tip
     THEN IF tries < 1
          THEN /\ ltip' = tip /\ tries' = tries + 1 /\ lphase' = RebuildTo
               /\ UNCHANGED remote /\ UNCHANGED <<badcaller, stalepush, lpushed>>
          ELSE /\ lphase' = "idle"
               /\ UNCHANGED remote /\ UNCHANGED <<ltip, tries, badcaller, stalepush, lpushed>>
     ELSE /\ base' = IF Protected THEN base ELSE base \cup Range(lbatch)
          /\ tip' = IF Protected \/ Range(lbatch) \subseteq base THEN tip ELSE tip + 1
          /\ ltip' = tip'
          /\ badcaller' = (badcaller \/ lep # lrep)
          /\ stalepush' = (stalepush \/ lep # epoch)
          /\ lpushed' = IF lep = epoch THEN lpushed \cup Range(lbatch) ELSE lpushed
          /\ lphase' = IF Protected /\ ~(Range(lbatch) \subseteq base) THEN "pr-open" ELSE PushTo
          /\ UNCHANGED tries

\* Report: one store step (lander.step), fenced to the epoch held, that lands
\* the batch only while the queue starts with exactly the heads pushed; the
\* store records each card at its current head. Refused, it writes nothing and
\* the lander says LAND FAILED. noguard lands the first Len(lbatch) queued,
\* whatever they are (merge --batch n alone); idguard compares ids; noepoch
\* compares heads with no epoch fence.
Report ==
  /\ lphase = ReportFrom
  /\ LET n == Len(lbatch)
         ok == CASE Broken = "noguard" -> epoch = lep /\ Len(queue) >= n
                 [] Broken = "noepoch" -> FreshAnyEpoch(lbatch)
                 [] OTHER -> Guard(lbatch)
     IN /\ IF ok
           THEN /\ landed' = landed \cup {Current(queue[i]) : i \in 1..n}
                /\ queue' = SubSeq(queue, n + 1, Len(queue))
                /\ stalerec' = (stalerec \/ epoch # lep)
           ELSE UNCHANGED <<queue, landed, stalerec>>
        \* the refused card is reported after the batch it ended, and only
        \* after one that landed (a report refused is LAND FAILED: nothing more)
        /\ lphase' = IF ok /\ lref # <<>> THEN "refusing" ELSE ReportTo
  /\ UNCHANGED <<att, epoch, ready, review, stop>> /\ UNCHANGED remote
  /\ UNCHANGED <<lq, lbatch, lep, lrep, ltip, tries, lref, lkind>>
  /\ UNCHANGED events /\ UNCHANGED <<badcaller, stalepush, lpushed, carried, ownstop>>

\* Refuse: the conflict fact of the refused card, one store step fenced to the
\* epoch held and guarded to plan only while the queue starts with that card
\* at the head the lander pinned (lander.conflict, lander.step); refused, it
\* writes nothing. A refusal of the card's own head never stops the stream:
\* the card leaves the queue, reworked at its next attempt with the refused
\* head carried (ready), or at its bound into review; the stream stays
\* unstopped, so the next Read lands the rest of the queue in the same pass.
\* The lander's own failure stops the stream on the card, for a mind.
\* ownstops stops the stream on the card's own refusal too; nocarry carries
\* nothing.
Refuse ==
  /\ lphase = "refusing"
  /\ LET c == lref[1]
         ok == Fresh(<<lref>>)
         own == lkind = "own" /\ Broken # "ownstops"
     IN IF ~ok
        THEN UNCHANGED <<queue, att, ready, review, stop, carried, ownstop>>
        ELSE IF own
        THEN /\ queue' = Tail(queue)
             /\ IF att[c] < MaxAttempts
                THEN /\ att' = [att EXCEPT ![c] = @ + 1] /\ ready' = ready \cup {c}
                     /\ carried' = IF Broken = "nocarry" THEN carried ELSE carried \cup {lref}
                     /\ UNCHANGED review
                ELSE /\ review' = review \cup {c}
                     /\ UNCHANGED <<att, ready, carried>>
             /\ UNCHANGED <<stop, ownstop>>
        ELSE /\ stop' = IF lkind = "own" THEN "own" ELSE "lander"
             /\ ownstop' = (ownstop \/ lkind = "own")
             /\ UNCHANGED <<queue, att, ready, review, carried>>
  /\ lphase' = "idle" /\ lref' = <<>>
  /\ UNCHANGED <<landed, epoch>> /\ UNCHANGED remote
  /\ UNCHANGED <<lq, lbatch, lep, lrep, ltip, tries, lkind>>
  /\ UNCHANGED events /\ UNCHANGED <<badcaller, stalepush, stalerec, lpushed>>

Land == Read \/ Build \/ Check \/ Push \/ Report \/ Refuse

\* ---- the outside, each event counted ----

\* A card queued anywhere: a new one, or a reworked or reviewed one accepted
\* again (its attempt worked, read and accepted between two of the lander's
\* calls).
Accept ==
  \E c \in Cards, i \in 1..(Len(queue) + 1) :
    /\ c \notin Range(queue) /\ Current(c) \notin landed
    /\ queue' = SubSeq(queue, 1, i - 1) \o <<c>> \o SubSeq(queue, i, Len(queue))
    /\ ready' = ready \ {c} /\ review' = review \ {c}
    /\ UNCHANGED <<att, landed, epoch, stop>> /\ UNCHANGED remote /\ UNCHANGED lander

Return ==
  \E c \in Range(queue) :
    /\ queue' = SelectSeq(queue, LAMBDA x : x # c)
    /\ UNCHANGED <<att, landed, epoch, ready, review, stop>> /\ UNCHANGED remote /\ UNCHANGED lander

Rework ==
  \E c \in Range(queue) :
    /\ att[c] < MaxAttempts
    /\ att' = [att EXCEPT ![c] = @ + 1]
    /\ UNCHANGED <<queue, landed, epoch, ready, review, stop>> /\ UNCHANGED remote /\ UNCHANGED lander

\* Another lander lands the queue's head, correctly: never on a stopped stream.
OtherLand ==
  /\ Len(queue) > 0 /\ stop = "none"
  /\ base' = base \cup {Current(Head(queue))} /\ tip' = tip + 1
  /\ landed' = landed \cup {Current(Head(queue))} /\ queue' = Tail(queue)
  /\ UNCHANGED <<att, epoch, ready, review, stop>> /\ UNCHANGED lander

Clear ==
  /\ epoch < MaxEpoch
  /\ epoch' = epoch + 1 /\ queue' = <<>> /\ landed' = {}
  /\ ready' = {} /\ review' = {} /\ stop' = "none"
  /\ UNCHANGED att /\ UNCHANGED remote /\ UNCHANGED lander

\* A mind resumes a stream the lander's own failure stopped.
Resume ==
  /\ stop # "none" /\ stop' = "none"
  /\ UNCHANGED <<queue, att, landed, epoch, ready, review>> /\ UNCHANGED remote /\ UNCHANGED lander

MoveBase ==
  /\ tip' = tip + 1
  /\ UNCHANGED store /\ UNCHANGED base /\ UNCHANGED lander

Crash ==
  /\ lphase # "idle"
  /\ lphase' = "idle" /\ tries' = 0 /\ lref' = <<>>
  /\ UNCHANGED store /\ UNCHANGED remote /\ UNCHANGED <<lq, lbatch, lep, lrep, ltip, lkind>>

Outside ==
  /\ events < MaxEvents
  /\ events' = events + 1
  /\ UNCHANGED <<badcaller, stalepush, stalerec, carried, ownstop>>
  /\ (Accept \/ Return \/ Rework \/ OtherLand \/ Clear \/ MoveBase \/ Crash \/ Resume)
  /\ lpushed' = IF epoch' # epoch THEN {} ELSE lpushed

\* A forge's outside merge event, observed by the next protected poll. Waiting
\* for approval has no fairness assumption: an unmerged PR may wait forever.
\* This abstraction covers one batch's safety/head/epoch fences, not API retry
\* identity; the pure remote and command tests cover durable URL reuse.
ProtectedMerge ==
  /\ Protected /\ lphase = "pr-open"
  /\ base' = base \cup Range(lbatch)
  /\ tip' = IF Range(lbatch) \subseteq base THEN tip ELSE tip + 1
  /\ lphase' = "pushed"
  /\ UNCHANGED store /\ UNCHANGED <<lq, lbatch, lep, lrep, ltip, tries, lref, lkind>>
  /\ UNCHANGED events /\ UNCHANGED ghosts

Next == Land \/ Outside \/ ProtectedMerge

Spec == Init /\ [][Next]_vars

\* The lander runs again whenever it can: the coordinator re-runs land.
FairSpec == Spec /\ WF_vars(Land)

\* Re-reading a recorded merged PR whose heads are in base needs no new approval.
\* Once a merged PR is observed, fair landing cycles report it or a changed head/epoch
\* refuses it. No property forces an outside approval/merge to happen.
ProtectedFairSpec == Spec /\ WF_vars(Land)
ProtectedMergedEventuallyLands ==
  \A h \in Heads :
    (Protected /\ lphase = "pushed" /\ h \in Range(lbatch) /\ Fresh(lbatch))
    ~> (h \in landed \/ h # Current(h[1]) \/ h[1] \notin Range(queue) \/ epoch # lep)
ReachProtectedLanded == ~(Protected /\ landed # {})

\* ---- the rules ----

LandedInBase == landed \subseteq base

LandsInOrder ==
  [][LET new == {h[1] : h \in landed' \ landed} IN
       /\ new \subseteq Range(queue)
       /\ \A i \in 1..Len(queue) : queue[i] \in new => \A j \in 1..(i - 1) : queue[j] \in new
    ]_vars

CallerEpochCheckedBeforePush == ~badcaller

ReportHoldsTheEpoch == ~stalerec

Recovers == <>[](stop = "none" => \A c \in Range(queue) : Current(c) \notin base)

\* A step of the lander takes a card off the queue only landed, reworked at
\* its next attempt with the refused head carried, or into review at its bound.
LeavesMergeRightly ==
  [][Land => \A c \in Range(queue) \ Range(queue') :
         \/ Current(c) \in landed'
         \/ c \in ready' /\ att'[c] = att[c] + 1
         \/ c \in review' /\ att[c] = MaxAttempts
    ]_vars

NeverStoppedByOwn == ~ownstop

CarriedHead == \A c \in ready : <<c, att[c] - 1>> \in carried

\* A card this lander pushed and landed while another, reworked by a refusal
\* of its own head, waits ready: the lander went on past it. Shortest: Accept,
\* Accept, Read, Build (the second refused), Check, Push, Report, Refuse.
ReachLandsOn == ~(\E c \in ready : \E h \in landed \cap lpushed : h[1] # c)

\* A card at its bound in review. Shortest: Accept, Rework, Read, Build,
\* Refuse.
ReachReview == review = {}

\* Reachability (reversed witnesses, written to be false where the design
\* must reach).
\* A head this lander pushed, its card still queued at it, the lander idle:
\* pushed and not reported (a crash between the push and the report, or a
\* report the guard refused). Shortest: Accept, Read, Build, Check, Push, Crash.
ReachStranded == ~(lphase = "idle" /\ \E c \in Range(queue) : Current(c) \in lpushed)

\* A push made after the store left the epoch the lander holds: a clear between
\* the check and the push. Shortest: Accept, Read, Build, Check, Clear, Push.
ReachStalePush == ~stalepush
=============================================================================
