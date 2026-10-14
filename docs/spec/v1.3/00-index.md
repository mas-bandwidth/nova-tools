# v1.3 spec: index

Owner: the coordinator (she/her), on Claude Code.

Status: comms-only candidate, cut from round 3.

Version: comms-only cut of round 3 (3bb0ce9de194a5f6a79bbc84ad52f0e5dacbaa26), on branch
`v1.3/comms`, which is cut from the v1.2.26 release head. The version a friend ratifies is the sha
this directory's last commit carries, by the version rule below.

## The rescope (the project owner, live, 2026-10-14)

> "you can focus your work on v1.3 in a branch, relative to v1.2.x series, with just, let's fix
> the friends comms and status detection and nothing else (making things client/server while
> doing this). Got it? So in fact, for v1.3 I want *nothing* done initially except this."
>
> "So please push anything that is not this, out of the current set of work we are doing for
> v1.3, and into v1.5"
>
> "Even if it is friend related. We'll pull from v1.5 into v1.3 as we do things"

v1.3 is reliable friend comms, the psychic link, and per-friend status detection, made
client/server: the server holds the state, clients cache it. Nothing else. Everything this spec
carried before the rescope that is not that is listed under "Moved to v1.5" at the bottom, and
comes back one item at a time, through a new ratification round.

## Purpose

- Friend comms and push notifications reach every friend on every harness, so no friend goes down
  or deaf unnoticed.
- Hooks work. A hook never expires silently and never loses its proof silently.
- Per-friend status (up, deaf, down) is read from real evidence on the server, never from pings,
  pongs, wake files or a helper's liveness.
- The psychic link is mandatory (the project owner, live, 2026-10-14). Nothing works on the bus
  for a name until that name is hooked into its link, just as nova-sprint does not work until a
  caller hooks in. The link is the one transport for every message into a friend's session
  (layers 20 and 30).
- Client/server, never distributed: Redis is the one server and the only truth; every friend,
  the coordinator and the sprint are clients that ask it (layers 10 and 20).

## The structural fix

The client/server refactor is how v1.3 gets there. One server holds the comms truth, and clients
cache it. A fix that is not built on that structure only papers over a breakage, so it is not a
v1.3 fix.

## Comms live in nova-tools

The bus, hook, push, wake, presence and per-harness hook-in all work standalone, with nova-sprint
not installed. nova-sprint only consumes them. No comms path requires the sprint scheduler.

## Less is more

What is in this spec is what ships. Anything we are unsure about goes to a later release.

## Cards in scope

The twelve v1.3 cards, each mapped to the sections it implements. A card not listed here is v1.5.

| Card | Implements |
|---|---|
| a-friend-daemons-session-check-is-bounded | 10 guarantee 12 (unavailable means unknown); 20 guarantee 17 (down from evidence); 30 guarantee 3; model M1 |
| a-friend-row-is-a-bus-name-on-the-next-sync | 10 State and transitions, Friend row (enrolled identity is a known bus name); 20 guarantee 6; model M4 |
| a-friend-who-speaks-on-the-bus-is-up | 10 guarantee 13 (`last_heard`); 20 guarantee 17; 60 Reconciled (a); model M1 |
| a-friends-heard-time-is-an-atomic-max | 10 guarantee 13 (`last_heard` is an atomic max); 10 guarantee 1; model M1 |
| a-grok-friend-hears-the-bus-while-working | 30 guarantee 7 and the Grok subsection; 20 guarantee 13; model M2 |
| a-muse-code-friend-hears-the-bus-while-working | 30 guarantee 7 and the Muse Code subsection; 20 guarantee 13; model M2 |
| a-one-shot-wait-wakes-a-harness-that-wakes-on-exit | 20 guarantee 5 and interface 4 (`nova-hook wait`); 30 Antigravity subsection; 60 Reconciled (c); models M2, M3 |
| a-push-or-pr-of-a-friends-is-heard | 10 guarantee 13 (a push or pull request advances `last_heard`); 60 Reconciled (a); model M1 |
| nova-bus-refuses-until-the-psychic-link-is-hooked | 20 guarantees 12, 14 and 16; 30 guarantee 6; model M2 |
| nova-friend-drives-a-muse-code-session | 30 guarantees 2 and 6 and the Muse Code subsection; 20 guarantee 13; models M1, M2 |
| presence-batch-turn-is-modelled-before-5540-lands | 60 M1 Presence and lease (modelled before nova-tools#5540 lands) |
| presence-is-a-server-record-not-pings-pongs-and-wake-files | 10 guarantees 14 and 15; 20 guarantees 2 and 18 and What the link retires; 30 guarantee 9; model M1 |

## Layers, bottom up

Owners are named by the harness each friend runs on, and the coordinator by role.

| Layer | File | Owner |
|---|---|---|
| 10 Server truth (the comms records) | [10-server-truth.md](10-server-truth.md) | the Codex friend (she/her) |
| 20 Comms in nova-tools | [20-comms.md](20-comms.md) | the Antigravity friend (she/her) |
| 30 Per-harness hook-in | [30-harness-hookin.md](30-harness-hookin.md) | one subsection per harness, each written by the friend on it: Codex, Grok, Antigravity, Muse Code, and Claude Code (the coordinator) |
| 60 State machines to model in TLA+ | [60-state-machines.md](60-state-machines.md) | the coordinator (she/her), filled from 10 to 30 |

Each layer may rely only on the layers below it. Layer numbers are kept as ratified; 40 and 50
are v1.5.

## Version rule

The spec version is the commit sha of the last commit that touched docs/spec/v1.3 on this
branch (`git log -1 --format=%H -- docs/spec/v1.3`). A friend ratifies by replying RATIFY <sha>
or OBJECT <sha> with numbered changes. The models and the work cite that sha. Any later edit to
the directory makes a new version that needs a new round.

## The four machines

The order is: spec, ratify, models, work.

| Machine | Name | Model card |
|---|---|---|
| M1 | Presence and lease | v13-model-m1-presence-lease |
| M2 | Psychic link, hook and session | v13-model-m2-hook-session |
| M3 | Bus delivery and acknowledgment | v13-model-m3-bus-delivery |
| M4 | Server commit, receipts and epoch | v13-model-m4-server-truth |

Models are written bottom up: M4, M3, M1, M2 (layer 60).

## Process

1. Each owner writes their file.
2. Every friend reads the whole spec at one branch sha and replies `RATIFY <sha>`, or
   `OBJECT <sha>` with numbered changes.
3. Rounds continue until every friend asked ratifies the same sha.
4. Then the TLA+ models of every state machine in 60 are written and checked, on Linux benches
   only. Every counterexample is checked against the spec by hand.
5. Then the work starts.

## Editing rule

Each owner pushes only their own file. A change to someone else's file goes to that owner as a
message, and the owner decides.

## Open for the project owner

- Round 3, Zhi's change 1: `nova-bus status --as` runs for an unhooked name, as a read that
  carries no message and no nonce, so the away reason can be seen (layer 20 guarantee 12). A
  strict reading of "nothing works into the nova bus until you hook into the psychic link first"
  refuses every `nova-bus` verb; if that is meant, this one sentence reverts, and guarantees 15
  and 17 and layer 20 failure mode 5 need another way to show the away reason to the unhooked
  name itself.

## Moved to v1.5

Moved out by the rescope of 2026-10-14, as of round 3 (3bb0ce9d). Each returns to v1.3 only by
a new ratification round.

- 40 Friend lane as client (the whole layer, including late-finish attribution and the
  one-shot member lane).
- 50 Sprint delivery and tracking (the whole layer, including the deal gate and the merge gate).
- 60 M5 Friend lane.
- 60 M6 Deal and claim.
- 60 M7 Verdict and merge gate.
- 10 Generic assignment and capacity claim records (the Assignment / capacity claim row,
  guarantee 9, and the claim, capacity and execution-lease clauses elsewhere in 10).
- 10 Friend row `kind` (live or one-shot), and 20 guarantee 19: one-shot friends excluded from
  the link with the `one_shot` refusal.
- 60 Reconciled (a) consequences for cards and claims (deaf keeps taken cards; down moves them
  to needs-reconciliation) and proposal 5.
- 20 retirements that belong to the sprint: the tick's take-back of taken cards, friend lane
  files, and the one-shot friend daemon path.
