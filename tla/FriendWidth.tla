---------------------------- MODULE FriendWidth ----------------------------
\* The friend-width and friend-delivery checks (docs/SPEC-SPRINT.md, section
\* friend-width-and-delivery; internal/sprint/friend_width.go, friend_delivery.go; the
\* owner, 2026-10-07: "make sure the friends are working to their width and actually
\* delivering work over time ... it should become machinery").
\*
\* One friend, as the tick sees her: up or not, running lanes under a configured width,
\* a queue of cards dealt to her and not started. The outside moves her (Start, Finish,
\* Deal, GoDown, ComeUp); the tick (Tick) runs the two checks on what it reads. Time is
\* ages that stop at their bound, as FriendPresence does, so the clock never ends and the
\* liveness is checked.
\*
\* The friend-width check: while she is up with free lanes beside queued cards (Cond) the
\* idle age runs; at Window it raises the judgment once and the rule pings her session
\* (pinged); it raises again every Window while Cond holds (sinceRaise); it keeps the most
\* lanes she ran (maxRan) for the note from the third raise on; the clock, the raises and
\* the ping are cleared when Cond breaks. The friend-delivery check: while she is up with
\* a lane busy (CondD) the silence since her last delivery, or her oldest take, runs
\* (silence); at Window it raises once and the rule pings her; at two Windows the rule
\* marks her down (down), which takes her out of up; the raises, the ping and the mark are
\* cleared when CondD breaks. The width is a constant of the configuration: the machine
\* reads it and never writes it (width = Width).
\*
\* Broken = "none" is the design. Every other value is a reversed witness, each caught by
\* one property below:
\*   "raiseearly"      the judgment is raised as soon as Cond holds: RaisedOnlyAfterWindow
\*   "noraise"         the judgment is never raised: RaisedWhenWindowHeld
\*   "setswidth"       the third raise writes her observed concurrency as her width:
\*                     WidthNeverChangedByMachine
\*   "downfirstwindow" the rule marks her down on the first window: DownOnlyAfterTwoWindows
\*   "noping"          the raise pings nobody: PingedWithinWindow (liveness)

EXTENDS Naturals

CONSTANTS Width, MaxQueue, Window, Broken

VARIABLES up, running, queue, width,
          idle, raised, sinceRaise, pinged, maxRan,
          silence, raisedD, sinceRaiseD, pingedD, down, downSilence

vars == <<up, running, queue, width, idle, raised, sinceRaise, pinged, maxRan,
          silence, raisedD, sinceRaiseD, pingedD, down, downSilence>>
widthVars == <<idle, raised, sinceRaise, pinged, maxRan>>
deliveryVars == <<silence, raisedD, sinceRaiseD, pingedD, down, downSilence>>

Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b

\* The raises are counted to three: the note from the third raise on says her observed
\* concurrency, and nothing past three changes the design.
MaxRaises == 3

TypeOK ==
  /\ up \in BOOLEAN
  /\ running \in 0..Width
  /\ queue \in 0..MaxQueue
  /\ width \in 0..Width
  /\ idle \in 0..Window
  /\ raised \in 0..MaxRaises
  /\ sinceRaise \in 0..Window
  /\ pinged \in BOOLEAN
  /\ maxRan \in 0..Width
  /\ silence \in 0..(2 * Window)
  /\ raisedD \in 0..MaxRaises
  /\ sinceRaiseD \in 0..Window
  /\ pingedD \in BOOLEAN
  /\ down \in BOOLEAN
  /\ downSilence \in 0..(2 * Window)

Init ==
  /\ up = TRUE /\ running = 0 /\ queue = 0 /\ width = Width
  /\ idle = 0 /\ raised = 0 /\ sinceRaise = 0 /\ pinged = FALSE /\ maxRan = 0
  /\ silence = 0 /\ raisedD = 0 /\ sinceRaiseD = 0 /\ pingedD = FALSE /\ down = FALSE /\ downSilence = 0

\* Free lanes beside queued cards, on a friend up (TickFriendWidth: holds).
Cond == up /\ running < width /\ queue > 0

\* A lane busy, on a friend up (TickFriendDelivery: working > 0).
CondD == up /\ running > 0

\* ---------------------------------------------------------------- the tick

\* The friend-width check on one tick: the idle age runs while Cond holds and is cleared
\* when it breaks; the judgment is raised at Window and again every Window; the rule's
\* ping goes with the first raise; the most lanes she ran is kept for the note.
WidthTick ==
  IF Cond THEN
    LET idle1 == Min(idle + 1, Window)
        since1 == Min(sinceRaise + 1, Window)
        due == CASE Broken = "raiseearly" -> TRUE
                 [] Broken = "noraise" -> FALSE
                 [] OTHER -> idle1 >= Window /\ (raised = 0 \/ since1 >= Window)
        raised1 == IF due THEN Min(raised + 1, MaxRaises) ELSE raised
        maxRan1 == Max(maxRan, running)
    IN /\ idle' = idle1
       /\ raised' = raised1
       /\ sinceRaise' = IF due THEN 0 ELSE since1
       /\ pinged' = pinged \/ (due /\ Broken # "noping")
       /\ maxRan' = maxRan1
       /\ width' = IF Broken = "setswidth" /\ due /\ raised1 = MaxRaises THEN maxRan1 ELSE width
  ELSE
    /\ idle' = 0 /\ raised' = 0 /\ sinceRaise' = 0 /\ pinged' = FALSE /\ maxRan' = 0
    /\ UNCHANGED width

\* The friend-delivery check on one tick: the silence runs while a lane is busy (it is
\* the facts, kept whether she is up or not); the judgment is raised at Window and again
\* every Window while CondD holds, the rule's ping with the first raise, and at two
\* Windows the rule marks her down, which takes her out of up (her unstarted cards go
\* back by the take-back path, not modelled: the queue is hers until the deal moves it).
DeliveryTick ==
  LET silence1 == IF running > 0 THEN Min(silence + 1, 2 * Window) ELSE 0
  IN IF CondD THEN
       LET since1 == Min(sinceRaiseD + 1, Window)
           due == silence1 >= Window /\ (raisedD = 0 \/ since1 >= Window)
           downNow == ~down /\ (IF Broken = "downfirstwindow" THEN silence1 >= Window ELSE silence1 >= 2 * Window)
       IN /\ silence' = silence1
          /\ raisedD' = IF due THEN Min(raisedD + 1, MaxRaises) ELSE raisedD
          /\ sinceRaiseD' = IF due THEN 0 ELSE since1
          /\ pingedD' = pingedD \/ due
          /\ down' = down \/ downNow
          /\ downSilence' = IF downNow THEN silence1 ELSE downSilence
          /\ up' = IF downNow THEN FALSE ELSE up
     ELSE
       /\ silence' = silence1
       /\ raisedD' = 0 /\ sinceRaiseD' = 0 /\ pingedD' = FALSE /\ down' = FALSE /\ downSilence' = 0
       /\ UNCHANGED up

Tick ==
  /\ WidthTick
  /\ DeliveryTick
  /\ UNCHANGED <<running, queue>>

\* ---------------------------------------------------------------- the outside

\* She starts a queued card in a free lane: it is working on her row. Her first start
\* after an empty row is the oldest take the delivery check counts from.
Start ==
  /\ up /\ running < width /\ queue > 0
  /\ running' = running + 1 /\ queue' = queue - 1
  /\ silence' = IF running = 0 THEN 0 ELSE silence
  /\ UNCHANGED <<up, width, idle, raised, sinceRaise, pinged, maxRan, raisedD, sinceRaiseD, pingedD, down, downSilence>>

\* A report of hers arrives (ok, failed or HOLD): a delivery.
Finish ==
  /\ running > 0
  /\ running' = running - 1 /\ silence' = 0
  /\ UNCHANGED <<up, queue, width, idle, raised, sinceRaise, pinged, maxRan, raisedD, sinceRaiseD, pingedD, down, downSilence>>

\* The deal places a card ready on her row.
Deal ==
  /\ queue < MaxQueue
  /\ queue' = queue + 1
  /\ UNCHANGED <<up, running, width, widthVars, deliveryVars>>

\* Her session goes silent, or the coordinator holds her: not up.
GoDown ==
  /\ up
  /\ up' = FALSE
  /\ UNCHANGED <<running, queue, width, widthVars, deliveryVars>>

\* Her session's next proof brings her back, as a down friend comes back today.
ComeUp ==
  /\ ~up
  /\ up' = TRUE
  /\ UNCHANGED <<running, queue, width, widthVars, deliveryVars>>

Next == Tick \/ Start \/ Finish \/ Deal \/ GoDown \/ ComeUp

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* ---------------------------------------------------------------- the rules

\* A judgment is raised only once the condition has held a whole window.
RaisedOnlyAfterWindow == raised > 0 => idle >= Window

\* A judgment is raised whenever the condition has held a whole window.
RaisedWhenWindowHeld == idle >= Window => raised > 0

\* The width is never changed by the machine: one width per friend, set by people.
WidthNeverChangedByMachine == width = Width

\* A friend goes down by delivery only after two windows of silence.
DownOnlyAfterTwoWindows == down => downSilence >= 2 * Window

\* A friend with free lanes and a queue is pinged within one window, unless the
\* condition breaks first (under fair ticks: the window runs out).
PingedWithinWindow == Cond ~> (pinged \/ ~Cond)

=============================================================================
