------------------------------ MODULE LaneCheck ------------------------------
EXTENDS Naturals, Integers, FiniteSets, TLC

\* The machine in internal/sprint/friend_deadline.go: LiveLane, friendLive,
\* LaneChecked and Suppressed.Counted. One friend owns Cards. Judgment generators
\* supply any subset of Judgments, independently of the lane check. A condition
\* ending closes its open judgment; a continuing open judgment is never filtered.
\* Type and subject form the key: card lateness, friend-name stall, friend-row idle.
\* ReadersFull and ReadTierRule are outside this model. This is the lane check
\* of existing runs, not attempt lifecycle: new takes and redeals are outside.
\* Initial ages range independently over every below-cap age and the cap; ages
\* at/above the cap are equivalent. Both ready and working columns are checked.
\* Losing and regaining evidence, a quiet-gap-quiet span, and an epoch reset
\* remain possible before expiry; reset of a card clock is not needed for them.
CONSTANTS Cards, FriendKeys, Cap, FriendLaneLive, MaxClock, Broken
VARIABLES clock, column, ran, beatAge, beatRunning, seatAge, seatRunning,
          epoch, open, previous, countEpoch, suppressed, spans,
          noRise, noMiss, noStale, epochRestartOK

vars == <<clock, column, ran, beatAge, beatRunning, seatAge, seatRunning,
          epoch, open, previous, countEpoch, suppressed, spans,
          noRise, noMiss, noStale, epochRestartOK>>
\* FriendKeys has two distinct opaque type/subject keys: stall-on-name and
\* idle-on-row. Both use friendLive; their identities remain distinct in the
\* open set, previous quiets and count. Card lateness retains the card key.
Judgments == ({"late"} \X Cards) \cup FriendKeys
\* Ages are an exact freshness quotient: -1 is a future timestamp, and Live+1
\* represents every stale timestamp. A seat with age -2 has no stored timestamp;
\* production accepts that direct daemon observation without an age test.
Ages == -1..(FriendLaneLive + 1)
Fresh(age) == age >= 0 /\ age <= FriendLaneLive
Active(c) == column[c] \in {"ready", "working"}
Named(c) == (c \in beatRunning /\ Fresh(beatAge)) \/
            (c \in seatRunning /\ (seatAge = -2 \/ Fresh(seatAge)))
Live(c) == Active(c) /\ Named(c) /\ ran[c] < Cap
MachineNamed(c) ==
    (c \in beatRunning /\ (Broken = "StaleBeat" \/ Fresh(beatAge))) \/
    (c \in seatRunning /\ (seatAge = -2 \/ Broken = "StaleBeat" \/ Fresh(seatAge)))
MachineLive(c) == Active(c) /\ MachineNamed(c) /\ (Broken = "NoCap" \/ ran[c] < Cap)
\* Card lateness uses that card; both friend-level keys use any live card.
Quietable(lanes) == ({"late"} \X lanes) \cup (IF lanes = {} THEN {} ELSE FriendKeys)

Init ==
    /\ clock = 0
    /\ column \in [Cards -> {"ready", "working"}]
    /\ ran \in [Cards -> 0..Cap]
    /\ beatAge = FriendLaneLive + 1 /\ beatRunning = {}
    /\ seatAge = FriendLaneLive + 1 /\ seatRunning = {}
    /\ epoch = 0 /\ countEpoch = 0
    /\ open = {} /\ previous = {} /\ suppressed = 0 /\ spans = 0
    /\ noRise = TRUE /\ noMiss = TRUE /\ noStale = TRUE
    /\ epochRestartOK = TRUE

\* Each successful tick is atomic, as the stored heartbeat's count and quiets are.
\* The next clock value follows that tick. Environmental actions may occur any
\* number of times between ticks, including immediately before or after a clear.
Tick(wanted, machine, actual) ==
    /\ clock < MaxClock
    /\ LET candidates == wanted \ open
           kept == candidates \cap machine
           old == IF countEpoch = epoch THEN previous ELSE {}
           base == IF countEpoch = epoch THEN suppressed ELSE 0
           added == IF Broken = "CountEveryTick" THEN Cardinality(kept)
                    ELSE Cardinality(kept \ old)
           expected == candidates \cap actual
           raised == candidates \ kept
       IN /\ noRise' = (raised \cap expected = {})
          /\ noMiss' = (candidates \ expected \subseteq raised)
          /\ noStale' = (kept \subseteq expected)
          /\ open' = (open \cap wanted) \cup (candidates \ kept)
          /\ suppressed' = base + added
          /\ spans' = (IF countEpoch = epoch THEN spans ELSE 0) + Cardinality(kept \ old)
          /\ epochRestartOK' = (countEpoch = epoch \/ suppressed' = Cardinality(kept))
          /\ previous' = kept
    /\ countEpoch' = epoch
    /\ clock' = clock + 1
    /\ ran' = [c \in Cards |-> IF ran[c] < Cap THEN ran[c] + 1 ELSE Cap]
    /\ beatAge' = IF beatAge <= FriendLaneLive THEN beatAge + 1 ELSE beatAge
    /\ seatAge' = IF seatAge # -2 /\ seatAge <= FriendLaneLive THEN seatAge + 1 ELSE seatAge
    /\ UNCHANGED <<column, beatRunning, seatRunning, epoch>>

\* Beat includes the clock-skew outside event (a beat one tick in the future).
\* The two channels are independent: a stored report, or the daemon's seat, whose
\* timestamp can also be absent. IDs stand for friendStarted's ID/job/primary match.
Beat(channel, age, running) ==
    /\ clock < MaxClock
    /\ IF channel = "beat"
          THEN /\ age \in Ages
               /\ beatAge' = age /\ beatRunning' = running
               /\ UNCHANGED <<seatAge, seatRunning>>
          ELSE /\ seatAge' = age /\ seatRunning' = running
               /\ UNCHANGED <<beatAge, beatRunning>>
    /\ UNCHANGED <<clock, column, ran, epoch, open, previous, countEpoch,
                   suppressed, spans, noRise, noMiss, noStale, epochRestartOK>>
BeatStops ==
    /\ clock < MaxClock
    /\ beatRunning' = {} /\ seatRunning' = {}
    /\ UNCHANGED <<clock, column, ran, beatAge, seatAge, epoch, open, previous,
                   countEpoch, suppressed, spans, noRise, noMiss,
                   noStale, epochRestartOK>>
CardFinishes(c) ==
    /\ clock < MaxClock /\ Active(c)
    /\ column' = [column EXCEPT ![c] = "done"]
    /\ UNCHANGED <<clock, ran, beatAge, beatRunning, seatAge, seatRunning, epoch,
                   open, previous, countEpoch, suppressed, spans, noRise,
                   noMiss, noStale, epochRestartOK>>
Clear ==
    /\ clock < MaxClock /\ epoch = 0 /\ epoch' = 1
    /\ open' = {}
    \* Counted receives the old heartbeat: reset happens at the next tick.
    /\ UNCHANGED <<clock, column, ran, beatAge, beatRunning, seatAge, seatRunning,
                   previous, countEpoch, suppressed, spans, noRise,
                   noMiss, noStale, epochRestartOK>>
\* Compute the snapshot's lane sets once before enumerating proposed subsets.
\* They are independent of wanted and open, just as LiveLane is in the code.
Next == LET machine == Quietable({c \in Cards : MachineLive(c)})
            actual == Quietable({c \in Cards : Live(c)})
        IN (\E wanted \in SUBSET Judgments : Tick(wanted, machine, actual)) \/
        (\E channel \in {"beat", "seat"}, age \in Ages \cup {-2}, running \in SUBSET Cards :
            Beat(channel, age, running)) \/ BeatStops \/
        (\E c \in Cards : CardFinishes(c)) \/ Clear
Spec == Init /\ [][Next]_vars

TypeOK == /\ clock \in 0..MaxClock /\ column \in [Cards -> {"ready", "working", "done"}]
          /\ ran \in [Cards -> 0..Cap] /\ beatAge \in Ages /\ seatAge \in Ages \cup {-2}
          /\ beatRunning \subseteq Cards /\ seatRunning \subseteq Cards
          /\ epoch \in 0..1 /\ countEpoch \in 0..1 /\ countEpoch <= epoch
          /\ open \subseteq Judgments /\ previous \subseteq Judgments
          /\ suppressed \in Nat /\ spans \in Nat
          /\ noRise \in BOOLEAN /\ noMiss \in BOOLEAN /\ noStale \in BOOLEAN
          /\ epochRestartOK \in BOOLEAN
\* These concern the lane snapshot at the tick, not the later environment: neither
\* a new beat nor finishing a card retroactively changes a tick or an open judgment.
NoRiseOverLiveLane == noRise
NoMissedRise == noMiss
CountedOncePerSpan == suppressed = spans
StaleBeatNeverRevives == noStale
CountRestartsAtEpoch == epochRestartOK
=============================================================================
