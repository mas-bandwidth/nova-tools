----------------------------- MODULE StatsTidy -----------------------------
EXTENDS Naturals, FiniteSets, TLC
CONSTANT Broken
\* Store.TidyStats: pin/read (171), refuse (178), dry run (207), planned
\* archive (211), Run/TidyDone (217), archive outcome (231/240), record
\* (232/255). KV records are deployment-wide; clear freezes old tables,
\* not these KV writes. TidyDone recomputes the removable cards in Run.
\* Two history-only done cards stand for TidyKept's complement, one per
\* row; a third, initially open card can finish and stays recent/kept.
\* Retention ages, counters, payload bytes and nonce collisions are outside
\* this model. Each invocation has a distinct archive key, as in the API.
Tidies == {1, 2}
Rows == {"friends", "fleet"}
Cards == {"a", "b", "c"}
History == {"a", "b"}
Row(c) == IF c = "b" THEN "fleet" ELSE "friends"
Epochs == 0..1
EmptyRecord == [epoch |-> 0, since |-> 0,
                kinds |-> [r \in Rows |-> 0], named |-> {}]
VARIABLES archives, record, done, records, epoch, pc, local, pinned,
          dry, selected, writes, moves, succeeded, beforeMove, keptWork,
          revision, observed, crashes, finished
vars == <<archives, record, done, records, epoch, pc, local, pinned,
          dry, selected, writes, moves, succeeded, beforeMove, keptWork,
          revision, observed, crashes, finished>>
Init ==
 /\ archives = [t \in Tidies |-> "absent"]
 /\ record = EmptyRecord
 /\ done = [e \in Epochs |-> IF e = 0 THEN History ELSE {}]
 /\ records = [e \in Epochs |-> IF e = 0 THEN Cards ELSE {}]
 /\ epoch = 0
 /\ pc = [t \in Tidies |-> "read"]
 /\ local = [t \in Tidies |-> EmptyRecord]
 /\ pinned = [t \in Tidies |-> 0]
 /\ dry \in [Tidies -> BOOLEAN]
 /\ selected \in [Tidies -> {Rows, {"friends"}, {"fleet"}}]
 /\ writes = [t \in Tidies |-> 0]
 /\ moves = [t \in Tidies |-> {}]
 /\ succeeded = [t \in Tidies |-> FALSE]
 /\ beforeMove = TRUE /\ keptWork = TRUE
 /\ revision = 0 /\ observed = [t \in Tidies |-> 0]
 /\ crashes = 0 /\ finished = FALSE
\* Both invocations' clocks lie within TidyAgainAfter of time 1. Zero is
\* no previous tidy; the initial empty record permits both concurrent reads.
TooSoon(t) == local[t].since = 1
TidyRead(t) ==
 /\ pc[t] = "read"
 /\ local' = [local EXCEPT ![t] = record]
 /\ pinned' = [pinned EXCEPT ![t] = epoch]
 /\ pc' = [pc EXCEPT ![t] = "check"]
 /\ UNCHANGED <<archives, record, done, records, epoch, dry, selected,
      writes, moves, succeeded, beforeMove, keptWork, revision, observed,
      crashes, finished>>
Refuse(t) ==
 /\ pc[t] = "check" /\ TooSoon(t) /\ Broken # "NoRefuse"
 /\ pc' = [pc EXCEPT ![t] = "refused"]
 /\ UNCHANGED <<archives, record, done, records, epoch, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, observed, crashes, finished>>
DryRun(t) ==
 /\ pc[t] = "check" /\ (~TooSoon(t) \/ Broken = "NoRefuse")
 /\ dry[t] /\ Broken # "DryWrites"
 /\ pc' = [pc EXCEPT ![t] = "dry"]
 /\ UNCHANGED <<archives, record, done, records, epoch, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, observed, crashes, finished>>
Accept(t) ==
 /\ pc[t] = "check" /\ (~TooSoon(t) \/ Broken = "NoRefuse")
 /\ (~dry[t] \/ Broken = "DryWrites")
 /\ pc' = [pc EXCEPT ![t] = IF Broken = "MoveFirst" THEN "move" ELSE "plan"]
 /\ observed' = [observed EXCEPT ![t] = revision]
 /\ UNCHANGED <<archives, record, done, records, epoch, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, crashes, finished>>
WriteArchivePlanned(t) ==
 /\ pc[t] \in {"plan", "lateplan"}
 /\ archives' = [archives EXCEPT ![t] = "planned"]
 /\ local' = [local EXCEPT ![t].named = @ \cup {t}]
 /\ writes' = [writes EXCEPT ![t] = @ + 1]
 /\ pc' = [pc EXCEPT ![t] = IF pc[t] = "lateplan" THEN "outcome" ELSE "move"]
 /\ UNCHANGED <<record, done, records, epoch, pinned, dry, selected,
      moves, succeeded, beforeMove, keptWork, revision, observed, crashes, finished>>
Move(t) ==
 /\ pc[t] = "move" /\ pinned[t] = epoch
 /\ LET take == {c \in done[epoch] \cap History : Row(c) \in selected[t]}
    IN /\ done' = [done EXCEPT ![epoch] = @ \ take]
       /\ moves' = [moves EXCEPT ![t] = take]
       /\ beforeMove' = beforeMove /\ (take = {} \/ archives[t] # "absent")
       /\ keptWork' = keptWork /\ take \subseteq records[epoch]
             /\ take \subseteq done[epoch]
             /\ \A c \in take : Row(c) \in selected[t]
 /\ succeeded' = [succeeded EXCEPT ![t] = TRUE]
 /\ pc' = [pc EXCEPT ![t] = IF Broken = "MoveFirst" THEN "lateplan" ELSE "outcome"]
 /\ revision' = revision + 1
 /\ UNCHANGED <<archives, record, records, epoch, local, pinned, dry,
      selected, writes, observed, crashes, finished>>
\* Lost abstracts exhausted optimistic retries after a changed done cell;
\* RefusedMove abstracts stale epoch refusal. Neither invents a successful move.
Lost(t) ==
 /\ pc[t] = "move" /\ revision # observed[t]
 /\ pc' = [pc EXCEPT ![t] = "outcome"]
 /\ UNCHANGED <<archives, record, done, records, epoch, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, observed, crashes, finished>>
RefusedMove(t) ==
 /\ pc[t] = "move" /\ pinned[t] # epoch
 /\ pc' = [pc EXCEPT ![t] = "outcome"]
 /\ UNCHANGED <<archives, record, done, records, epoch, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, observed, crashes, finished>>
WriteArchiveDone(t) ==
 /\ pc[t] = "outcome" /\ succeeded[t]
 /\ archives' = [archives EXCEPT ![t] = "done"]
 /\ writes' = [writes EXCEPT ![t] = @ + 1]
 /\ pc' = [pc EXCEPT ![t] = "record"]
 /\ UNCHANGED <<record, done, records, epoch, local, pinned, dry,
      selected, moves, succeeded, beforeMove, keptWork, revision,
      observed, crashes, finished>>
WriteArchiveFailed(t) ==
 /\ pc[t] = "outcome" /\ ~succeeded[t]
 /\ archives' = [archives EXCEPT ![t] = "failed"]
 /\ writes' = [writes EXCEPT ![t] = @ + 1]
 /\ pc' = [pc EXCEPT ![t] = "record"]
 /\ UNCHANGED <<record, done, records, epoch, local, pinned, dry,
      selected, moves, succeeded, beforeMove, keptWork, revision,
      observed, crashes, finished>>
WriteRecord(t) ==
 /\ pc[t] = "record"
 /\ record' = IF succeeded[t]
       THEN [epoch |-> pinned[t], since |-> 1, named |-> local[t].named,
             kinds |-> [r \in Rows |-> IF r \in selected[t] THEN 1
                       ELSE IF local[t].epoch = pinned[t]
                            THEN local[t].kinds[r] ELSE 0]]
       ELSE local[t]
 /\ writes' = [writes EXCEPT ![t] = @ + 1]
 /\ pc' = [pc EXCEPT ![t] = "end"]
 /\ UNCHANGED <<archives, done, records, epoch, local, pinned, dry,
      selected, moves, succeeded, beforeMove, keptWork, revision,
      observed, crashes, finished>>
Crash(t) ==
 /\ crashes = 0 /\ pc[t] \notin {"end", "refused", "dry", "crashed"}
 /\ crashes' = 1 /\ pc' = [pc EXCEPT ![t] = "crashed"]
 /\ UNCHANGED <<archives, record, done, records, epoch, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, observed, finished>>
Clear ==
 /\ epoch = 0 /\ epoch' = 1
 /\ UNCHANGED <<archives, record, done, records, pc, local, pinned,
      dry, selected, writes, moves, succeeded, beforeMove, keptWork,
      revision, observed, crashes, finished>>
CardFinishes ==
 /\ ~finished /\ finished' = TRUE
 /\ done' = [done EXCEPT ![epoch] = @ \cup {"c"}]
 /\ records' = [records EXCEPT ![epoch] = @ \cup {"c"}]
 /\ revision' = revision + 1
 /\ UNCHANGED <<archives, record, epoch, pc, local, pinned, dry,
      selected, writes, moves, succeeded, beforeMove, keptWork,
      observed, crashes>>
Next == Clear \/ CardFinishes \/ \E t \in Tidies :
 TidyRead(t) \/ Refuse(t) \/ DryRun(t) \/ Accept(t) \/ WriteArchivePlanned(t)
 \/ Move(t) \/ Lost(t) \/ RefusedMove(t) \/ WriteArchiveDone(t)
 \/ WriteArchiveFailed(t) \/ WriteRecord(t) \/ Crash(t)
Spec == Init /\ [][Next]_vars
TypeOK ==
 /\ archives \in [Tidies -> {"absent", "planned", "done", "failed"}]
 /\ done \in [Epochs -> SUBSET Cards] /\ records \in [Epochs -> SUBSET Cards]
 /\ epoch \in Epochs /\ crashes \in 0..1
 /\ record.named \subseteq Tidies /\ record.since \in 0..1
 /\ record.kinds \in [Rows -> 0..1] /\ record.epoch \in Epochs
ArchiveBeforeMove == beforeMove
ArchiveDoneOnlyAfterMove == \A t \in Tidies : archives[t] = "done" => succeeded[t]
WorkKept == keptWork /\ \A t \in Tidies : moves[t] \subseteq records[pinned[t]]
RefusedWritesNothing == \A t \in Tidies : TooSoon(t) => writes[t] = 0
DryRunWritesNothing == \A t \in Tidies : dry[t] => writes[t] = 0
EveryArchiveNamed == {t \in Tidies : archives[t] # "absent"} \subseteq record.named
\* Diagnostic strengthenings expose persistent failures beyond the first
\* normal in-flight gap, without changing the machine's actions.
CrashArchivesNamed == \A t \in Tidies :
 (pc[t] = "crashed" /\ archives[t] # "absent") => t \in record.named
CompletedArchivesNamed == (\A t \in Tidies : pc[t] = "end") => EveryArchiveNamed
=============================================================================
