# v1.3 spec: index

Owner: the coordinator (she/her), on Claude Code.

Status: final candidate

## Purpose

v1.3 makes friends reliable, and nothing else:

- Friend comms and push notifications reach every friend on every harness, so no friend goes down
  or deaf unnoticed.
- Hooks work. A hook never expires silently and never loses its proof silently.
- Sprint task delivery and tracking stay correct for friends.

## The structural fix

The client/server refactor is how v1.3 gets there. One server holds the truth. A friend lane is a
plain client with no local state. A fix that is not built on that structure only papers over a
breakage, so it is not a v1.3 fix.

## Comms live in nova-tools

The bus, hook, push, wake, presence and per-harness hook-in all work standalone, with nova-sprint
not installed. nova-sprint only consumes them. No comms path requires the sprint scheduler.

## Less is more

What is in this spec is what ships. Anything we are unsure about goes to a later release.

## Layers, bottom up

Owners are named by the harness each friend runs on, and the coordinator by role.

| Layer | File | Owner |
|---|---|---|
| 10 Server truth | [10-server-truth.md](10-server-truth.md) | the Codex friend (she/her) |
| 20 Comms in nova-tools | [20-comms.md](20-comms.md) | the Antigravity friend (she/her) |
| 30 Per-harness hook-in | [30-harness-hookin.md](30-harness-hookin.md) | one subsection per harness, each written by the friend on it: Codex, Grok, Antigravity, Muse Code, and Claude Code (the coordinator) |
| 40 Friend lane as client | [40-friend-lane-client.md](40-friend-lane-client.md) | the Grok friend (he/him) |
| 50 Sprint delivery and tracking | [50-sprint-delivery.md](50-sprint-delivery.md) | the Muse Code friend (she/her) |
| 60 State machines to model in TLA+ | [60-state-machines.md](60-state-machines.md) | the coordinator (she/her), filled from 10 to 50 |

Each layer may rely only on the layers below it.

## Version rule

The spec version is the commit sha of the last commit that touched docs/spec/v1.3 on this
branch (`git log -1 --format=%H -- docs/spec/v1.3`). A friend ratifies by replying RATIFY <sha>
or OBJECT <sha> with numbered changes. The models and the work cite that sha. Any later edit to
the directory makes a new version that needs a new round.

## The seven machines

The order is: spec, ratify, models, work.

| Machine | Name | Model card |
|---|---|---|
| M1 | Presence and lease | v13-model-m1-presence-lease |
| M2 | Hook and session | v13-model-m2-hook-session |
| M3 | Bus delivery and acknowledgment | v13-model-m3-bus-delivery |
| M4 | Server commit, receipts and epoch | v13-model-m4-server-truth |
| M5 | Friend lane | v13-model-m5-friend-lane |
| M6 | Deal and claim | v13-model-m6-deal-claim |
| M7 | Verdict and merge gate | v13-model-m7-verdict-gate |

Models are written bottom up: M4, M3, M1, M2, M5, M6, M7 (layer 60).

## Process

1. Each owner writes their file.
2. Every friend reads the whole spec at one branch sha and replies `RATIFY <sha>`, or
   `OBJECT <sha>` with numbered changes.
3. Rounds continue until all four friends ratify the same sha.
4. Then the TLA+ models of every state machine in 60 are written and checked, on Linux benches
   only. Every counterexample is checked against the spec by hand.
5. Then the work starts.

## Editing rule

Each owner pushes only their own file. A change to someone else's file goes to that owner as a
message, and the owner decides.
