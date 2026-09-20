RESULT tools22-rule-wake-1-L1819 sha=5298f6be12ea
CONFORMS cmd/nova-wake/main.go:703
SPEC docs/SPEC-WAKE.md:1819 rule 1
PKG internal/wake
ASK A `watch` must require `--max` and `--on-deadline <word>`, refuse to start (exit 2) naming whichever is missing, never poll or wait past `--max`, and when the deadline arrives print exactly one `WAKE QUIET` verdict line echoing the default word and exit 0, taking no action itself.

Deciding lines, `cmd/nova-wake/main.go`:
- L703–708: `--max` and `--on-deadline` are required; missing one adds a refusal.
- L833–834: if any problem exists, return `p.print(stderr, verb)` which prints each `refusing to guess` line and returns 2.
- L1144–1150: the deadline is checked BEFORE any poll; `reached := !now.Before(deadline)`.
- L1158: `if !reached && !stopped {` — no polling past deadline.
- L1259–1260: verdict line `WAKE QUIET after=%s polls=%d default=%s sources-failing=%d: deadline, default taken` with `onDeadline` echoed.
- L968–972: opening line echoes `on-deadline=<word>`.
- L1304–1319: `until` caps the sleep at `deadline`, never past `--max`.
- L12–14: comment: "EVERYTHING IT PRINTS IS DATA" — the tool takes no action itself.

GUARDED-BY cmd/nova-wake/main_test.go:257 TestAWatchNamesItsDeadlineAndItsDefault
  Tests missing `--max` → exit 2 naming `--max`, missing `--on-deadline` → exit 2 naming `--on-deadline`, both missing in one run, a watch that reaches its deadline → exit 0 with exactly one `WAKE QUIET` line echoing the default word and the clock advancing exactly `--max`, `--max` over ceiling → exit 2, no source → exit 2.

Greps run:
  `grep -rn "on-deadline\|on_deadline\|refusing to guess\|WAKE QUIET\|--max" --include='*.go' internal/wake/` — found only incidental mentions (not the rule implementation).
  `grep -rn "refusing to guess\|WAKE QUIET\|after=.*polls=.*default=" --include='*.go' .` — found the verdict and refusal logic in `cmd/nova-wake/main.go`.
  `ls internal/wake/` — listed package files.

Left owed

git status --short