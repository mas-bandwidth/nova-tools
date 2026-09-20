RESULT tools22-rule-wake-14 sha=5298f6be12ea
CONFORMS internal/wake/source.go:260
SPEC docs/SPEC-WAKE.md:4271 rule 14
PKG internal/wake
ASK A relayed line must be shortened to a bounded tail with a visible `...+<dropped>B` mark, so a reader can always tell a tool's cut from an author's own ellipsis.
Deciding lines:
  internal/wake/source.go:260 func escapeTail(s string) string {
  internal/wake/source.go:261 	return oneline.Escape(oneline.Cap(s, oneline.TailBytes))
  internal/wake/source.go:262 }
  internal/wake/source.go:154 "WAKE BUS LINE " + oneline.Escape(oneline.Cap(strings.TrimPrefix(key, "bus:line:"), oneline.TailBytes))
  internal/wake/bus.go:377 res.Standing = append(res.Standing, "WAKE BUS STANDING "+escapeTail(line))
  internal/oneline/oneline.go:157 const TailBytes = 500
  internal/oneline/oneline.go:220 return s[:cut] + mark(len(s)-cut)
  internal/oneline/oneline.go:225 func mark(dropped int) string { return fmt.Sprintf("...+%dB", dropped) }
GUARDED-BY cmd/nova-wake/serve_test.go:630 TestASoLongBusLineIsCappedWhenServeRelaysIt
  (asserts a relayed WAKE BUS LINE is under 2*oneline.TailBytes and carries "...+" and "B"; passes `go test ./cmd/nova-wake/ -run TestASoLongBusLineIsCappedWhenServeRelaysIt`)
Greps: `grep -rn "TailBytes\|cut160\|dropped\|dropped>B" --include='*.go' .`; `grep -rn "func Test" --include='*_test.go' internal/wake/`; `grep -rn "Render\|WAKE BUS LINE\|TailBytes\|Cap(" --include='*_test.go' internal/wake/`; `grep -rn "WAKE BUS LINE\|WAKE BUS STANDING\|escapeTail\|TailBytes" --include='*_test.go' .`
Left owed: none.