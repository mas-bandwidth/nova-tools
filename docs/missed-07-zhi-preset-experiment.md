# Zhi's DSH push-route experiment (missed-07)

Status: no push route proven; the existing `route=defer` fallback is sufficient,
so no adapter change is made.

Measured on 2026-10-04 with DeepSeek Harness 0.2.0-rc.2 (`dsh` from the
installed app's `runtime/cli/bin/dsh`), in an isolated DSH home with no
credentials:

- isolated home: `/Volumes/nova/ai/zhi/working/jobs/missed-07-zhi-preset-experiment.w2~15/experiment/dsh-home`
- disposable workspace: `/Volumes/nova/ai/zhi/working/jobs/missed-07-zhi-preset-experiment.w2~15/experiment/disposable-workspace`
- disposable session: `session-E9E55E39-C3C4-4497-B32F-E0BD9ED956E8`
- preset: `minimal` (the session's v4 transcript header carries
  `agentPreset:"minimal"`; the production desktop profile also selects it as
  the default)

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
identical after the run. For a session the one-shot runner does compose, the
runner itself appends the marker to that session's `session.v4.jsonl.zstd` as
a separate process; no open desktop conversation receives it. So the open
conversation does not receive the marker, and a separate process append is the
only write path the headless runner has.

The busy-session repeat was not run: the disposable session has no live
desktop turn, and creating one would require launching the Electron desktop
session with credentials, which this card forbids in the throwaway home. No
production session was targeted.

Conclusion: the one-shot runner refuses a `minimal`-preset desktop session
before any write, and the daemon already reports `route=defer` for this case.
The existing fallback is sufficient; no adapter change is needed.
