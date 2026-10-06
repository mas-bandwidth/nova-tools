---------------------------- MODULE LanePause ----------------------------
(* A provider failure stops every lane (internal/friend/lanes.go holdDown,
   stopEvery, heldTurn, markerStep; docs/SPEC-FRIEND.md, one-shot lanes at
   parity). A lane's turn meets the provider's failure (out of funds; a rate
   limit when the row says pause_on=any): the lanes are held, every other turn
   under way is told to stop (its process group signalled) and its card kept in
   its lane's hand, and the pause marker is written, which may fail. The hold
   is the marker's only once it is written (marked); a marker a person clears
   (nova-friend resume) lifts it, and a hold whose marker was never written
   lasts until a person restarts the daemon. StopAll = FALSE is the reversed
   witness of lanes that only stop starting turns (the finding of
   opencode-lanes-parity-r2.w2); MarkAfterWrite = FALSE the witness of a hold
   marked as the marker's before the write succeeded. *)
EXTENDS Naturals, FiniteSets

CONSTANTS Lanes, Cards, None, Breaks, StopAll, MarkAfterWrite

VARIABLES run,      \* each lane: "idle", "running" (spending), "stopping" (told to stop, not yet back)
          card,     \* each lane's card in hand, or None
          done,     \* the cards whose reports are written
          failing,  \* the provider refuses every turn
          breaks,   \* how many times it has begun to
          held,     \* the lanes start nothing
          marker,   \* PAUSED stands in the state directory
          marked,   \* the daemon holds the hold to be the marker's
          heldOnce, \* a hold was taken
          cleared   \* a person brought the lanes up since the last hold

vars == <<run, card, done, failing, breaks, held, marker, marked, heldOnce, cleared>>

TypeOK == /\ run \in [Lanes -> {"idle", "running", "stopping"}]
          /\ card \in [Lanes -> Cards \cup {None}]
          /\ done \subseteq Cards
          /\ failing \in BOOLEAN /\ breaks \in 0..Breaks
          /\ held \in BOOLEAN /\ marker \in BOOLEAN /\ marked \in BOOLEAN
          /\ heldOnce \in BOOLEAN /\ cleared \in BOOLEAN

Init == /\ run = [l \in Lanes |-> "idle"]
        /\ card = [l \in Lanes |-> None]
        /\ done = {}
        /\ failing = FALSE /\ breaks = 0
        /\ held = FALSE /\ marker = FALSE /\ marked = FALSE
        /\ heldOnce = FALSE /\ cleared = FALSE

InHand == {card[l] : l \in Lanes} \ {None}

\* every turn under way but l's told to stop (stopEvery); none when StopAll is off
StopOthers(r, l) == IF StopAll
                    THEN [k \in Lanes |-> IF k # l /\ r[k] = "running" THEN "stopping" ELSE r[k]]
                    ELSE r

Take(l, c) == /\ run[l] = "idle" /\ card[l] = None
              /\ c \notin done /\ c \notin InHand
              /\ card' = [card EXCEPT ![l] = c]
              /\ UNCHANGED <<run, done, failing, breaks, held, marker, marked, heldOnce, cleared>>

Start(l) == /\ run[l] = "idle" /\ card[l] # None /\ ~held
            /\ run' = [run EXCEPT ![l] = "running"]
            /\ UNCHANGED <<card, done, failing, breaks, held, marker, marked, heldOnce, cleared>>

\* the turn ends with the card's report
Finish(l) == /\ run[l] = "running" /\ ~failing
             /\ done' = done \cup {card[l]}
             /\ card' = [card EXCEPT ![l] = None]
             /\ run' = [run EXCEPT ![l] = "idle"]
             /\ UNCHANGED <<failing, breaks, held, marker, marked, heldOnce, cleared>>

\* the turn meets the provider's failure: its card kept (limitedTurn); the first holds
\* the lanes, stops the others and writes the marker, which may fail (holdDown)
Fail(l) == /\ run[l] = "running" /\ failing
           /\ IF held
              THEN /\ run' = [run EXCEPT ![l] = "idle"]
                   /\ UNCHANGED <<held, marker, marked, heldOnce, cleared>>
              ELSE /\ run' = [StopOthers(run, l) EXCEPT ![l] = "idle"]
                   /\ held' = TRUE /\ heldOnce' = TRUE /\ cleared' = FALSE
                   /\ \E ok \in BOOLEAN : /\ marker' = (marker \/ ok)
                                          /\ marked' = IF MarkAfterWrite THEN (marker \/ ok) ELSE TRUE
           /\ UNCHANGED <<card, done, failing, breaks>>

\* a stopped turn comes back: its card stays in hand, counted toward nothing (heldTurn)
StopEnds(l) == /\ run[l] = "stopping"
               /\ run' = [run EXCEPT ![l] = "idle"]
               /\ UNCHANGED <<card, done, failing, breaks, held, marker, marked, heldOnce, cleared>>

\* the marker is the hold's truth (markerStep)
MarkerHolds == /\ marker /\ ~held
               /\ held' = TRUE /\ marked' = TRUE /\ heldOnce' = TRUE /\ cleared' = FALSE
               /\ run' = StopOthers(run, CHOOSE l \in Lanes : TRUE) \* none is the failing lane here
               /\ UNCHANGED <<card, done, failing, breaks, marker>>
MarkerLifts == /\ ~marker /\ marked /\ held
               /\ held' = FALSE /\ marked' = FALSE
               /\ UNCHANGED <<run, card, done, failing, breaks, marker, heldOnce, cleared>>

\* the world and the person
Break == /\ breaks < Breaks /\ ~failing
         /\ failing' = TRUE /\ breaks' = breaks + 1
         /\ UNCHANGED <<run, card, done, held, marker, marked, heldOnce, cleared>>
Pay == /\ failing /\ failing' = FALSE
       /\ UNCHANGED <<run, card, done, breaks, held, marker, marked, heldOnce, cleared>>
Resume == /\ marker /\ ~failing
          /\ marker' = FALSE /\ cleared' = TRUE
          /\ UNCHANGED <<run, card, done, failing, breaks, held, marked, heldOnce>>
\* a hold with no marker lasts until a person restarts the daemon (between turns)
Restart == /\ held /\ ~marker /\ ~marked /\ ~failing
           /\ \A l \in Lanes : run[l] = "idle"
           /\ held' = FALSE /\ cleared' = TRUE
           /\ UNCHANGED <<run, card, done, failing, breaks, marker, marked, heldOnce>>

\* every card reported and the provider well: nothing more happens
Ended == /\ done = Cards /\ ~failing /\ breaks = Breaks /\ UNCHANGED vars

Next == \/ \E l \in Lanes, c \in Cards : Take(l, c)
        \/ \E l \in Lanes : Start(l) \/ Finish(l) \/ Fail(l) \/ StopEnds(l)
        \/ MarkerHolds \/ MarkerLifts
        \/ Break \/ Pay \/ Resume \/ Restart
        \/ Ended

Fairness == /\ \A l \in Lanes : WF_vars(Start(l)) /\ WF_vars(Finish(l)) /\ WF_vars(Fail(l)) /\ WF_vars(StopEnds(l))
            /\ \A l \in Lanes : WF_vars(\E c \in Cards : Take(l, c))
            /\ WF_vars(MarkerHolds) /\ WF_vars(MarkerLifts)
            /\ WF_vars(Pay) /\ WF_vars(Resume) /\ WF_vars(Restart)

Spec == Init /\ [][Next]_vars /\ Fairness

\* while the lanes are held no turn spends: every one under way was told to stop
NoSpendWhileHeld == held => \A l \in Lanes : run[l] # "running"
\* nothing resumes unless a person brought it up (resume, or a restart when no marker stands)
ResumeOnlyByPerson == (heldOnce /\ ~held) => cleared
\* a card is never lost: done, in a lane's hand, or still to be taken; never two lanes'
OneHand == \A k, l \in Lanes : (k # l /\ card[k] # None) => card[k] # card[l]
\* every card ends with its report
Finished == <>(done = Cards)
=============================================================================
