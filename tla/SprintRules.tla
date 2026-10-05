----------------------------- MODULE SprintRules -----------------------------
\* The machine feeds itself (the coordinator, for the owner, 2026-10-04 at 1:36 PM: "I want this
\* sort of oh no fleet is idle, do judgement, release more cards thing -- i want this
\* more automated."). Five small state machines of nova-sprint, each its own Part, each
\* checked alone; docs/SPEC-SPRINT.md section 2 ("A card replaced by its twin") and
\* section 8 ("Answered by rule", and the idle alarm in section 14), internal/sprint
\* twins.go, rules.go and idle.go, cmd/nova-sprint landgo.go (the base gate).
\*
\* Part "twins": an old card o, its twin t, and two waiting cards that need o.
\*   Replace (add --replaces) re-points every waiting need of o to t, drops o if it is
\*   still open, and answers the blocked judgments that named o, in one step; Relink
\*   does the same for a drop and an add made apart. Drop raises a blocked judgment on
\*   every waiting card that needs the dropped card (the code's Drop).
\* Part "rules": one card's attempts under the failed and bound rules: tiers 1 (flash),
\*   2 (pro), 3 (heavy), 4 (a friend's card, where no rule answers: a mind does).
\* Part "late": one work card past its deadline, its holder's progress stamps (the `progress`
\*   verb, an outside event: Stamp, and Silence as the stamp ages past the window), the one
\*   wait a generation, the hold of a card whose holder never stamped (the default: wait
\*   only), and the return and redeal of one whose holder stamped and went silent.
\* Part "gate": the lander's base tree gate on one base commit: red or green each time
\*   it is gated; the third failure stops the stream.
\* Part "idle": the fleet idle or working each tick; the alarm once an episode after
\*   Window ticks, and the clear note when it recovers.
\*
\* Part "late" also has a reachability witness, MCSprintRulesLateReachReturn: the design
\* still returns a card (gen > 0), so NeverReturned fails there.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, each broken
\* by one property:
\*   "norepoint"  replace drops and adds without re-pointing: TwinsInherit
\*   "silent"     replace drops raising no blocked judgment, without re-pointing:
\*                NoDanglingNeed (a waiting card needs a dropped card and no judgment
\*                says so)
\*   "nocap"      the failed rule redeals on the same tier for ever: RuleAttemptsBounded
\*   "down"       the bound rule may lower the tier: LadderClimbs
\*   "waitalways" the late rule waits whenever there was progress: WaitOnce
\*   "returnunstamped" the late rule returns a late card with no progress, stamped or not
\*                (the rule before the stamp existed): NeverStampedNeverReturned
\*   "stopfirst"  the base gate stops the stream on its first failure: BaseStopsOnThird
\*   "everytick"  the idle alarm is pushed every tick of an episode: AlarmOncePerEpisode

EXTENDS Integers, FiniteSets

CONSTANTS Part, Broken, Cap, MaxFails, MaxGen, Window, MaxClock

None == "none"

\* -- twins
TwinIds == {"o", "t", "d1", "d2"}
Waiters == {"d1", "d2"}
Cols == {"absent", "waiting", "open", "landed", "dropped"}

\* -- every part's variables
VARIABLES col, need, blocked, twin,               \* twins
          tier, fails, st, attempts, envFails,     \* rules
          gen, waited, progress, late, waits, lst, stamped, badReturn, \* late
          gfails, stopped,                         \* gate
          idle, since, said, alarms, clk, nalarm, nclear \* idle

twinVars == <<col, need, blocked, twin>>
ruleVars == <<tier, fails, st, attempts, envFails>>
lateVars == <<gen, waited, progress, late, waits, lst, stamped, badReturn>>
gateVars == <<gfails, stopped>>
idleVars == <<idle, since, said, alarms, clk, nalarm, nclear>>
vars == <<twinVars, ruleVars, lateVars, gateVars, idleVars>>

TypeOK ==
  /\ col \in [TwinIds -> Cols]
  /\ need \in [TwinIds -> SUBSET TwinIds]
  /\ blocked \in [TwinIds -> SUBSET TwinIds]
  /\ twin \in [TwinIds -> TwinIds \cup {None}]
  /\ tier \in 1..4
  /\ fails \in 0..Cap
  /\ st \in {"working", "failed", "bound", "done", "judged"}
  /\ attempts \in Nat
  /\ envFails \in 0..MaxFails
  /\ gen \in 0..MaxGen
  /\ waited \in (0..MaxGen) \cup {-1}
  /\ progress \in BOOLEAN
  /\ late \in BOOLEAN
  /\ waits \in 0..2
  /\ lst \in {"working", "withdrawn"}
  /\ stamped \in BOOLEAN
  /\ badReturn \in BOOLEAN
  /\ gfails \in 0..3
  /\ stopped \in BOOLEAN
  /\ idle \in BOOLEAN
  /\ since \in (0..MaxClock) \cup {-1}
  /\ said \in BOOLEAN
  /\ alarms \in 0..MaxClock
  /\ clk \in 0..MaxClock

Init ==
  /\ col = [x \in TwinIds |-> CASE x = "o" -> "open" [] x = "t" -> "absent" [] OTHER -> "waiting"]
  /\ need = [x \in TwinIds |-> IF x \in Waiters THEN {"o"} ELSE {}]
  /\ blocked = [x \in TwinIds |-> {}]
  /\ twin = [x \in TwinIds |-> None]
  /\ tier = 1 /\ fails = 0 /\ st = "working" /\ attempts = 0 /\ envFails = 0
  /\ gen = 0 /\ waited = -1 /\ progress = FALSE /\ late = FALSE /\ waits = 0 /\ lst = "working"
  /\ stamped = FALSE /\ badReturn = FALSE
  /\ gfails = 0 /\ stopped = FALSE
  /\ idle = FALSE /\ since = -1 /\ said = FALSE /\ alarms = 0 /\ clk = 0 /\ nalarm = 0 /\ nclear = 0

-----------------------------------------------------------------------------
\* Part "twins".

\* The waiting cards that need x and would be re-pointed.
Dependents(x) == {d \in Waiters : col[d] = "waiting" /\ x \in need[d]}

\* drop x: off the table; every waiting card that needs it is told (Drop's blocked
\* judgment, one per card, naming the dropped need)
DropOld ==
  /\ col["o"] \in {"open", "waiting"}
  /\ col' = [col EXCEPT !["o"] = "dropped"]
  /\ blocked' = [d \in TwinIds |-> IF d \in Dependents("o") THEN blocked[d] \cup {"o"} ELSE blocked[d]]
  /\ UNCHANGED <<need, twin>>

AddTwin ==
  /\ col["t"] = "absent"
  /\ col' = [col EXCEPT !["t"] = "open"]
  /\ UNCHANGED <<need, blocked, twin>>

\* every dependent of o needs t in o's place, and its judgments that named o are answered
Repoint(n) ==
  /\ need' = [d \in TwinIds |-> IF d \in Dependents("o") THEN (need[d] \ {"o"}) \cup {n} ELSE need[d]]
  /\ blocked' = [d \in TwinIds |-> IF d \in Dependents("o") THEN blocked[d] \ {"o"} ELSE blocked[d]]

\* add --replaces o: one step
Replace ==
  /\ col["t"] = "absent"
  /\ col["o"] \in {"open", "waiting", "dropped"}
  /\ twin' = [twin EXCEPT !["o"] = "t"]
  /\ IF Broken = "norepoint"
       THEN \* the drop and the add, the edges left on o: the drop's judgments raised
            /\ col' = [col EXCEPT !["t"] = "open", !["o"] = "dropped"]
            /\ blocked' = [d \in TwinIds |-> IF d \in Dependents("o") /\ col["o"] # "dropped" THEN blocked[d] \cup {"o"} ELSE blocked[d]]
            /\ UNCHANGED need
     ELSE IF Broken = "silent"
       THEN \* the drop told no one, and nothing was re-pointed
            /\ col' = [col EXCEPT !["t"] = "open", !["o"] = "dropped"]
            /\ UNCHANGED <<need, blocked>>
     ELSE /\ col' = [col EXCEPT !["t"] = "open", !["o"] = "dropped"]
          /\ Repoint("t")

\* relink o t: the repair of a drop and an add made apart
Relink ==
  /\ col["o"] = "dropped"
  /\ col["t"] \in {"open", "landed"}
  /\ Dependents("o") # {}
  /\ twin' = [twin EXCEPT !["o"] = "t"]
  /\ Repoint("t")
  /\ UNCHANGED col

LandTwin(x) ==
  /\ x \in {"o", "t"}
  /\ col[x] = "open"
  /\ col' = [col EXCEPT ![x] = "landed"]
  /\ UNCHANGED <<need, blocked, twin>>

Resolve(d) ==
  /\ d \in Waiters
  /\ col[d] = "waiting"
  /\ \A x \in need[d] : col[x] = "landed"
  /\ col' = [col EXCEPT ![d] = "open"]
  /\ UNCHANGED <<need, blocked, twin>>

TwinNext ==
  \/ DropOld \/ AddTwin \/ Replace \/ Relink
  \/ \E x \in TwinIds : LandTwin(x) \/ Resolve(x)

\* No waiting card needs an id that has a twin: the twin took over every edge.
TwinsInherit ==
  \A x \in TwinIds : twin[x] # None => \A d \in Waiters : col[d] = "waiting" => x \notin need[d]

\* No blocked judgment names a replaced card.
NoBlockedForReplaced ==
  \A x \in TwinIds : twin[x] # None => \A d \in TwinIds : x \notin blocked[d]

\* A waiting card that needs a dropped card has a judgment that says so: nothing waits
\* in silence on a card that will never land.
NoDanglingNeed ==
  \A d \in Waiters : col[d] = "waiting" => \A x \in need[d] : col[x] = "dropped" => x \in blocked[d]

-----------------------------------------------------------------------------
\* Part "rules": one card's attempts.

\* the outside: an attempt comes back failed, reaches its bound, or lands
WorkFails ==
  /\ st = "working" /\ envFails < MaxFails
  /\ st' = "failed" /\ envFails' = envFails + 1
  /\ UNCHANGED <<tier, fails, attempts>>
WorkBounds ==
  /\ st = "working" /\ envFails < MaxFails
  /\ st' = "bound" /\ envFails' = envFails + 1
  /\ UNCHANGED <<tier, fails, attempts>>
WorkLands ==
  /\ st = "working"
  /\ st' = "done"
  /\ UNCHANGED <<tier, fails, attempts, envFails>>

\* the failed rule: the next route on the tier, the Cap-th failure a tier up
RuleFailed ==
  /\ st = "failed" /\ tier <= 3
  /\ IF fails + 1 < Cap \/ Broken = "nocap"
       THEN /\ fails' = IF Broken = "nocap" THEN fails ELSE fails + 1
            /\ UNCHANGED tier
       ELSE /\ tier' = tier + 1 /\ fails' = 0
  /\ st' = "working" /\ attempts' = attempts + 1
  /\ UNCHANGED envFails

\* the bound rule: a tier up (heavy's next is a friend's card)
RuleBound ==
  /\ st = "bound" /\ tier <= 3
  /\ tier' = IF Broken = "down" /\ tier > 1 THEN tier - 1 ELSE tier + 1
  /\ fails' = 0 /\ st' = "working" /\ attempts' = attempts + 1
  /\ UNCHANGED envFails

\* a friend's card is answered by a mind, never by rule
Mind ==
  /\ st \in {"failed", "bound"} /\ tier = 4
  /\ st' = "judged"
  /\ UNCHANGED <<tier, fails, attempts, envFails>>

RuleNext == WorkFails \/ WorkBounds \/ WorkLands \/ RuleFailed \/ RuleBound \/ Mind

\* The rules make at most Cap attempts a tier on three tiers: they never loop.
RuleAttemptsBounded == attempts <= 3 * Cap

\* A rule never lowers a card's tier.
LadderClimbs == [][tier' >= tier]_ruleVars

\* Every failure or bound is answered: by rule below a friend's card, by a mind there.
Answered == (st \in {"failed", "bound"}) ~> (st \notin {"failed", "bound"})

-----------------------------------------------------------------------------
\* Part "late": one work card past its deadline (internal/sprint rules.go, ruleLate).

GoLate ==
  /\ lst = "working" /\ ~late
  /\ late' = TRUE
  /\ UNCHANGED <<gen, waited, progress, waits, lst, stamped, badReturn>>

\* the holder's `progress` verb (the member while its child prints, the friend daemon while a
\* lane's turn prints): a fresh stamp, at the server's time, on the card it works
Stamp ==
  /\ lst = "working"
  /\ stamped' = TRUE /\ progress' = TRUE
  /\ UNCHANGED <<gen, waited, late, waits, lst, badReturn>>

\* the holder goes silent: its last stamp ages past RuleProgressWindow
Silence ==
  /\ progress
  /\ progress' = FALSE
  /\ UNCHANGED <<gen, waited, late, waits, lst, stamped, badReturn>>

\* the late rule: a wait once a generation with progress; a card whose holder stamped and went
\* silent, or whose one wait is spent, returned and redealt; a card whose holder never stamped
\* this take held (the default, wait only: the judgment closed, raised again later)
RuleLate ==
  /\ late
  /\ IF progress /\ (waited # gen \/ Broken = "waitalways")
       THEN /\ waited' = gen /\ waits' = waits + 1 /\ late' = FALSE
            /\ UNCHANGED <<gen, lst, progress, stamped, badReturn>>
     ELSE IF stamped \/ waited = gen \/ Broken = "returnunstamped"
       THEN /\ gen < MaxGen
            /\ gen' = gen + 1 /\ waits' = 0 /\ late' = FALSE /\ lst' = "withdrawn"
            /\ badReturn' = (badReturn \/ ~stamped)
            /\ stamped' = FALSE /\ progress' = FALSE \* the next take's holder has stamped nothing
            /\ UNCHANGED waited
     ELSE /\ late' = FALSE
          /\ UNCHANGED <<gen, waited, progress, waits, lst, stamped, badReturn>>

Redeal ==
  /\ lst = "withdrawn"
  /\ lst' = "working"
  /\ UNCHANGED <<gen, waited, progress, late, waits, stamped, badReturn>>

LateNext == (GoLate \/ Stamp \/ Silence \/ RuleLate \/ Redeal) /\ waits <= 1

\* One wait a generation, whatever the progress.
WaitOnce == waits <= 1

\* A card whose holder never stamped is never returned by the late rule.
NeverStampedNeverReturned == ~badReturn

\* Progress is a stamp of this take's holder.
ProgressIsStamped == progress => stamped

\* The reachability witness's invariant: no card is ever returned. The design returns a card
\* whose holder stamped and went silent, so TLC's counterexample is the proof it is reached.
NeverReturned == gen = 0

-----------------------------------------------------------------------------
\* Part "gate": one base commit gated again and again.

GateRed ==
  /\ ~stopped
  /\ gfails' = gfails + 1
  /\ stopped' = (gfails + 1 >= 3 \/ Broken = "stopfirst")
GateGreen ==
  /\ ~stopped /\ gfails < 3
  /\ gfails' = 0
  /\ UNCHANGED stopped

GateNext == GateRed \/ GateGreen

\* The stream stops only on the base's third failure in a row.
BaseStopsOnThird == stopped => gfails >= 3

-----------------------------------------------------------------------------
\* Part "idle": the alarm once an episode, the clear once it recovers.

Flip ==
  /\ idle' = ~idle
  /\ UNCHANGED <<since, said, alarms, clk, nalarm, nclear>>

IdleTick ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ IF idle
       THEN IF since = -1
              THEN /\ since' = clk /\ UNCHANGED <<said, alarms, nalarm, nclear>>
            ELSE IF clk - since >= Window /\ (~said \/ Broken = "everytick")
              THEN /\ said' = TRUE /\ alarms' = alarms + 1 /\ nalarm' = nalarm + 1
                   /\ UNCHANGED <<since, nclear>>
            ELSE UNCHANGED <<since, said, alarms, nalarm, nclear>>
       ELSE IF since # -1
              THEN /\ since' = -1 /\ said' = FALSE /\ alarms' = 0
                   /\ nclear' = IF said THEN nclear + 1 ELSE nclear
                   /\ UNCHANGED nalarm
            ELSE UNCHANGED <<since, said, alarms, nalarm, nclear>>
  /\ UNCHANGED idle

IdleNext == Flip \/ IdleTick

\* One alarm an episode.
AlarmOncePerEpisode == alarms <= 1

\* A clear note follows an alarm: never more clears than alarms, never a clear for an
\* episode that raised none.
ClearFollowsAlarm == nclear <= nalarm

-----------------------------------------------------------------------------

Next ==
  \/ Part = "twins" /\ TwinNext /\ UNCHANGED <<ruleVars, lateVars, gateVars, idleVars>>
  \/ Part = "rules" /\ RuleNext /\ UNCHANGED <<twinVars, lateVars, gateVars, idleVars>>
  \/ Part = "late" /\ LateNext /\ UNCHANGED <<twinVars, ruleVars, gateVars, idleVars>>
  \/ Part = "gate" /\ GateNext /\ UNCHANGED <<twinVars, ruleVars, lateVars, idleVars>>
  \/ Part = "idle" /\ IdleNext /\ UNCHANGED <<twinVars, ruleVars, lateVars, gateVars>>

\* The rules and a mind act when they may; the outside is unfair.
Fairness ==
  /\ WF_vars(Part = "rules" /\ (RuleFailed \/ RuleBound \/ Mind) /\ UNCHANGED <<twinVars, lateVars, gateVars, idleVars>>)

Spec == Init /\ [][Next]_vars /\ Fairness

=============================================================================
