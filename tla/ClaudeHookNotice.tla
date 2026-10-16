---------------------------- MODULE ClaudeHookNotice ----------------------------
\* The Claude Code route of nova-hook (cmd/nova-hook/claude.go,
\* pkg/harness/notice/claude.go): how a heartbeat challenge reaches the model
\* of one Claude Code session, busy or idle, and what the session's own hooks
\* may do about it. Only the model proves; no hook, watcher or script does.
\*
\* nonce    the newest challenge the heartbeat issued (0: none yet)
\* pend     the challenge the pending notice file holds (0: none). The Core
\*          rewrites the file in place when a newer one arrives (Fold).
\* shown    the newest challenge put in front of the model: a PUSH line read by
\*          the watcher (the session's Monitor), the PostToolUse hook, the Stop
\*          hook's block, or SessionStart's recovery text
\* surfaced the challenge the PostToolUse hook last surfaced (once per nonce)
\* proved   the newest challenge the model proved (nova-hook prove records it
\*          in the claude state directory, which the hooks read)
\* blocked  the challenges the Stop hook has blocked a stop for (once each)
\* nagged   the Stop hook has blocked once for a missing watcher since the
\*          watcher was last armed
\* gen      the generation file: one per turn boundary of the session
\* seen     the generation when the pending notice was pushed
\* turn     "busy" or "idle"
\* watch    a watcher (the session's Monitor running nova-hook's --claude-watch)
\*          holds the watch lock
\*
\* Broken names the reversed witness each failing configuration runs:
\*   "noblock"       the Stop hook never blocks for an unproved challenge
\*   "blockproved"   the Stop hook blocks for a challenge already proved
\*   "childadvances" a headless claude child's Stop advances the generation
\*   "nowatchnag"    the Stop hook lets the session idle with no watcher
EXTENDS Naturals, FiniteSets
CONSTANTS MaxN, MaxGen, Broken
VARIABLES nonce, pend, shown, surfaced, proved, blocked, nagged, gen, seen, turn, watch

vars == <<nonce, pend, shown, surfaced, proved, blocked, nagged, gen, seen, turn, watch>>

TypeOK ==
    /\ nonce \in 0..MaxN /\ pend \in 0..MaxN /\ shown \in 0..MaxN
    /\ surfaced \in 0..MaxN /\ proved \in 0..MaxN
    /\ blocked \subseteq 1..MaxN /\ nagged \in BOOLEAN
    /\ gen \in 0..MaxGen /\ seen \in 0..MaxGen
    /\ turn \in {"busy", "idle"} /\ watch \in BOOLEAN

Init ==
    /\ nonce = 0 /\ pend = 0 /\ shown = 0 /\ surfaced = 0 /\ proved = 0
    /\ blocked = {} /\ nagged = FALSE
    /\ gen = 0 /\ seen = 0
    /\ turn = "busy" /\ watch = FALSE

Unproved(n) == n # 0 /\ n > proved

\* A push reaches the model only through an armed watcher; it wakes an idle
\* session (the Monitor's notification).
Told(n) ==
    IF watch THEN /\ shown' = n /\ turn' = "busy"
             ELSE UNCHANGED <<shown, turn>>

\* The heartbeat issues a challenge. With no notice pending the Core saves and
\* pushes one; with one pending it rewrites that file in place (rule 1).
Issue ==
    /\ nonce < MaxN
    /\ nonce' = nonce + 1
    /\ pend' = nonce + 1
    /\ IF pend = 0
         THEN /\ seen' = gen
              /\ Told(nonce + 1)
         ELSE UNCHANGED <<seen, shown, turn>>
    /\ UNCHANGED <<surfaced, proved, blocked, nagged, gen, watch>>

\* Rule 3a: the pending notice now holds a newer challenge than it showed, so
\* the Core pushes it again (same file).
Repush ==
    /\ Unproved(pend) /\ pend # shown
    /\ Told(pend)
    /\ UNCHANGED <<nonce, pend, surfaced, proved, blocked, nagged, gen, seen, watch>>

\* The Core observes consumption: a proof (the model's act on the bus), or a
\* turn boundary since the push. The notice ends (Clear removes the pending
\* name) and the challenge it showed goes with it (notice.go settle); a newer
\* challenge folded in behind it is held, and the next Step pushes it in a
\* new notice.
Settle ==
    /\ pend # 0
    /\ \/ proved >= pend
       \/ gen > seen
    /\ IF proved >= pend \/ pend = shown
         THEN /\ pend' = 0
              /\ UNCHANGED <<seen, shown, turn>>
         ELSE /\ seen' = gen
              /\ Told(pend)
              /\ UNCHANGED pend
    /\ UNCHANGED <<nonce, surfaced, proved, blocked, nagged, gen, watch>>

\* PostToolUse, in a busy turn: an unproved pending challenge not yet surfaced
\* goes into the turn as additional context, once.
Tool ==
    /\ turn = "busy"
    /\ Unproved(pend) /\ surfaced # pend
    /\ surfaced' = pend
    /\ shown' = pend
    /\ UNCHANGED <<nonce, pend, proved, blocked, nagged, gen, seen, turn, watch>>

\* The model proves the newest challenge it was shown.
Prove ==
    /\ turn = "busy"
    /\ shown > proved
    /\ proved' = shown
    /\ UNCHANGED <<nonce, pend, shown, surfaced, blocked, nagged, gen, seen, turn, watch>>

\* The model arms the watcher (its Monitor) in a busy turn.
Arm ==
    /\ turn = "busy" /\ ~watch
    /\ watch' = TRUE
    /\ nagged' = FALSE
    /\ UNCHANGED <<nonce, pend, shown, surfaced, proved, blocked, gen, seen, turn>>

\* The Monitor expires (or dies). Its expiry is itself a notification, so an
\* idle session wakes to re-arm it.
Expire ==
    /\ watch
    /\ watch' = FALSE
    /\ turn' = "busy"
    /\ UNCHANGED <<nonce, pend, shown, surfaced, proved, blocked, nagged, gen, seen>>

BlockFor(n) ==
    /\ Broken # "noblock"
    /\ n \notin blocked
    /\ (Unproved(n) \/ Broken = "blockproved")

\* The session's Stop hook. It blocks the stop once for a pending challenge
\* not proved, naming it; then once for a missing watcher; else the turn ends
\* and the generation advances.
SessionStop ==
    /\ turn = "busy"
    /\ gen < MaxGen
    /\ IF pend # 0 /\ BlockFor(pend)
         THEN /\ blocked' = blocked \cup {pend}
              /\ shown' = pend
              /\ UNCHANGED <<nagged, gen, turn>>
       ELSE IF ~watch /\ ~nagged /\ Broken # "nowatchnag"
         THEN /\ nagged' = TRUE
              /\ UNCHANGED <<blocked, shown, gen, turn>>
       ELSE /\ gen' = gen + 1
            /\ turn' = "idle"
            /\ UNCHANGED <<blocked, shown, nagged>>
    /\ UNCHANGED <<nonce, pend, surfaced, proved, seen, watch>>

\* A headless claude child (a lane's card, a read, the harness proof) whose
\* config directory holds the session's Stop hook ends a turn of its own.
ChildStop ==
    /\ gen < MaxGen
    /\ IF Broken = "childadvances" THEN gen' = gen + 1 ELSE UNCHANGED gen
    /\ UNCHANGED <<nonce, pend, shown, surfaced, proved, blocked, nagged, seen, turn, watch>>

\* SessionStart (a restart, resume or compaction): monitors died with the old
\* process; the hook puts the pending notice and the arm line in front of the
\* model.
Start ==
    /\ watch' = FALSE
    /\ turn' = "busy"
    /\ shown' = IF pend # 0 THEN pend ELSE shown
    /\ nagged' = FALSE
    /\ UNCHANGED <<nonce, pend, surfaced, proved, blocked, gen, seen>>

Next == Issue \/ Repush \/ Settle \/ Tool \/ Prove \/ Arm \/ Expire \/ SessionStop \/ ChildStop \/ Start

Spec == Init /\ [][Next]_vars

\* The session never goes idle with an unproved pending challenge the model
\* was not shown, while its watcher is armed. noblock violates it: a challenge
\* folded behind a shown one is never put in front of the model before idle.
IdleKnows ==
    [][ (turn = "busy" /\ turn' = "idle" /\ watch' /\ Unproved(pend')) => shown' = pend' ]_vars

\* The session idles with no watcher only after one block that told it so.
\* nowatchnag violates it.
IdleArmed ==
    turn = "idle" => (watch \/ nagged)

\* A child's end is no turn of the session's. childadvances violates it.
ChildNoBoundary ==
    [][ ChildStop => gen' = gen ]_vars

\* The Stop hook blocks only for a challenge the model has not proved, and
\* once per challenge. blockproved violates it.
BlockOnlyUnproved ==
    [][ (blocked' # blocked) => \A n \in blocked' \ blocked : n > proved ]_vars

=============================================================================
