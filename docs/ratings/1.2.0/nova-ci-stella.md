# nova-ci READ and USE rating, nova-tools 1.2.0

Rater: Stella (gpt-5.6-terra, Codex external child)
Build: 78e40bbe949f
READ: 7/10
USE: 0/10

This is a source-read and release-availability rating of the candidate at
`sprint/mechanical-2026-10-02` commit `78e40bbe949fed8bf1f7512fb1e31413884438fe`.
It is not a claim that the candidate was executed. The public `v1.2.0` release
endpoint and expected darwin/arm64 asset both returned HTTP 404; the only local
binary reported `v1.2.0-dev.0d56536c`, which is a different build and was not
used for help or behavior findings. A Linux bench host and Go-slot grant were
not supplied, so this Studio did not build, test, or run Go. The complete
availability transcript, including commands, output, exits, build identity and
the 404 refusal, is preserved at
`jobs/rerate-stella-ci-b.w2~15/repo2/evidence/release-availability.txt`.

## Reasons

READ. The source banner is unusually good at describing a difficult tool before
its usage list: it says what `slowtests` consumes, separates the state-free
verbs from checkout and store verbs, and gives a first run that needs no module
or store (cmd/nova-ci/main.go:26-35). The `slowtests` contract includes the
measurement-versus-verdict distinction, ledger shape, cache caveat and bounded
output in one place (cmd/nova-ci/main.go:40-72). The source also gives each
top-level verb an explicit effect and documents `bench run` as a constrained
Linux-bench delivery rather than a local build (cmd/nova-ci/main.go:73-123).

The rating stops at 7 because it cannot be read or tried as the claimed release:
there is no public release identity to install, and a different local dev binary
would make an apparently useful transcript false evidence for this build. The
static interface also still offers one JSON rendering only on `slowtests`, even
though the banner places several other AI-facing verbs in the same tool. The
usage presents `--budget` and `--package-budget` as alternatives, but the
implementation silently lets the latter replace the former. `bench run` asks
the shared help renderer for the full banner as its exit table, making the
compound verb's focused help depend on a broad top-level document.

USE. A release an AI cannot obtain and identify cannot earn a use score. No
matching artifact exists, no substitute build was attributed to this candidate,
and no Linux bench identity or slot was provided for an exact build. The zero
is a release-readiness score, not a claim that the source implementation returns
zero useful results. The source preserves strong prospects: a built-in,
store-free first run; explicit effect labels; and a bench verb designed to keep
Go work off the working machine.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | GitHub release `v1.2.0` and `nova-ci_v1.2.0_darwin_arm64` | both the release endpoint and expected artifact returned HTTP 404, while the only local binary identifies as a different `v1.2.0-dev.0d56536c` build; an AI cannot install or verify the candidate it is asked to trust | publish the immutable `v1.2.0` tag, checksums and platform assets, and make `nova-ci version` report the candidate's release identity | L |
| 2 | cmd/nova-ci/main.go:40-42,73-123 | only `slowtests` advertises `--json`; the other inspection, local-write and delivery verbs have unrelated line-only output shapes, so a programmatic AI must parse a different dialect for each | render every verb from the common result value and accept `--json` consistently | L |
| 3 | cmd/nova-ci/main.go:40,46-48; cmd/nova-ci/slowtests.go:151-154 | the banner displays `--budget` and `--package-budget` with `|`, but passing both is accepted and `--package-budget` silently wins | refuse the pair together and name the one flag to remove, or document the precedence in the synopsis and flag help | S |
| 4 | cmd/nova-ci/bench.go:30-40; cmd/nova-ci/main.go:209-229 | `bench run` supplies `exitTable("bench run")` to the shared renderer, while `exitTable` rebuilds text from the complete top-level usage; the compound verb cannot have a concise, self-contained help contract | give `bench` a compact group table and return only the common exit paragraph plus the `bench run` row | S |

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
| an earlier 1.1.0 USE read could execute a matching build | NOT RECONFIRMED | the exact candidate release endpoint and expected darwin asset return 404; the local `v1.2.0-dev.0d56536c` identity is different |
| prior ratings could score use from real scratch runs | BLOCKED for this candidate | no matching artifact and no supplied Linux bench host or Go-slot grant; no Studio Go command was run |
