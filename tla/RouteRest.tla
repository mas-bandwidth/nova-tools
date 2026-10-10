------------------------------- MODULE RouteRest ------------------------------
\* When a route rests (internal/sprint/route_rest.go, provider_funds.go,
\* balance.go, route_coordinator.go). The owner, 2026-10-10, through the seat:
\* "this sounds like something the machine should not do. it should raise it to
\* you as a thing to do, but not do it automatically." A route rests only on a
\* provider-typed failure or the coordinator's word; a take that ended with no
\* result is the model's output on that card and never rests the route; a low
\* balance is a judgment raised to the coordinator, never a rest.
\*
\* The state, for one provider and its routes: provRest, the provider's one
\* rest property (none, credit: it refused a take for want of credit, auth: it
\* refused the seat's key, coord: the coordinator's routes rest); own[r], the
\* route's own rest line (none, provider: its window of ended takes held
\* After transient provider failures, coord: the coordinator's); win[r], the
\* route's ended takes since its last rest, the newest W; low, the balance poll's
\* last read is not over one hour of the spend; judged, the low-on-funds
\* judgment is open; event, what the last step was (the record the rules read).
\*
\* The design, Broken = {}:
\*   TakeEnds   a take on a serving route ends (ok, noresult, transient: a 429,
\*              a 5xx, a timeout at the provider, credit: a 402, auth: a 401);
\*              the tick that sees it rests the provider on a refusal, and the
\*              route when its window holds After transient failures. A
\*              no-result counts in the window and never toward a rest.
\*   Balance    the poll reads the balance low or not: it writes no rest.
\*   Raise      the tick opens the low-on-funds judgment while the balance is
\*              low, and closes it when it is not.
\*   Pay        a payment seen by the poll, or funded: a credit rest ends.
\*   Rest       the coordinator rests the provider or one route.
\*   Wake       the coordinator ends the provider's rest (any cause) and its
\*              routes' own, or one route's own while its provider serves.
\*   Expire     a timed rest (transient, the coordinator's with a time) ends by
\*              the clock; a credit rest and an auth rest have no time (a refused
\*              key does not mend itself: the owner replaces it, then Wake).
\* Reversed witnesses:
\*   "noresult" the retired rule 3: a no-result counts toward the window's
\*              rest: RestOnlyByProviderOrCoordinator
\*   "balance"  the retired balance rest: a low read rests the provider:
\*              RestOnlyByProviderOrCoordinator
\*   "silent"   the tick never raises the low-on-funds judgment: LowIsRaised
\*   "authclock" the retired timed key rest (RouteRestFor, until 2026-10-10): the
\*              clock ends an auth rest: AuthEndsOnlyWoken
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Routes, W, After, Broken

VARIABLES provRest, own, win, low, judged, event
vars == <<provRest, own, win, low, judged, event>>

Kinds == {"ok", "noresult", "transient", "credit", "auth"}
Events == Kinds \cup {"balance", "raise", "payment", "funded", "coord", "wake", "clock"}

\* the bounded windows of ended takes
Windows == UNION {[1..n -> Kinds] : n \in 0..W}

TypeOK ==
    /\ provRest \in {"none", "credit", "auth", "coord", "balance"}
    /\ own \in [Routes -> {"none", "provider", "coord"}]
    /\ win \in [Routes -> Windows]
    /\ low \in BOOLEAN
    /\ judged \in BOOLEAN
    /\ event \in Events \cup {"init"}

Init ==
    /\ provRest = "none"
    /\ own = [r \in Routes |-> "none"]
    /\ win = [r \in Routes |-> << >>]
    /\ low = FALSE
    /\ judged = FALSE
    /\ event = "init"

Resting(r) == provRest # "none" \/ own[r] # "none"

\* the newest W of a window with one more end
Push(s, k) == IF Len(s) < W THEN Append(s, k) ELSE Append(Tail(s), k)

\* the ends that count toward a route's own rest (RestsDue: transient only)
Counts(k) == k = "transient" \/ ("noresult" \in Broken /\ k = "noresult")

CountIn(s) == Cardinality({i \in 1..Len(s) : Counts(s[i])})

\* a take on a serving route ends; the tick's rests in the same step
TakeEnds(r, k) ==
    /\ ~Resting(r)
    /\ LET w == Push(win[r], k) IN
         /\ provRest' = IF k = "credit" THEN "credit" ELSE IF k = "auth" THEN "auth" ELSE provRest
         /\ IF k \notin {"credit", "auth"} /\ CountIn(w) >= After
              THEN /\ own' = [own EXCEPT ![r] = "provider"]
                   /\ win' = [win EXCEPT ![r] = << >>]
              ELSE /\ own' = own
                   /\ win' = [win EXCEPT ![r] = w]
    /\ event' = k
    /\ UNCHANGED <<low, judged>>

\* the poll reads the balance: it writes no rest
Balance(l) ==
    /\ low' = l
    /\ provRest' = IF "balance" \in Broken /\ l /\ provRest = "none" THEN "balance" ELSE provRest
    /\ event' = "balance"
    /\ UNCHANGED <<own, win, judged>>

\* the tick holds the low-on-funds judgment open exactly while the balance is low
Raise ==
    /\ "silent" \notin Broken
    /\ judged # low
    /\ judged' = low
    /\ event' = "raise"
    /\ UNCHANGED <<provRest, own, win, low>>

\* a payment the poll sees, or funded: a credit rest ends
Pay(e) ==
    /\ provRest = "credit"
    /\ provRest' = "none"
    /\ event' = e
    /\ UNCHANGED <<own, win, low, judged>>

\* the coordinator rests the provider, or one route
RestProvider ==
    /\ provRest = "none"
    /\ provRest' = "coord"
    /\ event' = "coord"
    /\ UNCHANGED <<own, win, low, judged>>

RestRoute(r) ==
    /\ own[r] = "none"
    /\ own' = [own EXCEPT ![r] = "coord"]
    /\ event' = "coord"
    /\ UNCHANGED <<provRest, win, low, judged>>

\* the coordinator wakes the provider (its rest, any cause, and its routes' own),
\* or one route's own while its provider serves
WakeProvider ==
    /\ \E r \in Routes : Resting(r)
    /\ provRest' = "none"
    /\ own' = [r \in Routes |-> "none"]
    /\ event' = "wake"
    /\ UNCHANGED <<win, low, judged>>

WakeRoute(r) ==
    /\ provRest = "none"
    /\ own[r] # "none"
    /\ own' = [own EXCEPT ![r] = "none"]
    /\ event' = "wake"
    /\ UNCHANGED <<provRest, win, low, judged>>

\* the clock ends a timed rest; a credit rest has none
ExpireRoute(r) ==
    /\ own[r] # "none"
    /\ own' = [own EXCEPT ![r] = "none"]
    /\ event' = "clock"
    /\ UNCHANGED <<provRest, win, low, judged>>

ExpireProvider ==
    /\ provRest \in IF "authclock" \in Broken THEN {"auth", "coord"} ELSE {"coord"}
    /\ provRest' = "none"
    /\ event' = "clock"
    /\ UNCHANGED <<own, win, low, judged>>

Next ==
    \/ \E r \in Routes, k \in Kinds : TakeEnds(r, k)
    \/ \E l \in BOOLEAN : Balance(l)
    \/ Raise
    \/ \E e \in {"payment", "funded"} : Pay(e)
    \/ RestProvider
    \/ \E r \in Routes : RestRoute(r)
    \/ WakeProvider
    \/ \E r \in Routes : WakeRoute(r)
    \/ \E r \in Routes : ExpireRoute(r)
    \/ ExpireProvider

Spec == Init /\ [][Next]_vars /\ WF_vars(Raise)

\* ---------------------------------------------------------------- the rules

\* a serving route begins to rest only on a provider-typed failure or the
\* coordinator's word: never a no-result, never a balance read
RestOnlyByProviderOrCoordinator ==
    [][\A r \in Routes : (~Resting(r) /\ Resting(r)') => event' \in {"transient", "credit", "auth", "coord"}]_vars

\* a provider's credit rest ends only on a payment seen, funded, or the
\* coordinator's wake: never by the clock
CreditEndsOnlyPaidOrWoken ==
    [][(provRest = "credit" /\ provRest' # "credit") => event' \in {"payment", "funded", "wake"}]_vars

\* a provider's auth rest ends only on the coordinator's wake: never by the
\* clock, never by a payment
AuthEndsOnlyWoken ==
    [][(provRest = "auth" /\ provRest' # "auth") => event' = "wake"]_vars

\* a no-result step never changes a rest
NoResultNeverRests ==
    [][event' = "noresult" => (provRest' = provRest /\ own' = own)]_vars

\* a balance low long enough is raised to the coordinator as a judgment
LowIsRaised == [](low => <>(judged \/ ~low))

=============================================================================
