# accept-self-feeding — nova-sprint v1.0.0 acceptance, check 4 of 6

Verdict: FAIL

PASS line (verbatim): no judgment older than 15 minutes, the idle alarm fires and clears correctly, friend ready queues stay at 2x width.

Window start (UTC): 2026-10-04T23:35:04Z (the `date -u` of the first sample).
Window end (UTC): 2026-10-04T23:39:08Z (the `date -u` after the fifth sample). Five samples, 60 seconds apart. The card's stated 1-hour window was not run: the prescribed nova-sprint reads cannot be executed inside this wall (below), and the card directs that a window whose measurement cannot be run is reported rather than stretched. The dashboard read was available and is the evidence for the ready-queue criterion.

Build measured: the live system's dashboard `build` field `19c41193` (sprint epoch 15), the same on the local mirror and the remote pull at every sample. The installed `nova-sprint` binary could not be executed (EACCES), so its own `--version` was not read; the checkout is at `bf078ca7d54a07958e51bdb39b9e693fc1010a66` (`sprint/mechanical-2026-10-02`). Reporter: Rowan; harness opencode; model `openrouter/deepseek/deepseek-v4.1-flash`.

## Commands run

Each prescribed sprint verb was attempted as `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`. The binary never starts, so the environment and the timeout did not matter. A read that fails is a counted failure, never a skip. All probes are read-only; nothing was started, stopped or killed.

1. `date -u` at first sample: `Sun Oct  4 11:35:04 PM UTC 2026`.
2. `nova-sprint --version` -> exit 126, `timeout: failed to execute process: Permission denied (os error 13)`. Same for `nova-sprint help`.
3. `nova-sprint where --json --cards` -> exit 126, Permission denied. Same for `nova-sprint stats --json` and `nova-sprint log --json --since 2026-10-04T23:35:04Z`.
4. `nova-bus log --max 0` -> exit 126, Permission denied; `nova-bus2` is not on PATH (the rename has not reached this install).
5. `curl -s http://127.0.0.1:7390/api/sprint` (the card's dashboard command, no Host) -> HTTP 200, body 0 bytes, at every sample.
6. `curl -s -H 'Host: 100.115.99.19' http://127.0.0.1:7390/api/sprint` -> HTTP 200, `application/json`, 81288–81814 bytes, the live `where --json --cards` copy (Caddy serves `:7390` for the machine's tailnet host and rewrites `/api/sprint` to the pulled copy).
7. `curl -s http://100.76.29.55:7390/api/sprint` -> HTTP 200, `application/json`, the same live copy (the pull the local mirror fetches).
8. `timeout 5 bash -c 'cat < /dev/null > /dev/tcp/127.0.0.1/6390'` -> `Connection refused`; `ss -tln` shows no listener on 6390.
9. `curl -s -X POST --data '{"verbs":[["where","--json","--cards"]]}' http://100.76.29.55:6390/verbs` -> result code 2, `nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed`. That address is the worker server, not the coordinator loopback the card names.

## Criterion 1 — Judgments (no open judgment older than 15 minutes at any sample)

Measured value: not obtained. The dashboard copy of `where --json --cards` carried no `judgments` field at any sample (`absent`), and no `cards` field either. That view lists only judgments naming a *dealt* card's primary (`dealtView` filters by the dealt cards), so an open judgment on a not-dealt card is invisible to it; it therefore cannot confirm or refute the bar. `nova-sprint where --json --cards` itself could not run (command 3).
Raw counts: samples taken 5, samples passing 0 (unmeasured). Worst value: unmeasurable — no oldest-open-judgment age was returned.
Bar: every sample yields an oldest-open-judgment age under 15 minutes.
Decision: unmeasured; not the deciding criterion.

## Criterion 2 — Idle alarm (raises true, clears after the condition ends; at least one raise and clear)

Measured value: not obtained. `nova-sprint log` could not run (command 3) and the bus log could not run (command 4), so no raise or clear was observable. The dashboard `low` field (the ready buffer under width, the self-feeding alarm) was `true` at every sample and never cleared in the window.
Raw counts: samples taken 5, samples with an observable alarm event 0, raises observed 0, clears observed 0.
Bar: every raise true when raised, every clear after the condition ends, at least one raise and clear observed.
Decision: HOLD per the card's clause — none occurred that could be observed, and none was provoked.

## Criterion 3 — Friend ready queues (each friend not held holds ready at 2x its width at every sample)

Measured value: FAIL. Read from the dashboard friends table at every sample: every friend's ready count is `0`, against a 2x-width bar of `16` for a width-8 friend and `32` for the width-16 friend. The global ready buffer was `40/136` with `low: true` at every sample (ready 40, 2*width 136).
Raw counts: samples taken 5, samples passing 0, samples below 2x 5. Worst value: ready `0` for every non-held friend at all five samples (emma, freddy, johnny, rowan-next, rowan-personal, rowan-space, stella, zhi), first at 2026-10-04T23:35:04Z, last at 2026-10-04T23:39:07Z.
Bar: every non-held friend holds ready at 2x width at every sample, except within 2 minutes after a deal.
Decision: FAIL — every sample is below 2x.

## Raw sample lines that decide each criterion

Criterion 3 (all five samples fail; 0 passing). Dashboard line and the non-held friends' ready/width, one sample each:

    --- SAMPLE 2026-10-04T23:35:04Z ---
    {"build":"19c41193","fetchedAt":"2026-10-04T23:35:02.978930+00:00","at":"2026-10-04T19:35:02.740964-04:00","epoch":15,"ready":40,"width":68,"buffer":"40/136","low":true,"stalled":84,"cleared":"2026-10-02T02:14:51.267336Z"}
    emma ready=0 width=8 up; freddy ready=0 width=8 up; johnny ready=0 width=8 up; rowan-next ready=0 width=8 down; rowan-personal ready=0 width=8 down; rowan-space ready=0 width=8 down; stella ready=0 width=8 up; zhi ready=0 width=8 up

    --- SAMPLE 2026-10-04T23:36:05Z ---
    {"build":"19c41193","fetchedAt":"2026-10-04T23:36:03.965613+00:00","at":"2026-10-04T19:36:03.772848-04:00","epoch":15,"ready":40,"width":68,"buffer":"40/136","low":true,"stalled":84,"cleared":"2026-10-02T02:14:51.267336Z"}
    same friends, every ready=0

    --- SAMPLE 2026-10-04T23:37:06Z ---
    {"build":"19c41193","fetchedAt":"2026-10-04T23:37:05.001386+00:00","at":"2026-10-04T19:37:04.821005-04:00","epoch":15,"ready":40,"width":68,"buffer":"40/136","low":true,"stalled":84,"cleared":"2026-10-02T02:14:51.267336Z"}
    same friends, every ready=0

    --- SAMPLE 2026-10-04T23:38:07Z ---
    {"build":"19c41193","fetchedAt":"2026-10-04T23:38:05.290424+00:00","at":"2026-10-04T19:38:05.054325-04:00","epoch":15,"ready":40,"width":68,"buffer":"40/136","low":true,"stalled":85,"cleared":"2026-10-02T02:14:51.267336Z"}
    same friends, every ready=0

    --- SAMPLE 2026-10-04T23:39:07Z ---
    {"build":"19c41193","fetchedAt":"2026-10-04T23:39:06.122732+00:00","at":"2026-10-04T19:39:05.906023-04:00","epoch":15,"ready":40,"width":68,"buffer":"40/136","low":true,"stalled":86,"cleared":"2026-10-02T02:14:51.267336Z"}
    same friends, every ready=0

Criterion 1 (all five samples: the field is absent; 0 passing, 0 failing because unmeasured):

    judgments field: "absent"
    cards field: "absent"

Criterion 2 (all five samples: no alarm event observable; the deciding failing line is the failed read, in full):

    --- nova-sprint log --json --since 2026-10-04T23:35:04Z (installed, NOVA_SPRINT_SERVER=127.0.0.1:6390) ---
    timeout: failed to execute process: Permission denied (os error 13)
    exit=126

The prescribed path failing at every sample (the line that decides that the measurement could not be run):

    --- nova-sprint where --json --cards (installed, NOVA_SPRINT_SERVER=127.0.0.1:6390) ---
    timeout: failed to execute process: Permission denied (os error 13)
    exit=126

## What was not measured

- The oldest open judgment's age: `nova-sprint where --json --cards` could not run, and the dashboard's copy of that view omits judgments on not-dealt cards, so criterion 1 is unmeasured.
- The idle alarm's raise/clear events: `nova-sprint log` and the bus log could not run; no raise or clear was observable, and none was provoked (criterion 2, HOLD).
- The full 1-hour window: the prescribed reads fail in this wall, so a 5-sample, ~4-minute window was taken and not stretched; the dashboard read was available and is the evidence.
- `nova-sprint stats --json`: could not run.
- The installed nova-sprint build's own version line: the binary cannot be executed here (EACCES).
- The live store directly: `100.76.29.55:6379` is refused; the local `127.0.0.1:6379` holds only a foreign-version quack fixture.
