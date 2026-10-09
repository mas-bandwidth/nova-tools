-------------------- MODULE HierarchicalLanding --------------------
EXTENDS Naturals, FiniteSets, TLC
CONSTANTS Broken, Outside, RedCombined, Overlap, Recovery, ProgressOnly
VARIABLES head, reviewed, branches, reported, phase, payload, prior,
          certificate, local, cut, cutGate, remoteCut, pending,
          forgeMerged, marker, recorded, cleaned, disruptions, crashed,
          stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead
vars == <<head, reviewed, branches, reported, phase, payload, prior,
          certificate, local, cut, cutGate, remoteCut, pending,
          forgeMerged, marker, recorded, cleaned, disruptions, crashed,
          stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Children == {"a", "b"}
Jobs == Children \cup {"parent"}
Heads == Children \X (1..2)
Dest(j) == IF j = "parent" THEN "sprint" ELSE "parent"
Source(j) == IF j = "parent" THEN branches["parent"] ELSE {<<j, head[j]>>}
Current == {<<j, head[j]>> : j \in Children}
Rank(j) == IF j = "a" THEN 0 ELSE IF j = "b" THEN 1 ELSE 2
Eligible(j) == IF j = "parent" THEN Children \subseteq reported
               ELSE reviewed[j] = head[j] /\ j \notin reported
Green(tree) == ~RedCombined \/ Cardinality(tree) < 2
Files(tree) == {h[1] : h \in tree}
Disjoint(j) == ~Overlap /\ Files(payload[j]) \cap Files(branches[Dest(j)] \ prior[j]) = {}
Higher(j) == \E k \in Jobs : phase[k] = "checked" /\ Rank(k) < Rank(j)
Init == /\ head = [j \in Children |-> 1]
        /\ reviewed = head
        /\ branches = [d \in {"parent", "sprint", "dev"} |-> {}]
        /\ reported = {}
        /\ phase = [j \in Jobs |-> "idle"]
        /\ payload = [j \in Jobs |-> {}]
        /\ prior = payload /\ certificate = payload
        /\ local = "idle" /\ cut = {} /\ cutGate = {} /\ remoteCut = {}
        /\ pending = FALSE /\ forgeMerged = FALSE /\ marker = {} /\ recorded = {}
        /\ cleaned = FALSE /\ disruptions = 0 /\ crashed = FALSE
        /\ stalePush = FALSE /\ disjointPush = FALSE /\ orphan = FALSE
        /\ priorityViolation = FALSE /\ opened = 0 /\ intent = FALSE /\ mergeCut = {} /\ mergeInDev = FALSE /\ confirmed = FALSE /\ enqueued = FALSE /\ queuedHead = {}
Build(j) == /\ ~crashed /\ phase[j] = "idle" /\ Eligible(j) /\ j \notin reported
            /\ phase' = [phase EXCEPT ![j] = "built"]
            /\ payload' = [payload EXCEPT ![j] = Source(j)]
            /\ prior' = [prior EXCEPT ![j] = branches[Dest(j)]]
          /\ UNCHANGED <<head, reviewed, branches, reported, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Check(j) == /\ ~crashed /\ phase[j] = "built" /\ payload[j] = Source(j)
            /\ Green(payload[j] \cup prior[j])
            /\ phase' = [phase EXCEPT ![j] = "checked"]
            /\ certificate' = [certificate EXCEPT ![j] = payload[j] \cup prior[j]]
          /\ UNCHANGED <<head, reviewed, branches, reported, payload, prior, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Push(j) == /\ ~crashed /\ phase[j] = "checked"
           /\ (Broken = "priority" \/ ~Higher(j))
           /\ LET tree == branches[Dest(j)] \cup payload[j]
                  moved == branches[Dest(j)] # prior[j]
              IN /\ (~moved \/ Disjoint(j) \/ Green(tree))
                 /\ branches' = [branches EXCEPT ![Dest(j)] = tree]
                 /\ certificate' = IF moved /\ Disjoint(j) THEN certificate
                                    ELSE [certificate EXCEPT ![j] = tree]
                 /\ disjointPush' = (disjointPush \/ (moved /\ Disjoint(j) /\ ~Green(tree)))
           /\ phase' = [phase EXCEPT ![j] = "pushed"]
           /\ stalePush' = (stalePush \/ payload[j] # Source(j))
           /\ priorityViolation' = (priorityViolation \/ Higher(j))
          /\ UNCHANGED <<head, reviewed, reported, payload, prior, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, orphan, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Report(j) == /\ ~crashed /\ phase[j] = "pushed"
             /\ (Broken = "report" \/ payload[j] = Source(j))
             /\ payload[j] \subseteq branches[Dest(j)]
             /\ reported' = reported \cup {j}
             /\ phase' = [phase EXCEPT ![j] = "done"]
          /\ UNCHANGED <<head, reviewed, branches, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Retry(j) == /\ ~crashed /\ phase[j] \in {"built", "checked", "pushed"}
            /\ payload[j] # Source(j)
            /\ phase' = [phase EXCEPT ![j] = "idle"]
          /\ UNCHANGED <<head, reviewed, branches, reported, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Rework == /\ Outside /\ disruptions = 0 /\ local = "idle" /\ phase["parent"] = "idle"
          /\ head["a"] = 1
          /\ head' = [head EXCEPT !["a"] = 2]
          /\ reviewed' = [reviewed EXCEPT !["a"] = 0]
          /\ reported' = reported \ {"a"} /\ phase' = [phase EXCEPT !["a"] = IF @ = "done" THEN "idle" ELSE @]
          /\ disruptions' = 1
\* An outside event supplies a newly reviewed authoritative parent head.
          /\ UNCHANGED <<branches, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
ParentMoves == /\ Outside /\ disruptions = 0 /\ local = "idle"
               /\ phase["parent"] \in {"built","checked","pushed"}
               /\ branches' = [branches EXCEPT !["parent"] = @ \cup {<<"b",2>>}]
               /\ disruptions' = 1
          /\ UNCHANGED <<head, reviewed, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Review == /\ reviewed["a"] # head["a"]
          /\ reviewed' = [reviewed EXCEPT !["a"] = head["a"]]
          /\ UNCHANGED <<head, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Cut == /\ ~crashed /\ ~cleaned /\ local = "idle" /\ "parent" \in reported
       /\ ~(branches["sprint"] \subseteq branches["dev"])
       /\ Current \subseteq branches["sprint"]
       /\ cut' = branches["sprint"] \cup branches["dev"] /\ local' = "cut"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
GateCut == /\ ~crashed /\ local = "cut" /\ Green(cut)
           /\ cutGate' = cut /\ local' = "gated"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
PushCut == /\ ~crashed /\ local = "intent"
           /\ remoteCut' = cut /\ local' = "pushed"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, cutGate, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
SaveIntent == /\ ~crashed /\ local = "gated"
              /\ intent' = Recovery /\ local' = "intent"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
OpenPR == /\ ~crashed /\ local = "pushed" /\ opened < 2
                  /\ opened' = opened + 1 /\ local' = "opened"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
PersistPR == /\ ~crashed /\ local = "opened"
             /\ pending' = TRUE /\ local' = "watch"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, cutGate, remoteCut, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
ConfirmCut == /\ ~crashed /\ pending /\ ~confirmed /\ remoteCut = cutGate
              /\ confirmed' = TRUE
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, enqueued, queuedHead>>
Enqueue == /\ ~crashed /\ pending /\ confirmed /\ ~enqueued
           /\ (Broken = "enqueue" \/ remoteCut = cutGate)
           /\ enqueued' = TRUE /\ queuedHead' = remoteCut
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed>>
ForgeClaim == /\ pending /\ ~forgeMerged /\ forgeMerged' = TRUE
              /\ mergeCut' = IF Broken = "reported" THEN {} ELSE cutGate
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeInDev, confirmed, enqueued, queuedHead>>
ActualDevMerge == /\ pending /\ ~(remoteCut \subseteq branches["dev"])
                  /\ branches' = [branches EXCEPT !["dev"] = @ \cup remoteCut]
                  /\ mergeInDev' = (Broken # "reported")
          /\ UNCHANGED <<head, reviewed, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, confirmed, enqueued, queuedHead>>
Mark == /\ ~crashed /\ pending /\ forgeMerged /\ marker = {}
        /\ (Broken = "forge" \/ (cutGate \subseteq branches["dev"] /\
             (Broken = "reported" \/ (mergeCut = cutGate /\ mergeInDev))))
        /\ (Broken = "headrecord" \/ remoteCut = cutGate)
        /\ marker' = cutGate
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Record == /\ ~crashed /\ marker # {} /\ recorded = {} /\ recorded' = marker
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Forget == /\ ~crashed /\ recorded # {} /\ pending
          /\ pending' = FALSE /\ cleaned' = TRUE /\ local' = "done" /\ intent' = FALSE
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, cutGate, remoteCut, forgeMerged, marker, recorded, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Crash == /\ Outside /\ disruptions = 0 /\ ~crashed
         /\ (~ProgressOnly \/ ~Recovery \/ ~intent \/ opened > 0)
         /\ crashed' = TRUE /\ disruptions' = 1
         /\ phase' = [j \in Jobs |-> IF phase[j] = "done" THEN "done" ELSE "idle"]
         /\ local' = IF pending THEN "watch" ELSE IF Recovery /\ intent THEN "recover" ELSE "idle"
         /\ orphan' = (orphan \/ (local \in {"pushed", "opened"} /\ ~pending))
          /\ UNCHANGED <<head, reviewed, branches, reported, payload, prior, certificate, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, stalePush, disjointPush, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
RecoverPR == /\ ~crashed /\ Recovery /\ intent /\ ~pending /\ local = "recover"
             /\ opened = 1 /\ pending' = TRUE /\ local' = "watch"
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, cut, cutGate, remoteCut, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Resume == /\ crashed /\ crashed' = FALSE
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, remoteCut, pending, forgeMerged, marker, recorded, cleaned, disruptions, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
JobStep(j) == Build(j) \/ Check(j) \/ Push(j) \/ Report(j) \/ Retry(j)
Promote == Cut \/ GateCut \/ SaveIntent \/ PushCut \/ OpenPR \/ PersistPR \/ RecoverPR \/ ConfirmCut \/ Enqueue \/ ForgeClaim \/ ActualDevMerge \/ Mark \/ Record \/ Forget
RemoteCutChanges == /\ Broken \in {"head", "headsafe", "headrecord", "enqueue"} /\ pending /\ ~forgeMerged /\ remoteCut = cutGate
                    /\ remoteCut' = cutGate \cup {<<"b",2>>}
          /\ UNCHANGED <<head, reviewed, branches, reported, phase, payload, prior, certificate, local, cut, cutGate, pending, forgeMerged, marker, recorded, cleaned, disruptions, crashed, stalePush, disjointPush, orphan, priorityViolation, opened, intent, mergeCut, mergeInDev, confirmed, enqueued, queuedHead>>
Next == (\E j \in Jobs : JobStep(j)) \/ Promote \/ Rework \/ ParentMoves \/ Review \/ Crash \/ Resume \/ RemoteCutChanges
Spec == Init /\ [][Next]_vars
FairSpec == Spec /\ (\A j \in Jobs : WF_vars(JobStep(j))) /\ WF_vars(Promote)
            /\ WF_vars(Review) /\ WF_vars(Resume) /\ WF_vars(ActualDevMerge)
TruthfulReported == marker # {} => mergeCut = cutGate /\ mergeInDev
TruthfulDev == marker \subseteq branches["dev"] /\ recorded \subseteq branches["dev"]
ReportsCurrent == /\ \A j \in Children : j \in reported => <<j,head[j]>> \in branches["parent"]
                  /\ "parent" \in reported => payload["parent"] = branches["parent"]
FrozenGate == remoteCut # {} => remoteCut = cutGate
TypeOK == /\ head \in [Children -> 1..2] /\ reviewed \in [Children -> 0..2]
          /\ branches \in [{"parent", "sprint", "dev"} -> SUBSET Heads]
          /\ reported \subseteq Jobs /\ phase \in [Jobs -> {"idle","built","checked","pushed","done"}]
          /\ local \in {"idle","cut","gated","intent","pushed","opened","recover","watch","done"}
          /\ disruptions \in 0..1 /\ opened \in 0..2
PreservesBranches == [][\A d \in {"parent", "sprint", "dev"} : branches[d] \subseteq branches'[d]]_vars
KeepsAncestors == \A d \in {"parent", "sprint", "dev"} : branches[d] \subseteq Heads
PriorityHeld == ~priorityViolation
NoStalePush == ~stalePush
NoDisjointRed == ~disjointPush
NoOrphan == ~orphan
NoUncheckedQueued == enqueued => queuedHead = cutGate
NoUncheckedRecord == marker # {} => remoteCut = cutGate
NoDuplicatePR == opened <= 1
Drains == <>(cleaned /\ Current \subseteq branches["dev"])
====================================================================
