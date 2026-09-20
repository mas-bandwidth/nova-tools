RESULT tools22-rule-tokens-11-L2148 sha=5298f6be12ea
CONFORMS cmd/nova-tokens/bounded_test.go:138
SPEC docs/SPEC-TOKENS.md:2148 rule 11
PKG internal/tokens (bounded via cmd/nova-tokens)
ASK The implementation must build an over-capacity test scenario in t.TempDir(), run each verb that produces listings (fold, sources, check, sum), verify output is capped at 20 items per kind with a single MORE line per overflowing kind, confirm TOKENS NOTE appears exactly once, ensure FAIL/OK count lines carry actual totals not shown numbers, and validate --max 0 disables capping while --max -1 is rejected.

Deciding lines — the ceiling itself:
  internal/bounded/bounded.go:57   const Default = 20
  internal/bounded/bounded.go:96-100 func (l *List) Line(line string) { ... if l.max > 0 && l.shown >= l.max { return }
  internal/bounded/bounded.go:122-128 func (l *List) More() { ... if l.total <= l.shown { return }; fmt.Fprintf(l.w, "%s MORE kind=%s shown=%d total=%d %s\n", ...)
  internal/bounded/bounded.go:83-85 func Capped(w io.Writer, max int, token, kind, remedy string) *List { return &List{w: w, max: max, token: token, kind: kind, remedy: remedy} }
  cmd/nova-tokens/main.go:398-401 func checkMax(r *refusals, max int) { if max < 0 { r.add("--max is a ceiling on each listing, 0 for all, and is never negative...") } }

Guards — where the listing ceilings are wired into every verb:
  cmd/nova-tokens/main.go:494  max := fs.Int("max", bounded.Default, "")    // fold
  cmd/nova-tokens/main.go:507  checkMax(r, *max)                          // fold validation
  cmd/nova-tokens/main.go:550-561  bounded.Capped(...) calls per-kind lists  // fold lists
  cmd/nova-tokens/main.go:918  max := fs.Int("max", bounded.Default, "")    // sources
  cmd/nova-tokens/main.go:931  checkMax(r, *max)                          // sources validation
  cmd/nova-tokens/main.go:1006 max := fs.Int("max", bounded.Default, "")    // report ledger
  cmd/nova-tokens/main.go:1321 max := fs.Int("max", bounded.Default, "")    // other verbs
  cmd/nova-tokens/main.go:1214 max := fs.Int("max", bounded.Default, "")    // other verbs

GUARDED-BY cmd/nova-tokens/bounded_test.go:138 TestFoldIsBoundedAtTheLargestPlausibleState
  (also guarded by TestSourcesIsBoundedAtTheLargestPlausibleState:189, TestCheckIsBoundedAtTheLargestPlausibleState:218, TestSumIsBoundedAtTheLargestPlausibleState:261, TestMaxZeroPrintsAllAndMaxNegativeIsRefused:contract_test.go:346)

The fold test at bounded_test.go:138 exercises every requirement of rule 11:
- builds "ten declared sources" exceeding the ceiling in bounded_test.go:26-28 (overflow = 25)
- verifies prefix-of-20 + one MORE line per kind at bounded_test.go:151-155
- verifies TOKENS NOTE == 1 at bounded_test.go:159
- verifies counts on FAIL line say whole state (e.g. unreadable=25) at bounded_test.go:170-171
- verifies --max 0 prints all with no MORE lines at bounded_test.go:176-183
- verifies --max -1 is refused with exit 2 at bounded_test.go:185-186
- measures lines (< 160) and bytes (< 28 KB) against the spec's table at bounded_test.go:162-167

Additional guards:
  bounded_test.go:189 TestSourcesIsBoundedAtTheLargestPlausibleState
  bounded_test.go:218 TestCheckIsBoundedAtTheLargestPlausibleState
  bounded_test.go:261 TestSumIsBoundedAtTheLargestPlausibleState
  contract_test.go:346 TestMaxZeroPrintsAllAndMaxNegativeIsRefused

Grep evidence:
  grep -rn "func Test.*Bounded\|func Test.*MaxZero" --include='*_test.go' cmd/nova-tokens/
  grep -rn "bounded.Default\|checkMax\|bounded.Capped" --include='*.go' cmd/nova-tokens/
  grep -rn "const Default = 20" --include='*.go' internal/bounded/

Left owed

git status --short
