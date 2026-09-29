\* A snapshot of the parts of tla/CardManager.tla that the lifecycle comparison reads,
\* from the branch rowan/card-layer-model (PR #4599) at e0018f5c6ccaab02f7be31893a5e74388385a911.
\* It is a fixture for the extractor's own tests, not the model: the model is the
\* file tla/CardManager.tla, and TestLifecycleAgreesWithTheModel reads that when it
\* is in the tree.

States   == {"waiting", "ready", "working", "review", "merging", "landed", "done"}
Open     == {"waiting", "ready", "working", "review", "merging"}
Line     == {"ready", "working", "review", "merging"}
Terminal == {"landed", "done"}
Outcomes == {"completed", "cancelled", "depfailed", "replaced"}

Events == {"start", "result", "head", "merge", "rework", "requeue", "land",
           "complete", "cancel", "depfail"}

To(c, e) ==
  CASE e = "start"    -> "working"
    [] e = "result"   -> "review"
    [] e = "head"     -> "review"
    [] e = "merge"    -> "merging"
    [] e = "rework"   -> "ready"
    [] e = "requeue"  -> "review"
    [] e = "land"     -> "landed"
    [] e \in {"complete", "cancel", "depfail"} -> "done"

OwnOK(c, e) ==
  CASE e = "start"    -> St(c) = "ready"
    \* a bound result: a new head (or result artifact) really exists
    [] e = "result"   -> St(c) = "working" /\ truth[c] > head[c]
    \* a push since the recorded head
    [] e = "head"     -> St(c) \in {"review", "merging"} /\ truth[c] > head[c]
    [] e = "merge"    -> St(c) = "review" /\ PRKind(c) /\ Authorized(c) /\ truth[c] = head[c]
    [] e = "rework"   -> St(c) = "review" /\ head[c] < MaxHead
    \* the merge queue rejected the head: back to review at the same head,
    \* the rejection recorded
    [] e = "requeue"  -> St(c) = "merging" /\ QueueRejection(c) \in Obs[c]
    \* a verified landing: the true head, which the queue did not reject
    [] e = "land"     -> \/ St(c) = "merging" /\ truth[c] = head[c] /\ QueueRejection(c) \notin Obs[c]
                         \/ St(c) \in {"waiting", "ready", "working"} /\ PRKind(c)
                         \/ Broken = "resurrect" /\ St(c) = "done" /\ PRKind(c)
    [] e = "complete" -> St(c) = "review" /\ ~PRKind(c) /\ Authorized(c) /\ truth[c] = head[c]
    [] e = "cancel"   -> St(c) \in Open
    [] e = "depfail"  -> St(c) = "waiting"
\* the guard that reads other cards, in snapshot S

Replaceable == {c \in ActiveIds : St(c) \in {"waiting", "ready", "review", "merging"}}

