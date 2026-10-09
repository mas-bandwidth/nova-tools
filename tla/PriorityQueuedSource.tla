------------------------ MODULE PriorityQueuedSource ------------------------
EXTENDS PriorityQueued
\* Actual source Take/packet observations map primary ids to these roles.
QueueSourceObserved == <<"later">>
QueueSourceStarts == IF Broken = "reverse" THEN <<"earlier">> ELSE IF Broken = "blocked" THEN <<"later", "earlier">> ELSE QueueSourceObserved
QueueReplayNext == /\ Len(starts) < Len(QueueSourceStarts)
                   /\ LaunchObserved(QueueSourceStarts[Len(starts)+1],FALSE)
QueueReplaySpec == QueueInit /\ [][QueueReplayNext]_qvars /\ WF_qvars(QueueReplayNext)
QueueReplayFinishes == <> (Len(starts) = Len(QueueSourceStarts))
=============================================================================
