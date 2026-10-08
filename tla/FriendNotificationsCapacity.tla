------------------ MODULE FriendNotificationsCapacity ------------------
EXTENDS Naturals, FiniteSets
\* SPEC-FRIEND.md, Notifications: immutable full batches, one unread queue slot per
\* category, and a deferred report journal which does not own urgent intake.
\* The report category represents nonurgent report/notice input; both use the same
\* deferred slot and independent capacity guard against urgent input.
\* IDs stand for distinct bounded batches. Crash retains journals and bus sources;
\* queue acceptance is separate from processing and source receipt settlement.
CONSTANTS BadCapacity, BadHold
Kinds == {"report", "urgent"}
IDs == 1..2
VARIABLES source, queued, processed, acked, kind, id, accepted, report, due, success
vars == <<source, queued, processed, acked, kind, id, accepted, report, due, success>>
Init == /\ source = [k \in Kinds |-> IDs]
        /\ queued = [k \in Kinds |-> {}]
        /\ processed = [k \in Kinds |-> {}]
        /\ acked = [k \in Kinds |-> {}]
        /\ kind = "none" /\ id = 0 /\ accepted = FALSE
        /\ report = 0 /\ due = FALSE /\ success = FALSE
PrepareUrgent == /\ id = 0 /\ source["urgent"] # {}
                 /\ ~(BadHold /\ report # 0)
                 /\ \E chosen \in source["urgent"]:
                       /\ kind' = "urgent" /\ id' = chosen
                 /\ accepted' = FALSE
                 /\ UNCHANGED <<source, queued, processed, acked, report, due, success>>
PrepareReport == /\ id = 0 /\ report = 0 /\ source["report"] # {}
                 /\ \E chosen \in source["report"]:
                       /\ kind' = "report" /\ id' = chosen
                 /\ accepted' = FALSE
                 /\ UNCHANGED <<source, queued, processed, acked, report, due, success>>
Enqueue == /\ id # 0 /\ ~accepted /\ ~success
           /\ (queued[kind] = {} \/ id \in queued[kind] \/ BadCapacity)
           /\ queued' = [queued EXCEPT ![kind] = @ \cup {id}]
           /\ success' = TRUE
           /\ UNCHANGED <<source, processed, acked, kind, id, accepted, report, due>>
PersistAccepted == /\ success /\ accepted' = TRUE /\ success' = FALSE
                   /\ UNCHANGED <<source, queued, processed, acked, kind, id, report, due>>
Settle == /\ id # 0 /\ accepted
          /\ source' = [source EXCEPT ![kind] = @ \ {id}]
          /\ acked' = [acked EXCEPT ![kind] = @ \cup {id}]
          /\ kind' = "none" /\ id' = 0 /\ accepted' = FALSE
          /\ UNCHANGED <<queued, processed, report, due, success>>
DeferReport == /\ kind = "report" /\ id # 0 /\ ~accepted /\ ~success
               /\ report' = id /\ due' = FALSE
               /\ kind' = "none" /\ id' = 0
               /\ UNCHANGED <<source, queued, processed, acked, accepted, success>>
RetryDue == /\ report # 0 /\ ~due /\ due' = TRUE
            /\ UNCHANGED <<source, queued, processed, acked, kind, id, accepted, report, success>>
RetryReport == /\ id = 0 /\ report # 0 /\ due
               /\ kind' = "report" /\ id' = report /\ report' = 0 /\ due' = FALSE
               /\ UNCHANGED <<source, queued, processed, acked, accepted, success>>
Consume == \E k \in Kinds: \E chosen \in queued[k]:
             /\ queued' = [queued EXCEPT ![k] = @ \ {chosen}]
             /\ processed' = [processed EXCEPT ![k] = @ \cup {chosen}]
             /\ UNCHANGED <<source, acked, kind, id, accepted, report, due, success>>
Crash == /\ success /\ success' = FALSE
         /\ UNCHANGED <<source, queued, processed, acked, kind, id, accepted, report, due>>
Next == PrepareUrgent \/ PrepareReport \/ Enqueue \/ PersistAccepted \/ Settle
        \/ DeferReport \/ RetryDue \/ RetryReport \/ Consume \/ Crash
TypeOK == /\ source \in [Kinds -> SUBSET IDs] /\ queued \in [Kinds -> SUBSET IDs]
          /\ processed \in [Kinds -> SUBSET IDs] /\ acked \in [Kinds -> SUBSET IDs]
          /\ kind \in Kinds \cup {"none"} /\ id \in 0..2 /\ report \in 0..2
          /\ accepted \in BOOLEAN /\ due \in BOOLEAN /\ success \in BOOLEAN
QueueBounded == \A k \in Kinds: Cardinality(queued[k]) <= 1
NoLostPayload == \A k \in Kinds: acked[k] \subseteq queued[k] \cup processed[k]
UrgentEligible == (report # 0 /\ id = 0 /\ source["urgent"] # {}) => ENABLED PrepareUrgent
=============================================================================
