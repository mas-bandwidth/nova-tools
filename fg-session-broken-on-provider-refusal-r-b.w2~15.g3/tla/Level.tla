------------------------------- MODULE Level -------------------------------
\* The fleet's level (internal/sprint/steps_work.go level, round.go levelTo):
\* the rebalance that runs at the start of every tick and inside every planner
\* that computes holds (held.go). One call is the loop this module models: while
\* the longest backlog of a member with a ready card and the emptiest open member
\* differ by more than one, a ready card of the longest moves to the member
\* levelTo names. The call has to end. On 2026-10-02 at 2:26 PM it did not: the
\* emptiest member had refused the longest queue's newest card at staging, the
\* card went to a member one below instead, the receiver became the donor, and
\* the card went back, a Unit appended per turn under the server's mutex, until
\* the process held 244 GB (nova-tools#5122).
\*
\* THE STATE, per call.
\*   q      each member's queue of cards the call may still move (queues[m]):
\*          its ready cards in work order at the start; the newest is last
\*   work   each member's cards held that are not in its queue: working, cards a
\*          sweep placed on it in the same plan, and cards this call moved onto
\*          it. held is work plus the queue
\*   loc    the member each card is on
\*   ref    each card's StagingRefusers, fixed for the call
\*   up     the order the call reads the members up in (s.UpMembers(): the
\*          fleet's row order, not always name order); the first of equals is
\*          long, and short
\*   idx    where the deal's rolling index starts a scan (round.start(): the
\*          counter modulo the number of members, over the names in name order)
\*   moves  the moves the call made (past the ready count it goes between that
\*          count and one more, so a loop that does not end is a cycle TLC can
\*          find, and a move is never a stutter)
\*   pc     start (before Setup picks the call's start), run, or done (the
\*          loop returned)
\*   nx     the loop's next move on the state as it stands (ChoiceBy),
\*          computed once per state
\*
\* THE RULE (Rule = "gap", Requeue = FALSE: the design). A member's backlog is
\* its cards held less its width. levelTo(c, from) is the first member round the
\* fleet from idx that is open (held under DealAhead times its width), is no
\* refuser of c, and sits at least two below from, whose backlog is below the up
\* members' mean rounded down, or, when none such is below it, at it. The card
\* is the newest of the longest queue that has a target: the gap is tested where
\* the card is chosen, so a newest card whose only target is one below does not
\* stop an older card that has one (Zhi, zhi-b7086744a0b9). Two guards end the
\* loop, each alone:
\*   - the potential (PR #5127): a move from a backlog L to a backlog T changes
\*     the sum of squared backlogs by 2(T - L + 1), at most -2 when L - T >= 2
\*     (PotentialFalls), and the sum is never below zero;
\*   - the ready count: a card the call moves leaves the queues and is not
\*     queued on its receiver, so no card moves twice in one call and the call
\*     makes at most as many moves as there were ready cards (MovesBounded).
\*     With the moved card appended to its receiver's queue (Requeue, as PR
\*     #5127 left it) TLC finds a call that moves one card twice and makes four
\*     moves for three ready cards (MCLevelBrokenRequeue); the engine's second
\*     unit for a card is guarded on the place the card had before the first,
\*     so the plan it writes is not the plan it computed.
\*
\* RULE and REQUEUE name the reversed witnesses: Rule "old" with Requeue (b7776ca3,
\* the wedge: the refusers and the longest avoided, no gap: the receiver one
\* below becomes the donor and the card goes back), "nogap" with Requeue (PR
\* #5127 with its gap test removed: a guard removed, the loop does not end),
\* "noskip" (the gap without the refusers avoided: a card goes onto a member
\* that refused it at staging, the #5000 probe), "naive" (the gap applied where
\* the loop ends rather than where the card is chosen: only the newest card is
\* tried), Requeue alone (PR #5127 as it stands). Rule "old" without Requeue is
\* the second guard alone ending the rule of the wedge (MCLevelOldRuleNoRequeue).
\*
\* WHAT IS NOT MODELLED. The readers' level (readers.go levelReads: a read moves
\* at most once there already, it is never queued on its receiver), the plan's
\* units and generations (a move is one unit), members not up (level is handed
\* the up members), the deal and the sweep before the level (their cards are in
\* work), the store. A bounded instance of the loop checked over every start the
\* constants allow, not a proof for every fleet.
EXTENDS Integers, Sequences, FiniteSets

CONSTANTS Order,       \* the members, a sequence in name order (round.order)
          Cards,       \* the ready cards at the start, a sequence in work order
          Width,       \* [member -> width]: s.Width(m); open is held < 2 * width
          Works,       \* [member -> the counts of cards outside its queue it may start with]
          MaxRefusers, \* the most refusers a card starts with
          UpOrders,    \* the orders s.UpMembers() may give: a set of sequences
          Rule,        \* "gap" (the design) or a reversed witness
          Requeue      \* TRUE: a moved card joins its receiver's queue (PR #5127)

ASSUME Rule \in {"gap", "old", "nogap", "noskip", "naive"}
ASSUME Requeue \in BOOLEAN
\* the mean is rounded down below zero too, as level computes it
ASSUME (-3) \div 2 = -2 /\ (-4) \div 2 = -2 /\ 3 \div 2 = 1

N == Len(Order)
Members == {Order[i] : i \in 1..N}
CardSet == {Cards[i] : i \in 1..Len(Cards)}
Ready == Len(Cards)  \* the ready cards at the start
Cap == Ready + 1
None == "none"
Room(m) == 2 * Width[m]  \* DealAhead (width.go)

VARIABLES q, work, loc, ref, up, idx, moves, pc, nx
vars == <<q, work, loc, ref, up, idx, moves, pc, nx>>

Held(qq, ww, m) == ww[m] + Len(qq[m])
Backlogs(qq, ww) == [m \in Members |-> Held(qq, ww, m) - Width[m]]
RECURSIVE SumOver(_, _)
SumOver(f, i) == IF i = 0 THEN 0 ELSE f[Order[i]] + SumOver(f, i - 1)
Phi == LET n == Backlogs(q, work) IN SumOver([m \in Members |-> n[m] * n[m]], N)

\* the first of the up order with the most backlog among members with a card
\* in their queue (long), and with the least among the open members (short)
First(o, ok(_), better(_, _)) ==
  LET is == {i \in 1..Len(o) : ok(o[i])}
      best == {i \in is : \A j \in is : (j < i => better(o[i], o[j])) /\ (j > i => ~better(o[j], o[i]))}
  IN IF best = {} THEN None ELSE o[CHOOSE i \in best : TRUE]

\* round.scan from ii over the names in name order
At(ii, k) == Order[((ii + k) % N) + 1]
Scan(ii, s) == IF s = {} THEN None
               ELSE At(ii, CHOOSE k \in 0..N - 1 : At(ii, k) \in s /\ \A j \in 0..k - 1 : At(ii, j) \notin s)
Pos(m) == CHOOSE i \in 1..N : Order[i] = m

\* levelTo under a rule, on the backlogs n and their mean
Avoid(r, rf, c, from) == CASE r = "old" -> rf[c] \cup {from}
                       [] r = "noskip" -> {}
                       [] OTHER -> rf[c]
TargetBy(r, qq, ww, ii, rf, n, mean, c, from) ==
  LET open == {x \in Members : Held(qq, ww, x) < Room(x) /\ x \notin Avoid(r, rf, c, from)
                               /\ (r \in {"old", "nogap"} \/ n[from] - n[x] > 1)}
      below == {x \in open : n[x] < mean}
  IN IF below # {} THEN Scan(ii, below) ELSE Scan(ii, {x \in open : n[x] <= mean})

Stop == [stop |-> TRUE, from |-> None, i |-> 0, to |-> None]

\* the loop's next move on a state, under a rule: stop, or the card at i of
\* from's queue going to to (level's loop body, one turn)
ChoiceBy(r, qq, ww, ii, uu, rf) ==
  LET n == Backlogs(qq, ww)
      mean == SumOver(n, N) \div N
      l == First(uu, LAMBDA m : Len(qq[m]) > 0, LAMBDA x, y : n[x] > n[y])
      s == First(uu, LAMBDA m : Held(qq, ww, m) < Room(m), LAMBDA x, y : n[x] < n[y])
      movable == IF l = None THEN {} ELSE {i \in 1..Len(qq[l]) : TargetBy(r, qq, ww, ii, rf, n, mean, qq[l][i], l) # None}
      i == IF r = "naive"
           THEN IF l # None /\ Len(qq[l]) \in movable THEN Len(qq[l]) ELSE 0
           ELSE IF movable = {} THEN 0 ELSE CHOOSE i \in movable : \A j \in movable : j <= i
  IN IF l = None \/ s = None \/ n[l] - n[s] <= 1 \/ i = 0 THEN Stop
     ELSE [stop |-> FALSE, from |-> l, i |-> i, to |-> TargetBy(r, qq, ww, ii, rf, n, mean, qq[l][i], l)]

Drop(s, i) == SubSeq(s, 1, i - 1) \o SubSeq(s, i + 1, Len(s))
Placed(at, m) == SelectSeq(Cards, LAMBDA c : at[c] = m)


\* one initial state; Setup picks every start the constants allow. With nx,
\* the move computed once per state, it keeps TLC on an instance near a minute
\* (the same checks from a start per initial state, recomputing the move, took
\* four times as long)
Init ==
  /\ q = [m \in Members |-> <<>>]
  /\ work = [m \in Members |-> 0]
  /\ loc = [c \in CardSet |-> Order[1]]
  /\ ref = [c \in CardSet |-> {}]
  /\ up = Order
  /\ idx = 0
  /\ moves = 0
  /\ pc = "start"
  /\ nx = Stop

Setup ==
  /\ pc = "start"
  /\ ref' \in [CardSet -> SUBSET Members]
  /\ \A c \in CardSet : Cardinality(ref'[c]) <= MaxRefusers
  /\ work' \in [Members -> UNION {Works[m] : m \in Members}]
  /\ \A m \in Members : work'[m] \in Works[m]
  /\ loc' \in [CardSet -> Members]
  /\ \A c \in CardSet : loc'[c] \notin ref'[c]  \* the deal never places a card on its refuser
  /\ q' = [m \in Members |-> Placed(loc', m)]
  /\ up' \in UpOrders
  /\ idx' \in 0..N - 1
  /\ moves' = 0
  /\ pc' = "run"
  /\ nx' = ChoiceBy(Rule, q', work', idx', up', ref')

\* the loop returns
Ends ==
  /\ pc = "run"
  /\ nx.stop
  /\ pc' = "done"
  /\ UNCHANGED <<q, work, loc, ref, up, idx, moves, nx>>

\* one move: the card leaves from's queue for to, and joins to's queue only
\* under Requeue
Move ==
  /\ pc = "run"
  /\ ~nx.stop
  /\ LET l == nx.from
         to == nx.to
         c == q[l][nx.i]
         q2 == [m \in Members |->
                 IF m = l /\ m = to /\ Requeue THEN Append(Drop(q[l], nx.i), c)
                 ELSE IF m = l THEN Drop(q[l], nx.i)
                 ELSE IF m = to /\ Requeue THEN Append(q[to], c)
                 ELSE q[m]]
         w2 == IF Requeue THEN work ELSE [work EXCEPT ![to] = @ + 1]
         i2 == Pos(to) % N  \* round.moved: the next scan starts past to
     IN /\ q' = q2
        /\ work' = w2
        /\ loc' = [loc EXCEPT ![c] = to]
        /\ idx' = i2
        /\ moves' = IF moves < Cap THEN moves + 1 ELSE Cap - 1
        /\ nx' = ChoiceBy(Rule, q2, w2, i2, up, ref)
        /\ UNCHANGED <<ref, up, pc>>

Next == Setup \/ Move \/ Ends

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ q \in [Members -> Seq(CardSet)]
  /\ work \in [Members -> Nat]
  /\ loc \in [CardSet -> Members]
  /\ idx \in 0..N - 1
  /\ moves \in 0..Cap
  /\ pc \in {"start", "run", "done"}

\* no card is ever on a member that refused it at staging (CardContract's
\* NeverOnARefuser, over the whole loop)
NeverOnARefuser == \A c \in CardSet : loc[c] \notin ref[c]

\* the call makes no more moves than there were ready cards
MovesBounded == moves <= Ready

\* the loop returns only when no card in the longest queue has a target under
\* the design's rule: an older card is tried when the newest has none
SelectionComplete == pc = "done" => ChoiceBy("gap", q, work, idx, up, ref).stop

\* every move lowers the sum of squared backlogs by at least two
PotentialFalls == [][Move => Phi' <= Phi - 2]_vars

\* the call returns, when it keeps taking steps until it has. Fair is
\* WF_vars(Next) written out: every Move changes moves, so a step of Next that
\* changes vars is enabled exactly while pc # "done"; TLC reads no ENABLED (on
\* this module it made the check crawl).
Fair == []<>(pc = "done") \/ []<><<Next>>_vars
Terminates == Fair => <>(pc = "done")
=============================================================================
