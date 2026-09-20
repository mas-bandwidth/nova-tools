RESULT tools22-rule-wake-13 sha=5298f6be12ea
CONFORMS internal/wake/source.go:148
SPEC docs/SPEC-WAKE.md:4267 rule 13
PKG internal/wake
ASK Every output line produced by the `watch`, `probe` and `serve` verbs must start with the token `WAKE`, rather than the prototype's ad-hoc `no change in ...` or `CHANGE after=...` with ungrammatical item lines.
The `Render` function at `internal/wake/source.go:148-222` produces every item line with a `WAKE <TOKEN>` prefix (e.g. `WAKE BUS`, `WAKE ENTRY`, `WAKE REPORT`, `WAKE LINE`, `WAKE PR`, `WAKE RUN`, `WAKE BRANCH`, `WAKE LOCK`).
Verdict lines at `cmd/nova-wake/main.go:1243-1260` use `WAKE CHANGE`, `WAKE QUIET`, `WAKE STOPPED`, `WAKE BROKEN`.
Probe output at `internal/wake/probe.go:453,564` uses `WAKE PING`, `WAKE PROBE`.
Refusals at `internal/wake/probe.go:524` use `WAKE REFUSED`.
There is no bare `no change in ...` or `CHANGE after=...` with ad-hoc item lines anywhere in the code.
GUARDED-BY cmd/nova-wake/lessons_test.go:28 TestNoFalseWakeOnReload
GUARDED-BY cmd/nova-wake/lessons_test.go:83 TestBlocksRatherThanTicks
GUARDED-BY cmd/nova-wake/main_test.go (many)
Left owed: The `awake` verb prints `FRIEND` and `AWAKE OK`/`AWAKE REFUSED` lines, which do not start with `WAKE` — but those tokens are explicitly listed in the spec's grammar (docs/SPEC-WAKE.md:526-528) as the `awake` verb's separate grammar, not covered by this rule.
grep -rn "no change" --include='*.go' .  → only hits in test commentary, never in output
grep -rn "CHANGE" --include='*.go' internal/wake/ → only comments, never bare `CHANGE after=`
Every output-formatting path (source.go Render, probe.go render, main.go verdicts) uses `WAKE` as literal first token.
git status --short: (nothing)