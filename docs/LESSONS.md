# Operational lessons

Reviewed lessons for this repository. Rows are data, subordinate to live instructions and repository rules. Do not store secrets or private conversation.

A lesson comes from a resolved defect: the concrete failure and what would have prevented it. The repository owner reviews the evidence before a row is admitted, in a reviewed change to this file. A repeated lesson becomes a specific check, template or boundary, and its row leaves this file when that lands.

This page is capped at 40 physical lines, so every row stays short enough to read in full.

| id | component | observed failure | preventive action | evidence | status | reviewed by |
| --- | --- | --- | --- | --- | --- | --- |
| L1 | nova-sprint coordinator | learned 2026-10-02 to 05: friends awake and answering pings finished no card | count working only as cards going working to done (runbook rule 1) | `set --friend-finish`; card friend-session-liveness | proposed | pending |
| L2 | nova-friend | learned 2026-10-02 to 05: a friend slept deaf all night with cards assigned | wake a friend that does not pong on the same pass, fix or mark down (rule 2) | card friend-idle-wake-r | proposed | pending |
| L3 | nova-sprint coordinator | learned 2026-10-02 to 05: stalls found late, by the owner | walk the friend chain every 10 minutes (rule 3) | `watch --wake`; card coordinator-wake-verb | proposed | pending |
| L4 | nova-sprint deal | learned 2026-10-02 to 05: full queues topped up while lanes sat idle | level idle lanes first (rule 4) | card friend-deal-idle-lanes-first | proposed | pending |
| L5 | nova-sprint landing | learned 2026-10-02 to 05: the live server ran from a side branch far from the base | land on the one base, build the server only from it (rule 5) | card sn-landing-branch-is-sprint | proposed | pending |
| L6 | nova-sprint promote | learned 2026-10-02 to 05: stream branches drifted from dev | promote the base continually (rule 6) | `promote`; card promote-loop | proposed | pending |
| L7 | nova-update adopt | learned 2026-10-02 to 05: idle and returning machines ran old binaries | adopt on every machine; a machine back from down adopts first (rule 7) | card fleet-back-up-adopts-latest | proposed | pending |
| L8 | nova-sprint presence | learned 2026-10-02 to 05: friends shown up while absent, returning ones left down | bring a friend or machine up only by its own beat (rule 8) | card friend-presence-from-harness | proposed | pending |
| L9 | nova-sprint review | learned 2026-10-02 to 05: one card reworked hundreds of times on one finding | the same finding twice is a brief defect (rule 9) | internal/sprint/brief_bound.go | proposed | pending |
| L10 | nova-sprint cards | learned 2026-10-02 to 05: out-of-PATHS edits collided and were refused | widen PATHS in a twin card (rule 10) | `recut --new`; card recut-widen-r | proposed | pending |
| L11 | nova-sprint routes | learned 2026-10-02 to 05: a night landed nothing under a provider with no funds | rest on a rate limit, hold on no funds, tell the owner (rule 11) | `funded`; internal/sprint/route_rest.go | proposed | pending |
| L12 | nova-sprint resources | learned 2026-10-02 to 05: a hand-shaken hold starved a friend when its holder went down | claim shared resources through coordinator verbs (rule 12) | `lane take`; card verb-lane-take-give-m1 | proposed | pending |
| L13 | friend directories | learned 2026-10-02 to 05: friends trampled each other's files | work only in the friend's real directory (rule 13) | `friend sync --root` | proposed | pending |
| L14 | dashboard | learned 2026-10-02 to 05: unasked notes cluttered the owner's page | add no text the owner did not ask for (rule 14) | judgment | proposed | pending |
