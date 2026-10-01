------------------------------- MODULE Merger -------------------------------
\* The merger: the mechanical caller of the merge step (docs/SPEC-SPRINT.md
\* section 7: "The merge step is mechanical and is given its facts by the
\* caller"), written before it is built (the owner, 2026-10-01: "Fix the
\* problems. You have permission. Go."). internal/member/merger.go is the loop;
\* cmd/nova-swarm's merger git is its hands.
\*
\* THE STATE.
\*   col     the merge table: a card queued, stuck, merged (landed), or returned
\*           by the coordinator (off the queue)
\*   sstate  a stream's state: merging, or stopped (it needs the coordinator)
\*   br      the branch of a stream's one batch, sprint/<stream>.e<epoch>.b<k>, a
\*           new one every batch built fresh from the development branch (the
\*           record resets with each Start; a red, conflicting or rejected
\*           batch's branch is left on origin, never deleted or rewritten): its
\*           state (none, building, pushed, green, red, landed), the batch's cards
\*           in order, k the cards merged into it so far, base the development
\*           branch's length when it was built (a landing is a fast-forward only
\*           from it)
\*   dev     the development branch: the cards it holds, in the order landed
\*   ci      ghost: the CI result the batch was proved with (none, green, red)
\*
\* THE MERGER, per stream, one batch at a time: Start takes the first Batch
\* queued cards in work order (the stream's Order), and none while a card of the
\* stream is stuck (a stuck card is a barrier), onto a new branch; Build merges
\* the next card into it, or Conflict feeds `--conflict` on it (the card stuck, the
\* stream stopped); Push puts the built branch on origin; CI is the checks' result
\* on the pushed head; Land fast-forwards the development branch to a green
\* branch, or Rejected feeds `--rejected` when the development branch moved;
\* Feed feeds `--batch n` once the development branch holds the batch; Red feeds
\* `--red` with the batch as suspects. The coordinator (the environment) resumes
\* a stopped stream (its stuck cards queued again) and returns cards.
\*
\* Broken: "none" is the design; "landred" lands a red branch (RedNeverLands);
\* "passstuck" goes on past a conflicting card, leaving it stuck and merging the
\* cards after it (StuckIsABarrier).
\*
\* WHAT IS NOT MODELLED. The sprint's own steps (SprintEvents.tla, DirtyTick.tla:
\* the merge step's refusals, the stream's waiting and landed states); a cross
\* need (a cause the merger never feeds); the checks' timing (the deadline is a
\* red fact); a merger restart (the batch is built again from origin, which the
\* Go test pins); the git objects themselves (a card is its head).
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Streams, Cards, StreamOf, Order, Batch, Broken

VARIABLES col, sstate, br, dev, ci

vars == <<col, sstate, br, dev, ci>>

NoBatch == [st |-> "none", cards |-> <<>>, k |-> 0, base |-> 0]

\* A card's place in its stream's work order.
Pos(c) == CHOOSE i \in 1..Len(Order[StreamOf[c]]) : Order[StreamOf[c]][i] = c

Elems(s) == {s[i] : i \in 1..Len(s)}

\* The first n cards of a sequence (all of it when shorter).
Prefix(s, n) == SubSeq(s, 1, IF Len(s) < n THEN Len(s) ELSE n)

\* The stream's queued cards in work order.
Queued(s) == SelectSeq(Order[s], LAMBDA c : col[c] = "queued")

TypeOK ==
  /\ col \in [Cards -> {"queued", "stuck", "merged", "returned"}]
  /\ sstate \in [Streams -> {"merging", "stopped"}]
  /\ \A s \in Streams : br[s].st \in {"none", "building", "pushed", "green", "red", "landed"}
  /\ \A s \in Streams : ci[s] \in {"none", "green", "red"}

Init ==
  /\ col = [c \in Cards |-> "queued"]
  /\ sstate = [s \in Streams |-> "merging"]
  /\ br = [s \in Streams |-> NoBatch]
  /\ dev = <<>>
  /\ ci = [s \in Streams |-> "none"]

\* The merger takes the batch: the head of the queue, never past a stuck card.
Start(s) ==
  /\ sstate[s] = "merging"
  /\ br[s].st = "none"
  /\ Broken = "passstuck" \/ \A c \in Cards : StreamOf[c] = s => col[c] # "stuck"
  /\ Len(Queued(s)) > 0
  /\ br' = [br EXCEPT ![s] = [st |-> "building", cards |-> Prefix(Queued(s), Batch), k |-> 0, base |-> Len(dev)]]
  /\ ci' = [ci EXCEPT ![s] = "none"]
  /\ UNCHANGED <<col, sstate, dev>>

\* The next card's head merges cleanly into the stream branch.
Build(s) ==
  /\ br[s].st = "building"
  /\ br[s].k < Len(br[s].cards)
  /\ br' = [br EXCEPT ![s].k = @ + 1]
  /\ UNCHANGED <<col, sstate, dev, ci>>

\* The next card's head conflicts: --conflict <card>; the card stuck, the stream
\* stopped, nothing pushed. Broken "passstuck" leaves it stuck and goes on.
Conflict(s) ==
  LET c == br[s].cards[br[s].k + 1] IN
  /\ br[s].st = "building"
  /\ br[s].k < Len(br[s].cards)
  /\ col' = [col EXCEPT ![c] = "stuck"]
  /\ IF Broken = "passstuck"
     THEN /\ br' = [br EXCEPT ![s].cards = SelectSeq(@, LAMBDA x : x # c)]
          /\ UNCHANGED sstate
     ELSE /\ br' = [br EXCEPT ![s] = NoBatch]
          /\ sstate' = [sstate EXCEPT ![s] = "stopped"]
  /\ UNCHANGED <<dev, ci>>

\* The built branch is pushed (an ordinary push: the branch only grows).
Push(s) ==
  /\ br[s].st = "building"
  /\ br[s].k = Len(br[s].cards)
  /\ Len(br[s].cards) > 0
  /\ br' = [br EXCEPT ![s].st = "pushed"]
  /\ UNCHANGED <<col, sstate, dev, ci>>

\* The checks on the pushed head conclude.
CI(s) ==
  /\ br[s].st = "pushed"
  /\ \E r \in {"green", "red"} :
       /\ br' = [br EXCEPT ![s].st = r]
       /\ ci' = [ci EXCEPT ![s] = r]
  /\ UNCHANGED <<col, sstate, dev>>

\* A green branch lands: the development branch fast-forwards to it, which it
\* can only from the head the branch was built on. Broken "landred" lands red.
Land(s) ==
  /\ br[s].st = "green" \/ (Broken = "landred" /\ br[s].st = "red")
  /\ Len(dev) = br[s].base
  /\ dev' = dev \o br[s].cards
  /\ br' = [br EXCEPT ![s].st = "landed"]
  /\ UNCHANGED <<col, sstate, ci>>

\* The development branch moved since the build: the push is not a fast-forward,
\* --rejected; the stream stops.
Rejected(s) ==
  /\ br[s].st = "green"
  /\ Len(dev) # br[s].base
  /\ br' = [br EXCEPT ![s] = NoBatch]
  /\ sstate' = [sstate EXCEPT ![s] = "stopped"]
  /\ UNCHANGED <<col, dev, ci>>

\* The landed batch is fed: --batch n, its cards merged.
Feed(s) ==
  /\ br[s].st = "landed"
  /\ col' = [c \in Cards |-> IF c \in Elems(br[s].cards) THEN "merged" ELSE col[c]]
  /\ br' = [br EXCEPT ![s] = NoBatch]
  /\ UNCHANGED <<sstate, dev, ci>>

\* A red branch is fed: --red, the batch the suspects; the stream stops.
Red(s) ==
  /\ br[s].st = "red"
  /\ Broken # "landred"
  /\ br' = [br EXCEPT ![s] = NoBatch]
  /\ sstate' = [sstate EXCEPT ![s] = "stopped"]
  /\ UNCHANGED <<col, dev, ci>>

\* The coordinator resumes a stopped stream: its stuck cards are queued again.
Resume(s) ==
  /\ sstate[s] = "stopped"
  /\ sstate' = [sstate EXCEPT ![s] = "merging"]
  /\ col' = [c \in Cards |-> IF StreamOf[c] = s /\ col[c] = "stuck" THEN "queued" ELSE col[c]]
  /\ UNCHANGED <<br, dev, ci>>

\* The coordinator returns a card of a stopped stream to review (off the queue).
Return(c) ==
  /\ sstate[StreamOf[c]] = "stopped"
  /\ col[c] \in {"queued", "stuck"}
  /\ col' = [col EXCEPT ![c] = "returned"]
  /\ UNCHANGED <<sstate, br, dev, ci>>

Merger(s) == Start(s) \/ Build(s) \/ Conflict(s) \/ Push(s) \/ CI(s) \/ Land(s) \/ Rejected(s) \/ Feed(s) \/ Red(s)

Next == \/ \E s \in Streams : Merger(s) \/ Resume(s)
        \/ \E c \in Cards : Return(c)

Spec == Init /\ [][Next]_vars /\ \A s \in Streams : WF_vars(Land(s)) /\ WF_vars(Rejected(s)) /\ WF_vars(Feed(s))

\* merged only grows: a merged card is never anything else.
MergedOnlyGrows == [][\A c \in Cards : col[c] = "merged" => col'[c] = "merged"]_vars

\* No card is merged, or held by the development branch, while a card before it
\* in its stream's work order is stuck or queued: a stuck card is a barrier, and
\* the queue lands in work order.
StuckIsABarrier ==
  \A c \in Elems(dev) \cup {x \in Cards : col[x] = "merged"} :
    \A d \in Cards : StreamOf[d] = StreamOf[c] /\ Pos(d) < Pos(c) => col[d] \in {"merged", "returned"} \/ d \in Elems(dev)

\* A batch the checks found red never reaches the development branch.
RedNeverLands == \A s \in Streams : br[s].st = "landed" => ci[s] = "green"

\* A --batch n fact only after the development branch holds all n.
FactsMatchBranch == \A c \in Cards : col[c] = "merged" => c \in Elems(dev)

\* One batch at a time per stream: its cards are the stream's, queued, at most Batch.
OneBatchAtATime ==
  \A s \in Streams : br[s].st # "none" =>
    /\ Len(br[s].cards) <= Batch
    /\ \A i \in 1..Len(br[s].cards) : StreamOf[br[s].cards[i]] = s /\ col[br[s].cards[i]] = "queued"

\* A green batch is landed or, the development branch having moved, rejected.
GreenSettles == \A s \in Streams : br[s].st = "green" ~> br[s].st # "green"
=============================================================================
