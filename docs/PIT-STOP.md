# The pit stop — slow down to go fast

Glenn, 2026-09-11: a cascading worker failure is not fixed by more workers. A **pit stop** is
the coordinator's decision to stop widening the queue and spend the machine on the faults that
make every card slower, until one trust batch proves them gone. This page is the detector, the
trigger, the scope, the exit gate, and the record of the first one. The mechanisms live in
their specs and are only pointed to here.

## The detector

The `CONTRACTION` line's verdict ([SPEC-PULSE.md](SPEC-PULSE.md), **Status**): `EXPANDING`
when any stream's ratio — cards `cut/done`, PRs `opened/merged`, issues `filed/closed` — is
above 1 for two consecutive hours. `EXPANDING` says the spec work up front was not done
properly or the practice is lax, and the coordinator names which on the record before another
implementation card goes out (#549, #553). Two other readings point the same way and cost no
call: `PROGRESS effective_parallelism` far below the width (the benches idle while the queue is
deep), and a rising `abstain` on `BATCH` lines whose reason tokens repeat.

## The trigger

*When fixing one small thing makes everything faster.* A single fault that every card pays —
a false red in a package's tests, an idle watch that kills busy cards, a batch refused for one
card's shape, a `TMPDIR` inside a repo — multiplies through the whole rate: `rate =
concurrency / wall per card`, and the wall of every card carries the fault. Retries hide it as
noise. The moment one such fault is named, the stop begins; the second and third are usually
behind it (2026-09-15: nine were).

## The scope during a stop

A contraction phase is bugs only (SPEC-PULSE **Rate and convergence** 7):

- fix cards with a reproducing test and its red line quoted ([WORKER-CARDS.md](WORKER-CARDS.md)
  23), reads, rebases;
- every fix is one PR, one fault, the test named after the sentence it broke; no assertion is
  weakened, a flaky test gets a sync point, never a sleep;
- tests answer inside the fast tier, one minute per package, two at most (#516);
- anything expansionary is labelled `next-push` in its issue and never carded; the manager's
  `scope-regex` admits nothing else;
- the loop keeps running, narrower: the fixes are cards too, and the queue floor is held with
  them;
- one line per fault on the record — the issue, the test, the PR — so the stop is
  auditable when it is over.

## The exit gate: the trust batch

The stop ends by evidence, not by feeling. The fix cards of the stop, plus one known-answer
card per bench, rerun as **one batch on the fixed tools**, and every card scores `done` with its
red line quoted and its `RESULT.md` line 1 equal to its contract — no abstain, no
`harness-silent`, no `idle`, no false red in any package. That batch is the trust batch; its
`BATCH` line is quoted in the closing note, the adoption receipt names the tool versions
([SPEC-PULSE.md](SPEC-PULSE.md), `ADOPTION`), and only then does the queue widen again to the
bench widths. A trust batch with one abstain is not the exit; it is the next fault.

## The record: 2026-09-15/16, the first

The day's rate was measured at 44.6 cards per hour with effective parallelism 3.5 against 64
slots (#536); nine faults were paying for the gap. The stop was called on the evening of
2026-09-15; the faults and their fixes:

| fault | issue | fix |
|---|---|---|
| one card's quoted issue text refused the whole batch; 34 cards never ran | #529 | landed, #577 |
| two batches shared a slot; the second overwrote the first's log | #457 | landed, #577 |
| `abstain log=N` named no reason; every `RESULT.md` had to be opened | #461 | landed, #577 |
| macOS `/var` symlink made every nova-swarm card read a red suite | #578 | landed, #586 |
| `--config` refused a keyless local provider, then every unrelated provider | #523 | landed, #586 |
| the bench pull copied before the file landed, filtered to nothing, missed `repo/RESULT.md` | — | landed, #581 |
| `wait` left a beat that made the next `send` refuse, for every friend | #488 | landed, #588 |
| `TMPDIR` under the job repo turned a "not a repo" test red on every card | #460 | open |
| the idle watch killed cards whose `go test` printed nothing | #593 | open |
| `RESULT.md` written under `repo/` on the local bench scored `no-result` | #594 | open |
| a silent harness scored `NATIVE OK rc=0`; the local route was unproven | #591 | open |
| `go test ./cmd/nova-bus/` at 148 s paid by every card and CI job | #516 | open |

The rules the stop wrote are in their specs: the nine rate and convergence rules (#553,
SPEC-PULSE **Rate and convergence**), the status and progress verbs (#549, #536), the bench
width (#528, SPEC-SWARM **Benches**), the swarm's per-card admission, slot locks and reason
tokens (SPEC-SWARM **scatter** and **gather**), and practices 22-25 of WORKER-CARDS. The trust
batch that closes this stop is owed: it runs when the four open rows land, and its `BATCH` line
goes here.
