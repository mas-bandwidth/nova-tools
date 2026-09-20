RESULT tools22-rule-tokens-11-L229 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 11 says?
CONFORMS internal/bounded/bounded.go:57
SPEC docs/SPEC-TOKENS.md:229 rule 11
PKG internal/tokens
ASK Every listing must be capped at `--max` (default 20, 0 for all), print one MORE line per overflowing kind naming the remedy, count every item regardless of cap, print counts on failure as well as success, and have tests measuring the output bound in lines and bytes at the largest plausible state.

Deciding lines:
- `internal/bounded/bounded.go:57` — `const Default = 20`
- `internal/bounded/bounded.go:83-85` — `Capped(w, max, token, kind, remedy)` creates a capped list with a remedy string
- `internal/bounded/bounded.go:96-112` — `Line()` always increments `total`, prints only while `shown < max` (or `max <= 0`)
- `internal/bounded/bounded.go:122-128` — `More()` prints ONE `TOKEN MORE kind= shown= total= remedy` line when items were elided
- `cmd/nova-tokens/main.go:1263` — `bounded.Capped(stdout, *max, "SUM", "pair", ...)` for the sum verb's pair listing
- `cmd/nova-tokens/main.go:1269` — `bounded.Capped(stdout, *max, "SUM", "model", ...)` for the sum verb's model listing
- `cmd/nova-tokens/main.go:1321` — `max := fs.Int("max", bounded.Default, "")` in check, with `bounded.Default` (20) as default
- `cmd/nova-tokens/main.go:412` — `maxRemedy` function returns the remedy string with `--max 0`

GUARDED-BY cmd/nova-tokens/bounded_test.go TestFoldIsBoundedAtTheLargestPlausibleState
GUARDED-BY cmd/nova-tokens/bounded_test.go TestSourcesIsBoundedAtTheLargestPlausibleState
GUARDED-BY cmd/nova-tokens/bounded_test.go TestCheckIsBoundedAtTheLargestPlausibleState
GUARDED-BY cmd/nova-tokens/bounded_test.go TestSumIsBoundedAtTheLargestPlausibleState

Greps run:
- `grep -rn 'bounded.Capped\|bounded.Default\|--max' --include='*.go' internal/tokens/ cmd/nova-tokens/ internal/bounded/`
- `grep -rn 'func Test.*Bounded\|func Test.*Largest' --include='*_test.go' cmd/nova-tokens/`
- `sed -n '180,270p' docs/SPEC-TOKENS.md`

Left owed: none.
git status --short