RESULT tools22-pre-2071-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2071 at head 299fb0ef23ca182e58092d31dd75fa9284a51b80: Parked: specify measured fleet state without changing work ownership
PREREAD 2071 claims=18 proven=0 unproven=18 defects=0 high=0

PR 2071
HEAD 299fb0ef23ca182e58092d31dd75fa9284a51b80
BASE dev
MERGE-BASE a3abdd4ad6dd0a0427f131ad6b71a9e10f07308e
BEHIND 3
FILES 1 production, 0 test
LINES +112 -0

1. `nova-pulse fleet state|probe|hold|release` owns the fleet's measured state and administrative holds, separate from `fleet pause|stop|run|status` which owns the control signal — UNPROVEN (new spec, no code)
2. Reachability values are exactly `UNKNOWN`, `UP`, or `DOWN`; `HOLD` is an independent administrative bit, not a fourth reachability state — UNPROVEN
3. Required-service health is separate from reachability: ssh may prove a bench `UP` while a missing required tool makes `service=unhealthy` and prevents admission — UNPROVEN
4. The append-only event journal is authoritative; events carry id, bench, expected/new revision, cohort digest, observation instant, reachability, service observation with closed reason tokens, consecutive success/failure counts, observed build, and HOLD change — UNPROVEN
5. Writers append and sync the event before publishing an atomic replay-derived projection; probes run outside the writer lock and commit only if cohort digest and expected revision still match — UNPROVEN
6. Same event id with same content is idempotent; same id with different content refuses — UNPROVEN
7. A torn or malformed journal tail is not silently discarded; `fleet state` may replay valid prefix but prints cohort as `UNKNOWN` with `journal=torn`; mutation, `is-ready`, and admission refuse until recovery — UNPROVEN
8. A stale or partial projection is never authority; readers replay the journal, and a later successful writer may republish the projection — UNPROVEN
9. Command signatures: `fleet state` takes `--machines`, `--events`, optional `--state`, `--slots`, `--adoption`, `--bench`, `--stale-after`, `--now`, `--max`; `fleet probe` adds `--ssh`, `--timeout`, `--down-after`, `--up-after`, `--max-parallel`; `fleet hold` adds `--reason`; `fleet release` takes `--bench`; `fleet is-up` takes `--stale-after`; `fleet is-ready` adds `--control`, `--slots`, `--adoption` — UNPROVEN
10. `fleet state` is read-only: does not probe, update age, publish projection, change HOLD or control signal, move cards, or change slot/job lease — UNPROVEN
11. `--now` is available only to the display path and fake-clock tests; live mutation and admission use real clock with no caller-supplied time — UNPROVEN
12. `fleet state` prints one bounded row per selected registry bench in registry order, then one summary line with columns: FLEET row (`state`, `service`, `hold`, `observed`, `age`, `reason`, `build_observed`, `installed`, `ready`, `in_use`, `capacity`, `reserve`, `held`, `free`, `eligible`, `cohort`) and `FLEET STATE` summary (`cohort`, `selected`, `excluded`, `up`, `unknown`, `down`, `held`, `eligible`, `journal`) — UNPROVEN
13. Slot capacity/reserve/held/free come from swarm slot store; absent or unreadable store is `-`, never zero — UNPROVEN
14. `in_use` is active worker build/process epoch, `mixed` or `unknown`; may be `-` on idle bench; adoption complete is not itself admission; eligibility is derived from fresh UP, healthy services, no HOLD, RUN control, required adoption, launch-path evidence, readable positive free capacity, and all ordinary rules — UNPROVEN
15. `fleet is-up` exits 0 for fresh raw UP, 1 for UNKNOWN/DOWN, 2 for corrupt input; `fleet state` exits 0 after complete valid report (even DOWN/HELD rows), 2 on invalid input including torn-journal UNKNOWN rows; `fleet probe` exits 3 when any probe fails/times out/exceeds bound, 2 for invalid input — UNPROVEN
16. `fleet hold` changes only HOLD; `fleet release` releases HOLD only — does not release slot/lease, move work, set UP, or start loop — UNPROVEN
17. Release makes reachability UNKNOWN and requires a new successful probe before eligibility; becoming UP never restarts a script or bypasses RUN/PAUSE/STOP or ordinary admission — UNPROVEN
18. DOWN/stale has no ownership meaning; launched card on such bench is UNKNOWN/STRANDED and neither work nor leases released or retried; lifecycle fence insufficient alone — lifecycle owner must also prove compute terminal or prove card was never admitted before release/retry — UNPROVEN

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The spec says `fleet release` makes reachability UNKNOWN. Is there a concern that making reachability UNKNOWN on release could confuse operators who expect the prior reachability state to be preserved until re-probed? Could a separate mechanism reset reachability instead?
2. The spec references SPEC-AHEAD: #2046 for the measured fleet state section. What is the planned scope of #2046 — does it cover all verbs listed here (state/probe/hold/release/is-up/is-ready), or only some subset?
3. The acceptance criteria mention that `fleet hold` and `fleet release` interact with event journal writes under writer lock. How does this coordinate with the existing `fleet suspend`/`fleet wake`/`fleet reboot` paths in `internal/pulse/fleet.go`, which currently have no knowledge of this journal or HOLDS?
4. The spec defines exit code 3 for `fleet probe` when any probe fails. Why exit 3 rather than exiting non-zero per-bench with a summary? Is there a consumer parsing pattern that depends on this specific exit code?
5. The claim that `"an absent or unreadable store is \-, never zero"` — is there a guard in the existing slot store interface that enforces this, or will this require new sentinel handling in the store layer?

Left owed — The diff changes only one file (docs/SPEC-PULSE.md), which I read fully. No production Go code was changed by this PR, so I did not read fleet_test.go or other pulse internals beyond scanning for naming conflicts with the new verb names. The three commits behind on dev were not reviewed individually.

git status --short
(nothing — working tree clean)
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
