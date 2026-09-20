RESULT tools22-rule-toolwork-5-L670 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 5 says?
CONFORMS internal/decide/harvestclass.go:329
SPEC docs/SPEC-TOOLWORK.md:670 rule 5
PKG internal/decide
ASK Every outcome classification must be written as a single appended JSON line to <queue>/decide/outcomes.jsonl with class= and conf= fields so the Jev tuning lane measures agreement from structured rows.

deciding lines:
  internal/decide/harvestclass.go:324: // AppendOutcomeRow appends one JSON line to outcomes.jsonl beside the route log
  internal/decide/harvestclass.go:329: func AppendOutcomeRow(path, unit string, c Classification) error {
  ...
  internal/decide/harvestclass.go:352: f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
  internal/decide/harvestclass.go:357: if _, err := f.Write(append(body, '\n')); err != nil {

GUARDED-BY internal/decide/harvestclass_test.go:197 TestEveryOutcomeIsOneAppendedJSONLRow

grep -- ran:
  grep -rn 'outcomes\.jsonl' --include='*.go' .          -> ./internal/decide/harvestclass.go:324; ./internal/decide/harvestclass_test.go:198,406,471
  grep -rn 'AppendOutcomeRow' --include='*.go' .          -> ./internal/decide/harvestclass.go:324,329; ./internal/decide/harvestclass_test.go:202,205,245,407,472
  grep -rn 'func Test' --include='*_test.go' ./internal/decide/ | grep -i outcome  -> :31,197 (harvestclass_test); tune1_test.go:136,189
  No non-test callers of AppendOutcomeRow found in the repo.

Left owed: AppendOutcomeRow has no production callers — the wiring from harvest orchestration into this function is not exercised outside tests. This is a real finding: a UNGUARDED conformance and the cheapest kind of rot.

git status --short (RESULT.md is new and expected to appear)
?? RESULT.md
