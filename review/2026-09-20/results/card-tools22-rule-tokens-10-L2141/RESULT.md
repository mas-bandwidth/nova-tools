"RESULT tools22-rule-tokens-10-L2141 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 10 says?
CONFORMS internal/tokens/dayfile.go:129
SPEC docs/SPEC-TOKENS.md:2141 rule 10
PKG internal/tokens
ASK The implementation must compare daily-file token totals against a fresh fold's totals per type, flagging any type that went lower or became unknown (dash), while treating dash→number as coverage arriving rather than a shrink.

Deciding lines (internal/tokens/dayfile.go):

dayfile.go:106-114: Totals sums per-type across all rows of a DayFile.
```go
// Totals is the day's per-type totals, and whether any row reported each type. It is what
// the shrink comparison compares.
func (d *DayFile) Totals() Counts {
	var c Counts
	for _, r := range d.Rows {
		c.Add(r.Counts)
	}
	return c
}
```

dayfile.go:116-142: Shrinks detects every type that shrank (went lower) or vanished (number→dash). A dash→number is excluded:
```go
// Shrink is one type that would go backwards.
type Shrink struct {
	Day  string
	Type Type
	File string // the number in the file
	Now  string // the number now, or a dash where the source went quiet
}

// Shrinks compares what is on disk with what a fold has just computed. A type that is
// lower, or that was a number and is a dash now, is a source that went quiet, and a source
// that went quiet must never silently lower a day's spend.
//
// A dash in the file that is a number now is NOT a shrink: that is coverage arriving.
func Shrinks(old, now Counts, day string) []Shrink {
	var out []Shrink
	for t := Type(0); t < NTypes; t++ {
		was, hadIt := old.Get(t)
		is, hasIt := now.Get(t)
		switch {
		case hadIt && !hasIt:
			out = append(out, Shrink{Day: day, Type: t, File: strconv.FormatInt(was, 10), Now: Dash})
		case hadIt && hasIt && is < was:
			out = append(out, Shrink{Day: day, Type: t, File: strconv.FormatInt(was, 10), Now: strconv.FormatInt(is, 10)})
		}
	}
	return out
}
```

The calling code in cmd/nova-tokens/main.go:675-711 wires these into the TOKENS SHRANK output line, --allow-shrink gate, file preservation semantics, and exit 1/exit 0. Specifically:
- main.go:675 `dayShrinks = tokens.Shrinks(old.Totals(), file.Totals(), d)`
- main.go:708 prints `TOKENS SHRANK date=%s type=%s file=%s now=%s written=%t`
- main.go:695 `if !partial && (!shrank || *allowShrink) ...` gates file writes
- main.go:726 `(shrankList.Total() > 0 && !*allowShrink)` drives exit 1
- main.go:734 `return 1` / line 737 `return 0` are the exit codes

GUARDED-BY internal/tokens/tokens_test.go:47 TestTheShrinkComparisonWithADashOnEitherSide

(Grep evidence: `grep -rn "SHRUNK" --include='*.go' internal/tokens/` → no direct match; `grep -rn "TOKENS SHRANK" --include='*.go' internal/tokens/` → no match; `grep -rn "Shrinks(" --include='*.go' internal/tokens/` → found in dayfile.go:129 and tokens_test.go:53,65; `grep -rn "AllowShrink\|allow.shrink" --include='*.go' internal/tokens/` → no match in this package; the flag is in cmd/nova-tokens/main.go:493.)

Left owed
git status --short
