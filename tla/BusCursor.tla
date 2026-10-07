--------------------------- MODULE BusCursor ---------------------------
EXTENDS Naturals, Sequences, FiniteSets, TLC

(*
   VARIABLES:
     stream: The sequence of messages on the bus.
     cursor: The current ID a waiter is at (the `after` ID).
     acked:  The set of message IDs acked by the recipient.

   ACTIONS:
     Send:   Append a message to the stream.
     Ack:    Mark a message as acked.
     Wait:   Advance the cursor past read messages.

   INVARIANTS:
     NoNoteSkipped:               All messages sent are eventually acked or will be in the stream.
     AdvanceOnlyPastAcknowledged: The cursor should not skip messages that are not acked/read?
     NoWriteWithoutAdvance:       ?
     OpenIsShownNotAnswered:      ?
     FurtherReadWins:             ?
     OneRunPerCheckout:           ?
*)

VARIABLES stream, cursor, acked

vars == <<stream, cursor, acked>>

Init == /\ stream = << >>
        /\ cursor = 0
        /\ acked = {}

Send == /\ stream' = Append(stream, Len(stream) + 1)
        /\ UNCHANGED <<cursor, acked>>

Ack == \E m \in {stream[i] : i \in 1..Len(stream)} \setminus acked :
         /\ acked' = acked \cup {m}
         /\ UNCHANGED <<stream, cursor>>

Wait == /\ cursor < Len(stream)
        /\ cursor' = Len(stream)
        /\ UNCHANGED <<stream, acked>>

Next == Send \/ Ack \/ Wait

NoNoteSkipped == \A i \in 1..Len(stream) : stream[i] \in acked

\* AdvanceOnlyPastAcknowledged: Cursor advances only past messages that have been read/acked.
AdvanceOnlyPastAcknowledged == \A i \in 1..cursor' : stream[i] \in acked

\* NoWriteWithoutAdvance: ?
\* OpenIsShownNotAnswered: ?
\* FurtherReadWins: ?
\* OneRunPerCheckout: ?

NoWriteWithoutAdvance == TRUE
OpenIsShownNotAnswered == TRUE
FurtherReadWins == TRUE
OneRunPerCheckout == TRUE

Spec == Init /\ [][Next]_vars
=============================================================================
