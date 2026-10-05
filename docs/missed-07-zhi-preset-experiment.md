# Zhi's DSH push-route experiment (missed-07)

Status: no push route into Zhi's open desktop session exists, and the DSH
adapter already defers. internal/friend/adapter_dsh.go:25-34 records that a
session under an agent preset is refused by the one-shot runner, the delivery is
Deferred, and "No route into the open desktop session exists, so Route answers
defer"; the dsh row at docs/SPEC-FRIEND.md:1108 records the 2026-10-05 probe
against Zhi's real session (exit 1, preset `minimal` refused, transcript hash
unchanged, deliver.log 1339+ consecutive deferred attempts, no local listener or
IPC socket). No adapter change is made.

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

No-preset case, same isolated home on 2026-10-04 (docs/SPEC-FRIEND.md:1108):
with no preset the runner appended a turn to the persisted record (turn 2 in the
same record, 9 KB to 16 KB), so a separate process does write to the
transcript (adapter_dsh.go:19-21; the runner adopts only a session with no
preset, and a session never returns to none). That run is not repeated here and
has no argv, exit or SHA recorded in this card. Still not measured: whether an
open desktop window shows that appended turn, because no open desktop
conversation was available in the throwaway home. The refused `minimal` run
above shows neither, since the refusal happens before any write.

The busy-session repeat was not run: the disposable session has no live
desktop turn, and creating one would require launching the Electron desktop
session with credentials, which this card forbids in the throwaway home. No
production session was targeted.

Conclusion: the one-shot runner refuses a `minimal`-preset desktop session
before any write, and Zhi's real session is such a session. The DSH adapter
therefore defers (internal/friend/adapter_dsh.go:25-34, docs/SPEC-FRIEND.md:1108,
probed 2026-10-05 against the real session). The cmd/nova-friend/main.go:538
and docs/SPEC-FRIEND.md:565 route=push/defer text describes the Grok adapter and
is not evidence for Zhi. Unverified beyond the spec: whether the production
desktop profile defaults to `minimal` independently of that session, and whether
an open window displays a turn appended by a no-preset headless run. No adapter
change is made.
