# Zhi's DSH push-route experiment (missed-07)

Status: no push route proven; the existing `route=defer` fallback is not verified
in this checkout, so no adapter change is made on that basis.

Measured on 2026-10-04 with DeepSeek Harness 0.2.0-rc.2 (`dsh` from the
installed app's `runtime/cli/bin/dsh`), in an isolated DSH home with no
credentials:

- isolated home: `/Volumes/nova/ai/zhi/working/jobs/missed-07-zhi-preset-experiment.w2~15/experiment/dsh-home`
- disposable workspace: `/Volumes/nova/ai/zhi/working/jobs/missed-07-zhi-preset-experiment.w2~15/experiment/disposable-workspace`
- disposable session: `session-E9E55E39-C3C4-4497-B32F-E0BD9ED956E8`
- preset: `minimal` (the session's v4 transcript header carries
  `agentPreset:"minimal"`; whether the production desktop profile also defaults
  to `minimal` was not checked here and is not claimed)

The supported headless adoption was run against that throwaway id only, never
against the production session:

- argv: `dsh headless --session-id session-E9E55E39-C3C4-4497-B32F-E0BD9ED956E8 -`
- marker on stdin: `marker: zhi-preset-experiment-session-E9E55E39-C3C4-4497-B32F-E0BD9ED956E8`
- exit code: `1`
- stderr, verbatim:
  `dsh: session "session-E9E55E39-C3C4-4497-B32F-E0BD9ED956E8" runs under agent preset "minimal", which the one-shot runner does not compose`

The marker never reaches the disposable session's transcript: the transcript's
SHA-256 before the run is
`357e89daf9b83e92570bdeac69d8724123cde672fa9a320dd635a78391ba2eaa` and is
identical after the run.

Not measured: whether an open desktop conversation receives the marker, or
whether a separate process merely appends to the transcript. The only run with
argv, exit, stderr and a before/after transcript SHA is the refused `minimal`
session above; the refusal happens before any write, so it shows neither. A
headless session with no agent preset was also created in the isolated home, but
it is not an open desktop conversation and not a composable preset, and no argv,
exit, marker or SHA delta was recorded for it, so nothing is concluded from it.

The busy-session repeat was not run: the disposable session has no live
desktop turn, and creating one would require launching the Electron desktop
session with credentials, which this card forbids in the throwaway home. No
production session was targeted.

Conclusion: the one-shot runner refuses a `minimal`-preset desktop session
before any write. `grep -rn "route=defer"` over this checkout finds the phrase
only in this doc, so the daemon's `route=defer` fallback is not verified from
this repository; no adapter change is made on that unverified basis.
