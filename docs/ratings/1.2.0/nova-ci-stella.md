# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: Stella (gpt-5.6-terra, Codex external child)
Build: 78e40bbe949f
READ: 7/10
USE: 0/10

This is a cold source-read rating of the candidate at
`sprint/mechanical-2026-10-02` commit `78e40bbe949fed8bf1f7512fb1e31413884438fe`.
It is not a claim that the candidate was executed. The exact assigned branch
was cloned from `https://github.com/mas-bandwidth/nova-tools.git` on the named
Vision Linux bench, but its required `flock -w 90 /tmp/nova-go-gate.lock`
timed out before the build. The named Hetzner fallback had 46 MiB free on `/`
and its exact-source clone failed before any Go command. No Go command ran on
this Studio or either bench, and no unrelated installed binary was used for
help or behavior findings. The full command, stdout, stderr and exit receipts
are preserved in this job's evidence packet.

## Reasons

READ. The source banner is unusually good at describing a difficult tool before
its usage list: it says what `slowtests` consumes, separates the state-free
verbs from checkout and store verbs, and gives a first run that needs no module
or store (cmd/nova-ci/main.go:26-35). The `slowtests` contract includes the
measurement-versus-verdict distinction, ledger shape, cache caveat and bounded
output in one place (cmd/nova-ci/main.go:40-72). The source also gives each
top-level verb an explicit effect and documents `bench run` as a constrained
Linux-bench delivery rather than a local build (cmd/nova-ci/main.go:73-123).

The rating stops at 7 because its use path remains unobserved: the actual
candidate could not enter the prescribed serialized Linux build/use route.
The static interface also still offers one JSON rendering only on `slowtests`,
even though the banner places several other AI-facing verbs in the same tool.
The usage presents `--budget` and `--package-budget` as alternatives, but the
implementation silently lets the latter replace the former. `bench run` asks
the shared help renderer for the full banner as its exit table, making the
compound verb's focused help depend on a broad top-level document.

USE. The zero means use was not established, not that the source implementation
returns zero useful results. The prescribed candidate build/use route is still
blocked by the observed Vision lock timeout and Hetzner storage failure; no
substitute artifact or Studio build was attributed to it. The source preserves
strong prospects: a built-in, store-free first run; explicit effect labels; and
a bench verb designed to keep Go work off the working machine.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-ci/main.go:40-42,73-123 | only `slowtests` advertises `--json`; the other inspection, local-write and delivery verbs have unrelated line-only output shapes, so a programmatic AI must parse a different dialect for each | render every verb from the common result value and accept `--json` consistently | L |
| 2 | cmd/nova-ci/main.go:40,46-48; cmd/nova-ci/slowtests.go:151-154 | the banner displays `--budget` and `--package-budget` with `|`, but passing both is accepted and `--package-budget` silently wins | refuse the pair together and name the one flag to remove, or document the precedence in the synopsis and flag help | S |
| 3 | cmd/nova-ci/bench.go:30-40; cmd/nova-ci/main.go:209-229 | `bench run` supplies `exitTable("bench run")` to the shared renderer, while `exitTable` rebuilds text from the complete top-level usage; the compound verb cannot have a concise, self-contained help contract | give `bench` a compact group table and return only the common exit paragraph plus the `bench run` row | S |

## Good, keep

- The banner makes the first run real without a server or source checkout:
  `slowtests --example` is described as a built-in stream, not a pretend
  transcript (cmd/nova-ci/main.go:33-35,146-149).
- The `slowtests` description says that load is measured but does not alter the
  verdict, a vital distinction for an AI interpreting CI output
  (cmd/nova-ci/main.go:57-64).
- The bench design names its copy, execution and removal boundaries plainly;
  it is the right shape for the required Linux-only Go gates
  (cmd/nova-ci/bench.go:77-116; docs/SPEC-CI.md:429-489).

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| earlier 1.1.0 ratings found only `slowtests` on `--json` | STILL THERE in candidate source | cmd/nova-ci/main.go:40-42 lists `--json` only for `slowtests`; the other banner entries at lines 73-123 do not offer it |
| an earlier 1.1.0 USE read could execute a matching build | NOT RECONFIRMED | the Vision Linux route timed out acquiring the required shared Go lock; the Hetzner route had 46 MiB free and could not clone the candidate |
| prior ratings could score use from real scratch runs | BLOCKED for this candidate | no Go command ran: Vision did not grant the bounded lock and Hetzner storage could not stage the source; no Studio Go command was run |
