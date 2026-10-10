------------------------------ MODULE LatePush ------------------------------
\* The push loop's late-tick push (cmd/nova-sprint/pushlate.go, lateWatch.step and
\* pushLate). The tick that runs late cannot tell of itself, so the push loop reads the
\* machine's line at every look and pushes the seat while the tick runs late. Cold readers A
\* and B, PR 5548: the first cut ended its episode, and forgot its last push, on any look that
\* found the tick on time; the machine line counts lateness from the last heartbeat, so a look
\* just after each tick reads on time, and with ticks 60 s apart the seat was pushed sixty
\* times an hour. And the watch lived only in the loop's memory, so a restarted loop pushed at
\* once.
\*
\*   late    the machine line, as the next look reads it: an outside event (Flip), late or on
\*           time at any moment.
\*   clk     running time, one step a look (sprint.PassEvery is Every steps).
\*   last    the watch's memory of its last push (lateWatch.last); -1 while none.
\*   pushed  the clock of the last push made (a ghost: what the seat received); -1 while none.
\*   prev    the clock of the push before it (a ghost); -1 while none.
\*   since   the clock the episode began at; -1 while none.
\*   onTime  the clock the run of on-time looks began at, inside an episode; -1 while none.
\*   lateLook the last look found the tick late.
\*
\* The design, Broken = "none": a late look begins or continues the episode and pushes when
\* no push was made or the last is Every old; an on-time look ends the episode only after Every
\* of on-time looks, and never forgets the last push; a restart (Restart) loses the episode and
\* reads the last push back from the inbox it was written to (lastTickLate).
\* Reversed witnesses, each broken by one property:
\*   "wipe"    an on-time look ends the episode and forgets the last push (the first cut):
\*             OnePushAWindow.
\*   "forget"  a restart forgets the last push (the watch in memory alone): OnePushAWindow.
EXTENDS Integers

CONSTANTS Every, MaxClock, Broken

VARIABLES late, clk, last, pushed, prev, since, onTime, lateLook
vars == <<late, clk, last, pushed, prev, since, onTime, lateLook>>

Clock == (0..MaxClock) \cup {-1}

TypeOK ==
  /\ late \in BOOLEAN /\ lateLook \in BOOLEAN
  /\ clk \in 0..MaxClock
  /\ last \in Clock /\ pushed \in Clock /\ prev \in Clock /\ since \in Clock /\ onTime \in Clock

Init ==
  /\ late = FALSE /\ clk = 0 /\ last = -1 /\ pushed = -1 /\ prev = -1 /\ since = -1 /\ onTime = -1
  /\ lateLook = FALSE

\* The machine line between two looks: the tick runs late, or a tick was just made.
Flip ==
  /\ late' = ~late
  /\ UNCHANGED <<clk, last, pushed, prev, since, onTime, lateLook>>

\* A push is due: none was made, or the last is a whole window old.
Due(t) == last = -1 \/ t - last >= Every

\* One look of the push loop (lateWatch.step).
Look ==
  /\ clk < MaxClock
  /\ clk' = clk + 1
  /\ lateLook' = late
  /\ UNCHANGED late
  /\ IF late
       THEN /\ onTime' = -1
            /\ since' = IF since = -1 THEN clk + 1 ELSE since
            /\ IF Due(clk + 1)
                 THEN prev' = pushed /\ pushed' = clk + 1 /\ last' = clk + 1
                 ELSE UNCHANGED <<prev, pushed, last>>
       ELSE IF Broken = "wipe"
              THEN since' = -1 /\ onTime' = -1 /\ last' = -1 /\ UNCHANGED <<prev, pushed>>
            ELSE IF since = -1
              THEN UNCHANGED <<since, onTime, last, pushed, prev>>
            ELSE LET o == IF onTime = -1 THEN clk + 1 ELSE onTime
                 IN /\ IF clk + 1 - o >= Every
                         THEN since' = -1 /\ onTime' = -1
                         ELSE since' = since /\ onTime' = o
                    /\ UNCHANGED <<last, pushed, prev>>

\* The push loop restarts: the episode is lost; the last push is read back from the inbox's
\* TICKLATE files, unless the watch lives in memory alone ("forget").
Restart ==
  /\ since' = -1 /\ onTime' = -1 /\ lateLook' = FALSE
  /\ last' = IF Broken = "forget" THEN -1 ELSE last
  /\ UNCHANGED <<late, clk, pushed, prev>>

Next == Flip \/ Look \/ Restart

Spec == Init /\ [][Next]_vars

\* At most one late push a window: two pushes are a whole window apart.
OnePushAWindow == (prev # -1 /\ pushed # -1) => pushed - prev >= Every

\* Pushed while late: after a look that found the tick late, a push was made within the
\* window (the throttle never silences a late tick for longer than a window).
PushedWhileLate == lateLook => (pushed # -1 /\ clk - pushed < Every)

=============================================================================
