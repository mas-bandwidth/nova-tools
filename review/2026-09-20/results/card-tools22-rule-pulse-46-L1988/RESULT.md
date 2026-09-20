RESULT tools22-rule-pulse-46 sha=5298f6be12ea — does the code at this base do what docs/SPEC-PULSE.md rule 46 says?
ABSENT
SPEC docs/SPEC-PULSE.md:1988 rule 46
PKG internal/pulse
ASK An implementation would have to, at launch/admission time, count the prior attempts of a contract line across the queue and refuse any card whose line has reached the default `--max-attempts 2` by printing `ADMIT REFUSED card=<label> gate=attempts 2`, while admitting a first attempt.
VERDICT The `gate=attempts` admission refusal is not implemented anywhere in the tree. The only admission gate in the source is `gate=stop` (internal/pulse/admission.go:84), which prints `ADMIT REFUSED card=... gate=stop <reason>` under a `STOP` file; the spend/attempts/scope gates named in spec rule 9 (docs/SPEC-PULSE.md:139-146) do not appear in any `.go` file.
Greps run (all returned no matching `gate=attempts|gate=spend|gate=scope`):
  grep -rn "gate=attempts\|gate=spend\|gate=scope" --include='*.go' .
  grep -rn "gate=" --include='*.go' internal/pulse/            (only admission.go:84 gate=stop)
  grep -rn "admit-refuses\|third-attempt\|RefusesThirdAttempt" --include='*.go' .
  grep -rn "ADMIT REFUSED" --include='*.go' internal/pulse/    (only admission.go:84)
The attempt tracking that does exist is the manager abstain requeue (internal/pulse/manager.go:592-597, `attempts := attemptOf(...)+1` vs `m.pol.MaxAttempts`, default 1 per manager.go:77), which escalates an `ABSTAIN` — not a launch-time `ADMIT REFUSED gate=attempts` — and the `Prior attempts:` line written onto a fix card by cutkind.go:206. Neither emits the rule's `ADMIT REFUSED card=<label> gate=attempts 2`, and no test in internal/pulse names `admit-refuses-third-attempt` or asserts `gate=attempts`.
Files read: internal/pulse/admission.go (full), internal/pulse/manager.go:560-649, docs/SPEC-PULSE.md:130-159 and 1988.
Left owed
