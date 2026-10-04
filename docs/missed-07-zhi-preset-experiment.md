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
  `agentPreset:"minimal"`; the production desktop profile at
  `~/.dsh/profiles/desktop/cordis.patch.yml` sets `agent-preset-registry`
  `selectedDefault: minimal`)

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
identical after the run. Measured in the same isolated home with a throwaway
headless session that has no agent preset, `session-582bfb82-9dae-4dfa-b9e3-f7bfdc4bfe93`,
transcript
`<isolated-home>/sessions/--Volumes-nova-ai-zhi-working-jobs-missed-07-zhi-preset-experiment.w2~007E15--/session-582bfb82-9dae-4dfa-b9e3-f7bfdc4bfe93/session.v4.jsonl.zstd`:
the one-shot runner adopted the session and appended the marker to that
session's own transcript as a separate process, then the turn stopped at
`MISSING_CREDENTIAL` with no credential in the isolated home; no open desktop
conversation received it. So the open conversation does not receive the marker,
and a separate process append is the write path the headless runner has.

The busy-session repeat was not run: the disposable session has no live
desktop turn, and creating one would require launching the Electron desktop
session with credentials, which this card forbids in the throwaway home. No
production session was targeted.

Conclusion: the one-shot runner refuses a `minimal`-preset desktop session
before any write. `grep -rn "route=defer"` over this checkout finds the phrase
only in this doc, so the daemon's `route=defer` fallback is not verified from
this repository; no adapter change is made on that unverified basis.
