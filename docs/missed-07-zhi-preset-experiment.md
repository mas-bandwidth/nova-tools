# Zhi's DSH push-route experiment (missed-07)

Status: no push route proven. Zhi's daemon is off because the DSH session
preset refuses the headless runner.

Measured on 2026-10-04, DSH desktop app v0.2.0-rc.2, session
session-e9b0dc0e-b03d-4516-b40e-6a0287ca218b under the desktop preset
"minimal": `dsh headless --session-id <id> -` refuses with the preset error
and the adapter defers (route=defer). The open session's persisted directory
holds an empty `session.lock` and one `session.v4.jsonl.zstd` transcript; no
append-to-file, local-socket, or IPC route into the live desktop turn is
exposed by the app. A separate disposable session could not be launched in
this environment (no credential-safe throwaway launch available here), so
the controlled busy-session repeat was not run.

Fallback: the session's own blocking bus read; the daemon reports route=defer
or passive in presence, never a silent loss.
