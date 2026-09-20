RESULT tools22-rule-wake-14-L4271 sha=5298f6be12ea
CONFORMS internal/wake/source.go:154
SPEC docs/SPEC-WAKE.md:4271 rule 14
PKG internal/wake
ASK A relayed line must never be cut in silence: any truncation to the one-line ceiling must carry the `...+<dropped>B` mark, at the `oneline.TailBytes` budget, so a reader can tell a cut from an author's own ellipsis.
The deciding lines:
- internal/wake/source.go:154 — `return "WAKE BUS LINE " + oneline.Escape(oneline.Cap(strings.TrimPrefix(key, "bus:line:"), oneline.TailBytes))` — the relayed line is capped with the mark.
- internal/wake/source.go:260-262 — `func escapeTail(s string) string { return oneline.Escape(oneline.Cap(s, oneline.TailBytes)) }` with the comment "capped at the repo's tail budget with the ...+<dropped>B mark that a reader can tell from an author's own ellipsis. The prototype's cut160 cut with no mark at all." Used for every other relayed tail (PR/RUN/BRANCH/LOCK unreadable; bus.go:377 WAKE BUS STANDING).
- internal/oneline/oneline.go:170-172 — "it leaves a mark: `...+<dropped>B`. The mark is the point."; Cap at oneline.go:181-221 writes it via mark() at oneline.go:225.
GUARDED-BY cmd/nova-wake/serve_test.go:630 TestASoLongBusLineIsCappedWhenServeRelaysIt (asserts the relayed line is capped and marked at :643-648, error text naming "the prototype's cut160"; confirmed passing). Also internal/oneline/oneline_test.go:235 TestCapMarksWhatItDropped guards the mark itself. No test under internal/wake/ guards it directly.
Greps run: `grep -rn "cut160|TailBytes|dropped" --include='*.go' .` (truncated, full in tool output); `grep -rn "cut160\|160 bytes\|160B\|TailBytes\|escapeTail\|Cap(" --include='*.go' internal/wake/`; `grep -rn "func Test" --include='*_test.go' internal/wake/`; `grep -rn "WAKE BUS LINE\|WAKE BUS STANDING\|escapeTail" --include='*_test.go' .`; `grep -rn "cut160" .`. Files read: internal/wake/source.go, internal/wake/bus.go (355-394), internal/oneline/oneline.go, internal/oneline/oneline_test.go, cmd/nova-wake/serve_test.go (615-669), docs/SPEC-WAKE.md (4180-4297).
Left owed