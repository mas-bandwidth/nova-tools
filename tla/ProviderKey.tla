----------------------------- MODULE ProviderKey -----------------------------
\* The v1.9 provider-key design (the owner, 2026-10-20: "I should have to only
\* enter the key and press enter. Rest just happens."). This module is the
\* design every v19-secrets card cites; it is written ahead of the code that
\* carries it (cmd/nova-secrets, pkg/secrets, pkg/config kind loop/provider/
\* route, and the fleet join plays).
\*
\* THE STORY IT CLOSES. Adding MINIMAX_API_KEY took a seal into the right seat,
\* a pull request wait, then a nine-seat `nova-secrets seat inject` loop that
\* refused because the checkout had not pulled the merged PR: ten-plus PRs for
\* one provider key. The design is one verb, `nova-secrets provider-key add
\* <NAME>`: prompt once (hidden), seal into the fleet's source seat and every
\* seat that runs provider routes, in one commit and one pull request, wait for
\* its own merge and pull. Every store verb fetches and fast-forwards first, so
\* a stale checkout never refuses. A provider's routes stay disabled until every
\* route-running seat carries its key, a new machine's join injects every
\* provider key, and no value passes through sudo, argv or an environment dump.
\*
\* THE STATE.
\*   seats        the store's seats, as they exist on the fleet.
\*   carries[s]   the provider key names whose sealed values seat s's file holds.
\*                It is the store's main content: only Merge and the fan-out
\*                actions (Seal's branch, Join and AddLoop) put a name there.
\*   loops        the loop rows: [seat |-> s, route |-> BOOLEAN, fixed |-> K].
\*                route is TRUE when the argv runs a nova-swarm member or
\*                reader; fixed is the loop row's hand-listed keys.
\*   branch[p]    the seats the open pull request for provider p carries the
\*                key to: the source seat plus every route seat at Seal time.
\*   pr[p]        none, open, or merged: the one pull request a key add makes.
\*   providerEnabled[p]  whether the provider's routes are dealt.
\*   prompts[p], prs[p]  how many prompts and pull requests this key add made.
\*   checkout     the owner's local store HEAD, as the seat/key pairs it holds.
\*                It may lag main; every verb reads it only after Fetch.
\*   fetched      the main content the local repository last fetched.
\*   judged[p]    the one judgment DisableWhenMissing raised for p.
\*   valueAt[p]   where p's value is: none, the one prompt, or the store. No
\*                other variable can hold it; the model has no argv or env
\*                channel for a value (NoValueOutsideTheStore).
\*
\* DERIVED, NEVER STORED.
\*   routeSeats == the seats of the loop rows that run a member or reader.
\*                The design derives it from the loop rows; the "handlist"
\*                witness replaces it with the hand list HandSeats. A Join
\*                adds a route loop, so the derived list grows and the hand
\*                list does not.
\*   unitLoads(l) == the names loop l's unit loads: its fixed keys plus every
\*                enabled provider's key_env that its seat actually holds. A
\*                key the seat lacks is not loaded, which is what
\*                UnitsLoadEveryEnabledKey measures.
\*   keyOf[p]     the provider row's key_env: the NAME, never the value.
\*
\* ACTIONS.
\*   Prompt(p)    the one input: the value. It is asked once, and the value is
\*                in no variable but carries (valueAt says prompt, then store).
\*                (cmd/nova-secrets seal.go:15 runSealCLI hides the prompt;
\*                pkg/secrets/seal.go:134 RunSeal reads it once.)
\*   Seal(p)      one commit on one branch: the source seat and every route
\*                seat in routeSeats. It needs a fresh checkout, the stale-
\*                refusal witness's first guard. (pkg/secrets/seal.go:291
\*                sealCarry.carry; the design adds the fan-out.)
\*   OpenPR(p)    that commit becomes the one pull request of the key add.
\*   Merge(p)     the forge's action, outside the tool and never fair in Spec.
\*   Fetch        every store verb's first step: the local refs become main.
\*                (the design; today docs/SPEC-SECRETS.md says the store verbs
\*                never fetch, and pkg/secrets/git.go:374 ValidateAdmissibleStore
\*                refuses a HEAD behind its ref instead.)
\*   Pull         fast-forward the checkout to the fetched main.
\*   EnableRoute(p)  guarded: every seat of routeSeats carries keyOf[p]. A
\*                stale checkout never enables from a stale read. (the design;
\*                today pkg/config/kind.go:677 route enabled is the hand switch)
\*   AddLoop(row) a loop row is written; a route row fans the available provider
\*                keys out to its seat, so the unit loads them. (pkg/config/
\*                kind.go:629 KindLoop, the seat and keys fields.)
\*   Join         a new machine's seat is made and every provider key the fleet
\*                holds is injected in the same commit. (the design; today
\*                fleet/join.yml and fleet/group_vars/all.yml hold the hand
\*                list join_seat_names.)
\*   DisableWhenMissing(p)  a route seat added later that lacks the key turns
\*                the provider's routes off, with one judgment, until it
\*                carries it.
\*
\* INVARIANTS.
\*   EnabledOnlyWhenEverySeatCarries  a provider is enabled only while every
\*                true route seat (derived from the loop rows, never the
\*                hand list) carries keyOf[p].
\*   OnePromptOnePR  one prompt and one pull request per key add.
\*   UnitsLoadEveryEnabledKey  every route-running unit loads keyOf[p] of every
\*                enabled provider.
\*   NoValueOutsideTheStore  the value is in no variable but carries; the only
\*                other places are the one prompt and the store.
\*   Liveness    after Prompt, once the forge merges, the key is on every route
\*                seat, the checkout is pulled, and the provider can be enabled
\*                with no further input. Only the tool's own actions are fair;
\*                Merge is the outside forge and is not.
\*
\* Broken = "none" is the design. Each other value is one reversed witness and
\* the invariant it breaks:
\*   "enableearly"   EnableRoute with no carry guard: EnabledOnlyWhenEverySeatCarries
\*   "stalerefuses"  verbs skip Fetch and refuse on a stale checkout: Liveness
\*   "handlist"      routeSeats is the constant HandSeats a Join does not
\*                   extend: EnabledOnlyWhenEverySeatCarries after Join
\*   "joinmisses"    Join injects no provider key: UnitsLoadEveryEnabledKey

EXTENDS Naturals, Sequences

CONSTANTS Seats, SourceSeat, OtherSeat, JoinSeat, Providers,
          HandSeats, Broken

\* A provider's key_env is only a name, derived here so the configuration says
\* nothing about a value. The model's providers are strings.
keyOf == [p \in Providers |-> "KEY_" \o p]
KeyNames == {keyOf[p] : p \in Providers}

RouteLoop == [seat |-> SourceSeat, route |-> TRUE, fixed |-> {}]
OtherRoute == [seat |-> OtherSeat, route |-> TRUE, fixed |-> {}]
PlainLoop == [seat |-> SourceSeat, route |-> FALSE, fixed |-> {"SEAT_KEY"}]
JoinLoop == [seat |-> JoinSeat, route |-> TRUE, fixed |-> {}]
PossibleLoops == {RouteLoop, OtherRoute, PlainLoop, JoinLoop}
InitialLoops == {RouteLoop}

ASSUME SourceSeat \in Seats /\ OtherSeat \in Seats /\ JoinSeat \in Seats
ASSUME SourceSeat # OtherSeat /\ SourceSeat # JoinSeat /\ OtherSeat # JoinSeat
ASSUME HandSeats \subseteq Seats
ASSUME Broken \in {"none", "enableearly", "stalerefuses", "handlist", "joinmisses"}

VARIABLES
  seats, carries, loops, branch, pr, providerEnabled,
  prompts, prs, checkout, fetched, judged, valueAt

vars == <<seats, carries, loops, branch, pr, providerEnabled,
          prompts, prs, checkout, fetched, judged, valueAt>>

PairSet == UNION {{[seat |-> s, key |-> k] : k \in KeyNames} : s \in Seats}
PairsOf(m) == UNION {{[seat |-> s, key |-> k] : k \in m[s]} : s \in Seats}

TrueRouteSeats == {l.seat : l \in {row \in loops : row.route}}
routeSeats == IF Broken = "handlist" THEN HandSeats ELSE TrueRouteSeats

\* The keys a new route seat can be given: every provider key the source seat
\* holds, on main or on the open branch the source seat's commit holds.
SourceKeys ==
  {keyOf[p] : p \in {q \in Providers :
                       keyOf[q] \in carries[SourceSeat] \/ SourceSeat \in branch[q]}}

MainPairs == PairsOf(carries)
Fresh == checkout = MainPairs

TypeOK ==
  /\ seats \subseteq Seats
  /\ carries \in [Seats -> SUBSET KeyNames]
  /\ loops \subseteq PossibleLoops
  /\ branch \in [Providers -> SUBSET Seats]
  /\ pr \in [Providers -> {"none", "open", "merged"}]
  /\ providerEnabled \in [Providers -> BOOLEAN]
  /\ prompts \in [Providers -> 0..1]
  /\ prs \in [Providers -> 0..1]
  /\ checkout \in SUBSET PairSet
  /\ fetched \in SUBSET PairSet
  /\ judged \in [Providers -> BOOLEAN]
  /\ valueAt \in [Providers -> {"none", "prompt", "store"}]

Init ==
  /\ seats = {SourceSeat, OtherSeat}
  /\ carries = [s \in Seats |-> {}]
  /\ loops = InitialLoops
  /\ branch = [p \in Providers |-> {}]
  /\ pr = [p \in Providers |-> "none"]
  /\ providerEnabled = [p \in Providers |-> FALSE]
  /\ prompts = [p \in Providers |-> 0]
  /\ prs = [p \in Providers |-> 0]
  /\ checkout = PairsOf(carries)
  /\ fetched = PairsOf(carries)
  /\ judged = [p \in Providers |-> FALSE]
  /\ valueAt = [p \in Providers |-> "none"]

\* The one input: the value, hidden, asked once. Nothing else is asked.
Prompt(p) ==
  /\ p \in Providers
  /\ prompts[p] = 0
  /\ pr[p] = "none"
  /\ prompts' = [prompts EXCEPT ![p] = 1]
  /\ valueAt' = [valueAt EXCEPT ![p] = "prompt"]
  /\ UNCHANGED <<seats, carries, loops, branch, pr, providerEnabled,
                  prs, checkout, fetched, judged>>

\* One commit into the source seat and every route seat. A stale checkout
\* refuses (the stalerefuses witness skips Fetch, so this can never run).
Seal(p) ==
  /\ p \in Providers
  /\ prompts[p] = 1
  /\ pr[p] = "none"
  /\ branch[p] = {}
  /\ Fresh
  /\ branch' = [branch EXCEPT ![p] = ({SourceSeat} \cup routeSeats)]
  /\ valueAt' = [valueAt EXCEPT ![p] = "store"]
  /\ UNCHANGED <<seats, carries, loops, pr, providerEnabled,
                  prompts, prs, checkout, fetched, judged>>

\* The one pull request a key add opens.
OpenPR(p) ==
  /\ p \in Providers
  /\ pr[p] = "none"
  /\ branch[p] # {}
  /\ pr' = [pr EXCEPT ![p] = "open"]
  /\ prs' = [prs EXCEPT ![p] = 1]
  /\ UNCHANGED <<seats, carries, loops, branch, providerEnabled,
                  prompts, checkout, fetched, judged, valueAt>>

\* The forge merges the pull request. It is outside the tool: no fairness is
\* given to it, so Liveness begins only once it has happened.
Merge(p) ==
  /\ p \in Providers
  /\ pr[p] = "open"
  /\ carries' = [s \in Seats |->
                   carries[s] \cup (IF s \in branch[p] THEN {keyOf[p]} ELSE {})]
  /\ branch' = [branch EXCEPT ![p] = {}]
  /\ pr' = [pr EXCEPT ![p] = "merged"]
  /\ UNCHANGED <<seats, loops, providerEnabled, prompts, prs,
                  checkout, fetched, judged, valueAt>>

\* Every store verb's first step. The stalerefuses witness skips it.
Fetch ==
  /\ fetched' = IF Broken = "stalerefuses" THEN fetched ELSE PairsOf(carries)
  /\ UNCHANGED <<seats, carries, loops, branch, pr, providerEnabled,
                  prompts, prs, checkout, judged, valueAt>>

\* Fast-forward the checkout to the fetched main.
Pull ==
  /\ fetched = PairsOf(carries)
  /\ checkout' = fetched
  /\ UNCHANGED <<seats, carries, loops, branch, pr, providerEnabled,
                  prompts, prs, fetched, judged, valueAt>>

\* Route it only when every route seat carries the key, from a fresh checkout.
\* The enableearly witness drops the carry guard.
EnableRoute(p) ==
  /\ p \in Providers
  /\ Fresh
  /\ (Broken = "enableearly" \/ \A s \in routeSeats : keyOf[p] \in carries[s])
  /\ providerEnabled' = [providerEnabled EXCEPT ![p] = TRUE]
  /\ UNCHANGED <<seats, carries, loops, branch, pr, prompts, prs,
                  checkout, fetched, judged, valueAt>>

\* A loop row is written. A route row fans the available provider keys out to
\* its seat in the same step, so the unit's load list has them; routeSeats is
\* the derived list unless the handlist witness replaced it.
AddLoop(row) ==
  /\ row \in PossibleLoops \ loops
  /\ row.seat \in seats
  /\ (row.route =>
        (\A p \in Providers : providerEnabled[p] => keyOf[p] \in (carries[row.seat] \cup SourceKeys)))
  /\ loops' = loops \cup {row}
  /\ carries' =
       IF row.route
         THEN [s \in Seats |->
                 carries[s] \cup
                   (IF s \in (IF Broken = "handlist"
                                THEN HandSeats
                                ELSE {x.seat : x \in {y \in loops' : y.route}})
                      THEN SourceKeys ELSE {})]
         ELSE carries
  /\ UNCHANGED <<seats, branch, pr, providerEnabled, prompts, prs,
                  checkout, fetched, judged, valueAt>>

\* A new machine joins: its seat is made and every provider key the fleet holds
\* is injected in the same step. The handlist witness fans out by the old hand
\* list; the joinmisses witness injects nothing.
Join ==
  /\ JoinSeat \notin seats
  /\ JoinLoop \notin loops
  /\ seats' = seats \cup {JoinSeat}
  /\ loops' = loops \cup {JoinLoop}
  /\ LET NewRouteSeats == IF Broken = "handlist"
                           THEN HandSeats
                           ELSE {x.seat : x \in {y \in loops' : y.route}}
         Injected == IF Broken = "joinmisses" THEN {} ELSE SourceKeys
     IN carries' = [s \in Seats |->
                      carries[s] \cup (IF s \in NewRouteSeats THEN Injected ELSE {})]
  /\ UNCHANGED <<branch, pr, providerEnabled, prompts, prs,
                  checkout, fetched, judged, valueAt>>

\* A route seat added later that lacks the key turns the provider's routes off,
\* with one judgment, until it carries it.
DisableWhenMissing(p) ==
  /\ p \in Providers
  /\ providerEnabled[p]
  /\ ~judged[p]
  /\ \E s \in TrueRouteSeats : keyOf[p] \notin carries[s]
  /\ providerEnabled' = [providerEnabled EXCEPT ![p] = FALSE]
  /\ judged' = [judged EXCEPT ![p] = TRUE]
  /\ UNCHANGED <<seats, carries, loops, branch, pr, prompts, prs,
                  checkout, fetched, valueAt>>

Next ==
  \/ (\E p \in Providers :
        Prompt(p) \/ Seal(p) \/ OpenPR(p) \/ Merge(p) \/
        EnableRoute(p) \/ DisableWhenMissing(p))
  \/ Fetch
  \/ Pull
  \/ (\E row \in PossibleLoops : AddLoop(row))
  \/ Join

\* Weak fairness of the tool's own actions. Merge (the forge) and Prompt (the
\* one input) are not fair: Liveness starts after Prompt and after the merge.
Spec ==
  Init /\ [][Next]_vars
  /\ WF_vars(\E p \in Providers : Seal(p))
  /\ WF_vars(\E p \in Providers : OpenPR(p))
  /\ WF_vars(Fetch)
  /\ WF_vars(Pull)
  /\ WF_vars(\E p \in Providers : EnableRoute(p))

\* A provider is enabled only while every true route seat carries its key.
EnabledOnlyWhenEverySeatCarries ==
  \A p \in Providers :
    providerEnabled[p] =>
      \A s \in TrueRouteSeats : keyOf[p] \in carries[s]

\* One key add makes exactly one prompt and one pull request.
OnePromptOnePR ==
  \A p \in Providers :
    /\ prompts[p] <= 1
    /\ prs[p] <= 1
    /\ prs[p] <= prompts[p]

\* A unit's loads: its fixed keys and the enabled provider keys its seat holds.
unitLoads(l) ==
  l.fixed \cup
    {keyOf[p] : p \in {q \in Providers :
                        providerEnabled[q] /\ keyOf[q] \in carries[l.seat]}}

UnitsLoadEveryEnabledKey ==
  \A l \in loops :
    l.route =>
      \A p \in Providers :
        providerEnabled[p] => keyOf[p] \in unitLoads(l)

\* The value is only ever in the one prompt or the store; never argv or env.
NoValueOutsideTheStore ==
  \A p \in Providers : valueAt[p] \in {"none", "prompt", "store"}

AllRouteSeatsCarry(p) ==
  \A s \in TrueRouteSeats : keyOf[p] \in carries[s]

Liveness ==
  \A p \in Providers :
    ((prompts[p] = 1 /\ pr[p] = "merged")
      ~> (AllRouteSeatsCarry(p) /\ checkout = MainPairs /\ providerEnabled[p]))

=============================================================================
