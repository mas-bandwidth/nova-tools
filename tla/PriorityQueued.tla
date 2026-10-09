--------------------------- MODULE PriorityQueued ---------------------------
EXTENDS Naturals, FiniteSets, Sequences
CONSTANTS Scenario, Broken, Controls, Lane
Producers == {"p", "q"}
Children == {"earlier", "later"}
\* An explicit authoritative link, not an id-derived association. Source bridge
\* work/read observations resolve their primary fields to these logical roles.
Producer(c) == IF c = "earlier" THEN "p" ELSE "q"
Fix(c) == Scenario \in {"fix", "source-fix"}
Read(c) == Scenario \in {"read", "source-read"} \/
           (Scenario = "mixed" /\ c = "later")
InitialLevel(p) == CASE Scenario = "mixed" -> 3
                       [] Scenario \in {"source-work", "source-read", "source-fix"} /\ p = "q" -> 1
                       [] OTHER -> 5
Allowed == IF Scenario \in {"read", "source-read"} THEN Lane \in {"friend", "reader"}
           ELSE IF Scenario = "mixed" THEN Lane = "friend"
           ELSE Lane \in {"friend", "fleet"}
RoleRank(c,l) == IF Read(c) THEN IF Broken = "readrole" THEN l ELSE 4
                 ELSE IF Fix(c) THEN IF Broken = "fixrole" THEN 3 ELSE 0 ELSE l
VARIABLES primary, primaryStage, child, copied, copiedProducer, mode, promoted, starts, bad
qvars == <<primary, primaryStage, child, copied, copiedProducer, mode, promoted, starts, bad>>
QueueInit == /\ primary = [p \in Producers |-> InitialLevel(p)]
             /\ primaryStage = [p \in Producers |-> IF Read(IF p = "p" THEN "earlier" ELSE "later") THEN "review" ELSE "working"]
             /\ child = [c \in Children |-> IF Scenario = "creation" THEN "absent" ELSE "queued"]
             /\ copied = [c \in Children |-> RoleRank(c,IF Scenario = "mixed" THEN 3 ELSE 5)]
             /\ copiedProducer = [c \in Children |-> IF Scenario = "mixed" THEN 3 ELSE 5]
             /\ mode = "running" /\ promoted = (Scenario \in {"source-work", "source-read", "source-fix"})
             /\ starts = <<>> /\ bad = {}
Effective(c) == RoleRank(c,primary[Producer(c)])
Before(a,b) == Effective(a) < Effective(b) \/
  (((Read(a) /\ Read(b)) \/ (Fix(a) /\ Fix(b))) /\ primary[Producer(a)] < primary[Producer(b)]) \/
  (Effective(a) = Effective(b) /\ Read(a) = Read(b) /\
    ((~Read(a) /\ ~Fix(a)) \/ (primary[Producer(a)] = primary[Producer(b)])) /\ a = "earlier" /\ b = "later")
ChoiceRank(c) == IF Broken = "stale" THEN copied[c] ELSE Effective(c)
ChoiceProducer(c) == IF Broken = "stale" THEN copiedProducer[c] ELSE primary[Producer(c)]
SelectorBefore(a,b) == ChoiceRank(a) < ChoiceRank(b) \/
  (((Read(a) /\ Read(b)) \/ (Fix(a) /\ Fix(b))) /\ ChoiceProducer(a) < ChoiceProducer(b)) \/
  (ChoiceRank(a) = ChoiceRank(b) /\ Read(a) = Read(b) /\
    ((~Read(a) /\ ~Fix(a)) \/ (ChoiceProducer(a) = ChoiceProducer(b))) /\ a = "earlier" /\ b = "later")
CurrentBest(c) == \A k \in Children : child[k] = "queued" => ~Before(k,c)
Selected(c) == \A k \in Children : child[k] = "queued" => ~SelectorBefore(k,c)
Create(c) == /\ mode = "running" /\ child[c] = "absent"
             /\ child' = [child EXCEPT ![c] = "queued"]
             /\ copied' = [copied EXCEPT ![c] = Effective(c)]
             /\ copiedProducer' = [copiedProducer EXCEPT ![c] = primary[Producer(c)]]
             /\ UNCHANGED <<primary, primaryStage, mode, promoted, starts, bad>>
Promote(l) == /\ ~promoted /\ l \in {1,2,3,5,6}
              /\ child["later"] \in {"queued", "running"}
              /\ primary' = [primary EXCEPT !["q"] = l]
              /\ promoted' = TRUE
              /\ UNCHANGED <<primaryStage, child, copied, copiedProducer, mode, starts, bad>>
\* Launch is distinct from Create. The parent already works/is in review while
\* its physical work/read consumer is still queued. Producer updates deliberately
\* leave copied fields unchanged; admission resolves authoritative live rank.
LaunchObserved(c,checkSelector) == /\ Allowed /\ child[c] = "queued"
             /\ (mode = "running" \/ Broken = "control")
             /\ \A k \in Children : child[k] # "running"
             /\ (Selected(c) \/ ~checkSelector \/ Broken = "reverse")
             /\ child' = [child EXCEPT ![c] = "running"]
             /\ starts' = Append(starts,c)
             /\ bad' = bad \cup (IF CurrentBest(c) THEN {} ELSE {"order"}) \cup
                         (IF mode = "running" THEN {} ELSE {"control"})
             /\ UNCHANGED <<primary, primaryStage, copied, copiedProducer, mode, promoted>>
Launch(c) == LaunchObserved(c,TRUE)
Finish(c) == /\ mode # "stopped" /\ child[c] = "running"
             /\ child' = [child EXCEPT ![c] = "done"]
             /\ primaryStage' = [primaryStage EXCEPT ![Producer(c)] = IF Read(c) THEN "merging" ELSE "review"]
             /\ UNCHANGED <<primary, copied, copiedProducer, mode, promoted, starts, bad>>
Pause == /\ Controls /\ mode = "running" /\ mode' = "paused"
         /\ UNCHANGED <<primary, primaryStage, child, copied, copiedProducer, promoted, starts, bad>>
Unpause == /\ Controls /\ mode = "paused" /\ mode' = "running"
           /\ UNCHANGED <<primary, primaryStage, child, copied, copiedProducer, promoted, starts, bad>>
Stop == /\ Controls /\ mode # "stopped" /\ mode' = "stopped"
        /\ UNCHANGED <<primary, primaryStage, child, copied, copiedProducer, promoted, starts, bad>>
QueueNext == Pause \/ Unpause \/ Stop \/ (\E l \in {1,2,3,5,6} : Promote(l))
             \/ (\E c \in Children : Create(c) \/ Launch(c) \/ Finish(c))
QueueSpec == QueueInit /\ [][QueueNext]_qvars
QueueTypeOK == /\ primary \in [Producers -> 0..6]
               /\ primaryStage \in [Producers -> {"working", "review", "merging"}]
               /\ child \in [Children -> {"absent", "queued", "running", "done"}]
               /\ copied \in [Children -> 0..6] /\ copiedProducer \in [Children -> 0..6] /\ Len(starts) <= 2
CapacitySafe == Cardinality({c \in Children : child[c] = "running"}) <= 1
OrderedLaunch == "order" \notin bad
ReadRoleFixed == \A c \in Children : Read(c) => Effective(c) = 4 /\ copied[c] = 4
FixRoleFixed == \A c \in Children : Fix(c) => Effective(c) = 0 /\ copied[c] = 0
NoControlLaunch == "control" \notin bad
ReachPromotionWitness == ~promoted
=============================================================================
