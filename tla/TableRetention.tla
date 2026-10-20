------------------------- MODULE TableRetention -------------------------
\* A nova table's operation ledger and change stream, and replay inside their
\* bounds (pkg/nsprint/fn/lua/table.lua: T.recordops, T.trimops, T.sweepops,
\* the digest lookup and the change event's MAXLEN).
\*
\* State: ops is the set of held operation records (epoch, id, digest,
\* result, and the sequence the store assigned when it recorded the op);
\* stream is the sequence of change events (their operation keys); epoch is
\* the current epoch; runs counts how many times each operation was applied;
\* clock is the next record sequence. lastKind/lastKey/lastDigest are the
\* answer of the most recent replay: "result" for a held op whose digest
\* matches, "conflict" for a different request under a held id, "expired"
\* for an op whose record is past the bound, and "none" after a write or an
\* epoch advance.
\*
\* Actions: Record appends at most Bound records to its epoch, dropping the
\* oldest by sequence, and appends one event trimmed to StreamBound; ReplaySame
\* and ReplayDifferent answer from a held record; ReplayExpired refuses an
\* operation whose record was trimmed, unless the broken model applies it
\* again; Advance moves the epoch and removes every epoch older than the
\* previous one, so only the current and previous epochs are retained.
\*
\* The code keeps no reader cursor on a table's change stream, so the model
\* trims the oldest event without consulting a reader: there is no such
\* cursor to keep.
\*
\* Broken names a misimplementation a reversed witness turns on:
\*   "unbounded": Record trims neither the operation hash nor the stream
\*                (Bounded).
\*   "rerun":     ReplayExpired applies an operation past the bound again
\*                (NeverRerun).
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Epochs, OpIDs, Digests, Bound, StreamBound, Broken

Keys == Epochs \X OpIDs

VARIABLES ops, stream, epoch, runs, clock, lastKind, lastKey, lastDigest

vars == <<ops, stream, epoch, runs, clock, lastKind, lastKey, lastDigest>>

MinEpoch == CHOOSE e \in Epochs : \A f \in Epochs : e <= f
MaxEpoch == CHOOSE e \in Epochs : \A f \in Epochs : f <= e
k0 == CHOOSE k \in Keys : TRUE
d0 == CHOOSE d \in Digests : TRUE

TypeOK ==
  /\ ops \in SUBSET [epoch : Epochs, id : OpIDs, digest : Digests, result : {"ok"}, seq : Nat]
  /\ stream \in Seq(Keys)
  /\ epoch \in Epochs
  /\ runs \in [Keys -> Nat]
  /\ clock \in Nat
  /\ lastKind \in {"none", "result", "conflict", "expired"}
  /\ lastKey \in Keys
  /\ lastDigest \in Digests

Init ==
  /\ ops = {}
  /\ stream = <<>>
  /\ epoch = MinEpoch
  /\ runs = [k \in Keys |-> 0]
  /\ clock = 0
  /\ lastKind = "none"
  /\ lastKey = k0
  /\ lastDigest = d0

Held(k) == {r \in ops : r.epoch = k[1] /\ r.id = k[2]}

EpochCount(e) == Cardinality({r \in ops : r.epoch = e})

TrimEpoch(S, e) ==
  LET inE == {r \in S : r.epoch = e}
  IN IF Cardinality(inE) <= Bound
       THEN S
       ELSE S \ {CHOOSE r \in inE : \A s \in inE : r.seq <= s.seq}

TrimStream(s) ==
  IF Len(s) <= StreamBound THEN s ELSE SubSeq(s, Len(s) - StreamBound + 1, Len(s))

Record(e, id, d) ==
  LET key == <<e, id>>
      rec == [epoch |-> e, id |-> id, digest |-> d, result |-> "ok", seq |-> clock]
      all == ops \cup {rec}
      kept == IF Broken = "unbounded" THEN all ELSE TrimEpoch(all, e)
      added == Append(stream, key)
      keptStream == IF Broken = "unbounded" THEN added ELSE TrimStream(added)
  IN /\ e = epoch
     /\ Held(key) = {}
     /\ runs[key] = 0
     /\ ops' = kept
     /\ stream' = keptStream
     /\ runs' = [runs EXCEPT ![key] = @ + 1]
     /\ clock' = clock + 1
     /\ lastKind' = "none"
     /\ UNCHANGED <<epoch, lastKey, lastDigest>>

ReplaySame(k, d) ==
  /\ \E r \in Held(k) : r.digest = d
  /\ lastKind' = "result"
  /\ lastKey' = k
  /\ lastDigest' = d
  /\ UNCHANGED <<ops, stream, epoch, runs, clock>>

ReplayDifferent(k, d) ==
  /\ \E r \in Held(k) : r.digest # d
  /\ lastKind' = "conflict"
  /\ lastKey' = k
  /\ lastDigest' = d
  /\ UNCHANGED <<ops, stream, epoch, runs, clock>>

\* The record is past the bound: refused by name and never applied again.
ReplayExpired(k) ==
  LET rec == [epoch |-> k[1], id |-> k[2], digest |-> lastDigest, result |-> "ok", seq |-> clock]
      all == ops \cup {rec}
  IN /\ Held(k) = {}
     /\ runs[k] >= 1
     /\ IF Broken = "rerun"
          THEN /\ ops' = TrimEpoch(all, k[1])
               /\ stream' = TrimStream(Append(stream, k))
               /\ runs' = [runs EXCEPT ![k] = @ + 1]
               /\ clock' = clock + 1
               /\ lastKind' = "none"
               /\ lastKey' = k
               /\ UNCHANGED <<epoch, lastDigest>>
          ELSE /\ UNCHANGED <<ops, stream, epoch, runs, clock, lastDigest>>
               /\ lastKind' = "expired"
               /\ lastKey' = k

Advance ==
  /\ epoch < MaxEpoch
  /\ LET next == epoch + 1
         keep == {r \in ops : r.epoch >= next - 1}
     IN /\ epoch' = next
        /\ ops' = keep
        /\ lastKind' = "none"
        /\ UNCHANGED <<stream, runs, clock, lastKey, lastDigest>>

Next ==
  \/ \E e \in Epochs, id \in OpIDs, d \in Digests : Record(e, id, d)
  \/ \E k \in Keys, d \in Digests : ReplaySame(k, d) \/ ReplayDifferent(k, d)
  \/ \E k \in Keys : ReplayExpired(k)
  \/ Advance
  \/ UNCHANGED vars

Spec == Init /\ [][Next]_vars

\* The operation hash holds at most Bound records in an epoch and the change
\* stream at most StreamBound events.
Bounded ==
  /\ \A e \in Epochs : EpochCount(e) <= Bound
  /\ Len(stream) <= StreamBound

\* A replay of a held op returns its recorded result; a different request
\* under a held id is a conflict; an expired op is refused, not answered.
ReplayAnswered ==
  /\ (lastKind = "result" => \E r \in Held(lastKey) : r.digest = lastDigest)
  /\ (lastKind = "conflict" => \E r \in Held(lastKey) : r.digest # lastDigest)
  /\ (lastKind = "expired" => Held(lastKey) = {})

\* An operation id is applied at most once, so an id past the bound is never
\* run a second time.
NeverRerun == \A k \in Keys : runs[k] <= 1
=============================================================================
