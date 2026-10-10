----------------------------- MODULE SprintRules -----------------------------
\* The machine feeds itself (the coordinator, for the owner, 2026-10-04 at 1:36 PM: "I want this
\* sort of oh no fleet is idle, do judgement, release more cards thing -- i want this
\* more automated."). Six small state machines of nova-sprint, each its own Part, each
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
\* Part "reads": one card's attempts under a reader's broken reads (rules.go, ruleReadBroken):
\*   the rule reworks it with the finding as the fix, on the same tier; a finding that names
\*   a file outside PATHS widens the card's PATHS in place by that file (the same card, never
\*   a twin), its attempts on the widened brief from the first, at the brief's bound too (a
\*   widened brief is the bound's remedy), at most MaxWidens times a card, past which the
\*   brief is wrong and the card a mind's; a card at its brief's bound (the same finding
\*   twice, or Cap attempts on one brief) on any other finding is a mind's, never the rule's.
\* Part "gate": the lander's base tree gate on one base commit: red or green each time
\*   it is gated; the third failure stops the stream.
\* Part "idle": the fleet idle or working each tick; the alarm once an episode after
\*   Window ticks, and the clear note when it recovers.
\* Part "answers": one card's attempts under the three answers that took the seat's hand
\*   loops (rules_read.go ruleReadBroken, harness_fault.go ruleHarness, steps_work.go
\*   lateFinish): a broken read with a finding reworks the card with it, a broken read with
\*   none is a mind's; work failed on a harness fault or a HOLD with findings is reworked with
\*   the failure, any other failure is a mind's; a deadline fails an attempt while the
\*   worker's report is still on its way, and that report, arriving while no later attempt
\*   has started, finishes the attempt. Every rework is one attempt more and starts from the
\*   head the attempt before pushed (a head here is its attempt's number).
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
\*   "nobound"    the read-broken rule reworks a card at its brief's bound: ReadAnswersBounded
\*   "widenall"   the read-broken rule widens a card whose finding names no file outside
\*                PATHS: WidensWiden
\*   "boundoutside" the read stops a finding naming a file outside PATHS at the brief's
\*                bound instead of letting the rule widen it: OutsideNeverBound
\*   "nowidencap" the rule widens on every finding outside PATHS, past MaxWidens:
\*                WidensBounded
\*   "nofinding"  the read-broken rule reworks a broken read that carries no finding:
\*                ReworkHasAFix
\*   "recount"    a rework starts its attempt without counting it: ReworkKeepsCount
\*   "nocarry"    a rework starts from the base, not the head the attempt before pushed:
\*                ReworkCarriesHead
\*   "stale"      finish refuses the report of an attempt the deadline failed, and the card
\*                goes round again: LateReportFinishes
\*   "faultnobound" the harness rule reworks a card at its attempt bound: AnswerAttemptsBounded

EXTENDS Integers, FiniteSets

CONSTANTS Part, Broken, Cap, MaxFails, MaxGen, Window, MaxClock

None == "none"

\* -- twins
TwinIds == {"o", "t", "d1", "d2"}
Waiters == {"d1", "d2"}
Cols == {"absent", "waiting", "open", "landed", "dropped"}

\* -- reads: the files a finding may name outside the card's first PATHS, and findings
\* inside them (or naming no file)
RdFiles == {"f1", "f2", "f3"}

\* -- reads: the most times readers' findings widen one card's PATHS in place (the code's
\* MaxReadWidens; 2 here, so a third file outside PATHS reaches the cap)
MaxWidens == 2
RdFindings == RdFiles \cup {"in1", "in2"}

\* -- every part's variables
VARIABLES col, need, blocked, twin,               \* twins
          tier, fails, st, attempts, envFails,     \* rules
          gen, waited, progress, late, waits, lst, stamped, badReturn, \* late
          rdst, rdatt, rdlast, rdfind, rdpaths, rdwidens, rdtotal, \* reads
          gfails, stopped,                         \* gate
          idle, since, said, alarms, clk, nalarm, nclear, \* idle
          anst, anatt, anreworks, anhead, ancarry, anfind, anfail, anlast, anland, anlost, anfixless \* answers

twinVars == <<col, need, blocked, twin>>
ruleVars == <<tier, fails, st, attempts, envFails>>
lateVars == <<gen, waited, progress, late, waits, lst, stamped, badReturn>>
rdVars == <<rdst, rdatt, rdlast, rdfind, rdpaths, rdwidens, rdtotal>>
gateVars == <<gfails, stopped>>
idleVars == <<idle, since, said, alarms, clk, nalarm, nclear>>
anVars == <<anst, anatt, anreworks, anhead, ancarry, anfind, anfail, anlast, anland, anlost, anfixless>>
vars == <<twinVars, ruleVars, lateVars, rdVars, gateVars, idleVars, anVars>>

\* -- answers: a reader's findings ("empty" is a broken verdict that carries none), and the
\* ways an attempt fails
AnFindings == {"f1", "f2", "empty"}
AnFails == {"harness", "hold", "other"}

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
  /\ rdst \in {"working", "broken", "bound", "done", "judged"}
  /\ rdatt \in Nat
  /\ rdlast \in RdFindings \cup {None}
  /\ rdfind \in RdFindings \cup {None}
  /\ rdpaths \subseteq RdFiles
  /\ rdwidens \in Nat
  /\ rdtotal \in Nat
  /\ gfails \in 0..3
  /\ stopped \in BOOLEAN
  /\ idle \in BOOLEAN
  /\ since \in (0..MaxClock) \cup {-1}
  /\ said \in BOOLEAN
  /\ alarms \in 0..MaxClock
  /\ clk \in 0..MaxClock
  /\ anst \in {"working", "broken", "failed", "bound", "done", "judged"}
  /\ anatt \in Nat /\ anreworks \in Nat /\ anhead \in Nat /\ ancarry \in Nat
  /\ anfind \in AnFindings \cup {None}
  /\ anfail \in AnFails \cup {None}
  /\ anlast \in AnFindings \cup {None}
  /\ anland \in BOOLEAN /\ anlost \in BOOLEAN /\ anfixless \in BOOLEAN

Init ==
  /\ col = [x \in TwinIds |-> CASE x = "o" -> "open" [] x = "t" -> "absent" [] OTHER -> "waiting"]
  /\ need = [x \in TwinIds |-> IF x \in Waiters THEN {"o"} ELSE {}]
  /\ blocked = [x \in TwinIds |-> {}]
  /\ twin = [x \in TwinIds |-> None]
  /\ tier = 1 /\ fails = 0 /\ st = "working" /\ attempts = 0 /\ envFails = 0
  /\ gen = 0 /\ waited = -1 /\ progress = FALSE /\ late = FALSE /\ waits = 0 /\ lst = "working"
  /\ stamped = FALSE /\ badReturn = FALSE
  /\ rdst = "working" /\ rdatt = 1 /\ rdlast = None /\ rdfind = None /\ rdpaths = {}
  /\ rdwidens = 0 /\ rdtotal = 0
  /\ gfails = 0 /\ stopped = FALSE
  /\ idle = FALSE /\ since = -1 /\ said = FALSE /\ alarms = 0 /\ clk = 0 /\ nalarm = 0 /\ nclear = 0
  /\ anst = "working" /\ anatt = 1 /\ anreworks = 0 /\ anhead = 0 /\ ancarry = 0
  /\ anfind = None /\ anfail = None /\ anlast = None
  /\ anland = FALSE /\ anlost = FALSE /\ anfixless = FALSE

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
\* Part "reads": one card's attempts under a reader's broken reads (internal/sprint rules.go,
\* ruleReadBroken; brief_bound.go, AtBriefBound; steps_review.go, Read).

\* the finding names a file outside the card's PATHS
RdOutside(f) == f \in RdFiles /\ f \notin rdpaths

\* the card's brief is at its bound for finding f: the same finding as the attempt before, or
\* Cap attempts on one brief
RdAtBound(f) == f = rdlast \/ rdatt >= Cap

\* the finding names a file outside PATHS and the card's widenings are under the cap: the rule
\* may widen it in place (a widening is never reset by the brief it writes)
RdWidenable(f) == RdOutside(f) /\ (rdwidens < MaxWidens \/ Broken = "nowidencap")

\* the outside: a reader finds the attempt broken with finding f (Read writes the brief's
\* bound judgment instead at the bound, unless f names a file outside PATHS under the widen
\* cap; and for a file outside PATHS past the cap), or the attempt lands
RdReadBroken(f) ==
  /\ rdst = "working"
  /\ rdfind' = f
  /\ rdst' = IF Broken # "nobound"
                  /\ \/ (RdAtBound(f) \/ RdOutside(f)) /\ ~RdWidenable(f)
                     \/ RdAtBound(f) /\ Broken = "boundoutside"
               THEN "bound" ELSE "broken"
  /\ UNCHANGED <<rdatt, rdlast, rdpaths, rdwidens, rdtotal>>
RdLands ==
  /\ rdst = "working"
  /\ rdst' = "done"
  /\ UNCHANGED <<rdatt, rdlast, rdfind, rdpaths, rdwidens, rdtotal>>

\* the rule: a finding naming a file outside PATHS widens the card's PATHS in place by it,
\* attempts on the widened brief from the first; any other finding is the fix of a rework on
\* the same tier
RuleReadBroken ==
  /\ rdst = "broken"
  /\ IF RdWidenable(rdfind) \/ Broken = "widenall"
       THEN /\ rdpaths' = rdpaths \cup ({rdfind} \cap RdFiles)
            /\ rdatt' = 1 /\ rdlast' = None /\ rdwidens' = rdwidens + 1
       ELSE /\ rdatt' = rdatt + 1 /\ rdlast' = rdfind
            /\ UNCHANGED <<rdpaths, rdwidens>>
  /\ rdst' = "working" /\ rdtotal' = rdtotal + 1
  /\ UNCHANGED rdfind

\* a card at its brief's bound is answered by a mind (brief or drop), never by rule
RdMind ==
  /\ rdst = "bound"
  /\ rdst' = "judged"
  /\ UNCHANGED <<rdatt, rdlast, rdfind, rdpaths, rdwidens, rdtotal>>

RdNext == RdLands \/ RuleReadBroken \/ RdMind \/ \E f \in RdFindings : RdReadBroken(f)

\* The rule's answers never loop: under Cap attempts a brief, and a widening only for a file
\* outside PATHS.
ReadAnswersBounded == rdtotal <= Cap * (Cardinality(RdFiles) + 1)

\* Every widening widens PATHS by a file.
WidensWiden == rdwidens <= Cardinality(rdpaths)

\* A finding naming a file outside PATHS is never stopped at the brief's bound while the
\* card's widenings are under the cap: the rule widens the brief in place, the bound's own
\* remedy.
OutsideNeverBound == rdst = "bound" /\ RdOutside(rdfind) => rdwidens >= MaxWidens

\* Readers' findings widen one card's PATHS at most MaxWidens times: a reader naming a new
\* file outside PATHS every round never widens it for ever.
WidensBounded == rdwidens <= MaxWidens

\* Every broken read is answered: by rule below the bound, by a mind at it.
ReadAnswered == (rdst \in {"broken", "bound"}) ~> (rdst \notin {"broken", "bound"})

-----------------------------------------------------------------------------
\* Part "answers": one card's attempts under the broken-read, harness-fault and late-report
\* answers (internal/sprint rules_read.go, harness_fault.go, steps_work.go lateFinish).

\* the card's brief is at its bound for finding f: the same finding as the attempt before, or
\* Cap attempts on one brief
AnAtBound(f) == (f # "empty" /\ f = anlast) \/ anatt >= Cap

\* the outside: a reader finds the attempt broken with finding f (at the bound the read raises
\* the brief's judgment instead); the attempt pushed its head
AnReadBroken(f) ==
  /\ anst = "working"
  /\ anhead' = anatt /\ anfind' = f
  /\ anst' = IF f # "empty" /\ AnAtBound(f) THEN "bound" ELSE "broken"
  /\ UNCHANGED <<anatt, anreworks, ancarry, anfail, anlast, anland, anlost, anfixless>>

\* the outside: the attempt comes back failed the way k (at the attempt cap the finish raises
\* the brief's judgment instead)
AnWorkFails(k) ==
  /\ anst = "working"
  /\ anhead' = anatt /\ anfail' = k
  /\ anst' = IF anatt >= Cap THEN "bound" ELSE "failed"
  /\ UNCHANGED <<anatt, anreworks, ancarry, anfind, anlast, anland, anlost, anfixless>>

\* the outside: the deadline fails the attempt (a harness fault) while the worker's LAND is
\* still on its way
AnDeadline ==
  /\ anst = "working" /\ ~anland
  /\ anhead' = anatt /\ anfail' = "harness" /\ anland' = TRUE
  /\ anst' = IF anatt >= Cap THEN "bound" ELSE "failed"
  /\ UNCHANGED <<anatt, anreworks, ancarry, anfind, anlast, anlost, anfixless>>

\* the outside: the worker's late LAND arrives. While its attempt is failed and no later
\* attempt has started, finish takes it: the attempt is done. Once a later attempt has
\* started it is refused, the old attempt staying failed.
AnLateLand ==
  /\ anland
  /\ anland' = FALSE
  /\ IF anst \in {"failed", "bound"}
       THEN IF Broken = "stale"
              THEN /\ anlost' = TRUE
                   /\ UNCHANGED <<anst, anfail>>
              ELSE /\ anst' = "done" /\ anfail' = None
                   /\ UNCHANGED anlost
       ELSE UNCHANGED <<anst, anfail, anlost>>
  /\ UNCHANGED <<anatt, anreworks, anhead, ancarry, anfind, anlast, anfixless>>

\* a rework with fix f ("empty": no fix): one attempt more, from the head the attempt before
\* pushed
AnRework(f) ==
  /\ anreworks < MaxFails
  /\ anatt' = IF Broken = "recount" THEN anatt ELSE anatt + 1
  /\ anreworks' = anreworks + 1
  /\ ancarry' = IF Broken = "nocarry" THEN 0 ELSE anhead
  /\ anfixless' = (anfixless \/ f = "empty")
  /\ anst' = "working" /\ anfind' = None /\ anfail' = None
  /\ UNCHANGED <<anhead, anland, anlost>>

\* the read-broken rule: a broken read with a finding reworks the card with it
RuleAnBroken ==
  /\ anst = "broken"
  /\ anfind # "empty" \/ Broken = "nofinding"
  /\ AnRework(anfind)
  /\ anlast' = anfind

\* the failed rule: a harness fault, or a HOLD with findings, reworks the card with the
\* failure as its fix
RuleAnHarness ==
  /\ \/ anst = "failed"
     \/ Broken = "faultnobound" /\ anst = "bound"
  /\ anfail \in {"harness", "hold"}
  /\ AnRework(anfail)
  /\ UNCHANGED anlast

\* a mind: a broken read with no finding, a failure no class names, a card at its bound
AnMind ==
  /\ \/ anst = "broken" /\ anfind = "empty"
     \/ anst = "failed" /\ anfail = "other"
     \/ anst = "bound"
  /\ anst' = "judged"
  /\ UNCHANGED <<anatt, anreworks, anhead, ancarry, anfind, anfail, anlast, anland, anlost, anfixless>>

AnNext ==
  \/ AnDeadline \/ AnLateLand \/ RuleAnBroken \/ RuleAnHarness \/ AnMind
  \/ \E f \in AnFindings : AnReadBroken(f)
  \/ \E k \in AnFails : AnWorkFails(k)

\* The answers never pass the attempt bound.
AnswerAttemptsBounded == anatt <= Cap

\* A reworked card keeps its attempt count: every rework is one attempt more.
ReworkKeepsCount == anatt = anreworks + 1

\* A reworked card carries its head: its attempt starts from the head the one before pushed.
ReworkCarriesHead == anreworks > 0 => ancarry = anatt - 1

\* No rule reworks a card without a fix: a broken read with no finding is a mind's.
ReworkHasAFix == ~anfixless

\* A report that arrives for an attempt the deadline failed, before a later attempt started,
\* finishes it: it is never refused while it is the attempt's to finish.
LateReportFinishes == ~anlost

\* Every broken read, failure and bound is answered: by rule where a rule answers, by a mind
\* where none does.
AnAnswered == (anst \in {"broken", "failed", "bound"}) ~> (anst \notin {"broken", "failed", "bound"})

-----------------------------------------------------------------------------

Next ==
  \/ Part = "twins" /\ TwinNext /\ UNCHANGED <<ruleVars, lateVars, rdVars, gateVars, idleVars, anVars>>
  \/ Part = "rules" /\ RuleNext /\ UNCHANGED <<twinVars, lateVars, rdVars, gateVars, idleVars, anVars>>
  \/ Part = "late" /\ LateNext /\ UNCHANGED <<twinVars, ruleVars, rdVars, gateVars, idleVars, anVars>>
  \/ Part = "reads" /\ RdNext /\ UNCHANGED <<twinVars, ruleVars, lateVars, gateVars, idleVars, anVars>>
  \/ Part = "gate" /\ GateNext /\ UNCHANGED <<twinVars, ruleVars, lateVars, rdVars, idleVars, anVars>>
  \/ Part = "idle" /\ IdleNext /\ UNCHANGED <<twinVars, ruleVars, lateVars, rdVars, gateVars, anVars>>
  \/ Part = "answers" /\ AnNext /\ UNCHANGED <<twinVars, ruleVars, lateVars, rdVars, gateVars, idleVars>>

\* The rules and a mind act when they may; the outside is unfair.
Fairness ==
  /\ WF_vars(Part = "rules" /\ (RuleFailed \/ RuleBound \/ Mind) /\ UNCHANGED <<twinVars, lateVars, rdVars, gateVars, idleVars, anVars>>)
  /\ WF_vars(Part = "reads" /\ (RuleReadBroken \/ RdMind) /\ UNCHANGED <<twinVars, ruleVars, lateVars, gateVars, idleVars, anVars>>)
  /\ WF_vars(Part = "answers" /\ (RuleAnBroken \/ RuleAnHarness \/ AnMind) /\ UNCHANGED <<twinVars, ruleVars, lateVars, rdVars, gateVars, idleVars>>)

Spec == Init /\ [][Next]_vars /\ Fairness

=============================================================================
