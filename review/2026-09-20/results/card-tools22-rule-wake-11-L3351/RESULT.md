RESULT tools22-rule-wake-11-L3351 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 11 says?
CONFORMS internal/wake/state.go:396
SPEC docs/SPEC-WAKE.md:3351 rule 11
PKG internal/wake
ASK A delivery is the printed line, not the state write, so the tool must keep every unprinted observation in a queue, delete a record and write its `printed=<id>` mark ONLY after that line reached stdout (never before the print, never on a failed write), append a newer observation behind an unprinted one rather than over it, and order the `--advance-cursor` transaction so that a kill between the advance returning and the write of its output leaves `bus:advance=inflight`, which the next call recovers by spooling every carried note with `--open-max <carrying>` before anything else.

Deciding lines:

internal/wake/state.go:46-48  "A change is delivered when its line has been written to stdout and nothing else, so a record leaves the queue only by being printed (rule 11)."
internal/wake/state.go:396-403  "MarkPrinted deletes a record and writes printed=<its id> for its key. It is called ONLY after the record's line reached stdout (rule 11, step 3): a kill before this leaves the entry pending, so the duplicate is a repeated wake and never a lost one."
internal/wake/state.go:283-306  "A change APPENDS a queue record and then replaces the newest value. It never replaces an UNPRINTED value ... red then green with the red unprinted prints red, then green, in that order. Nothing coalesces." (ObserveDisplay at 297)
internal/wake/advance.go:30-38   step 3 "write bus:advance=inflight|<stamp>|<head>, run `inbox --advance ...`, spool what it listed, write the state, and only then clear the marker"; step 4 "a call that finds the marker runs `inbox` plainly, reads carrying=<n>, and lists the whole carried list with `inbox --open --open-max <n>` -- n, never a fixed number".
internal/wake/advance.go:240-249  "st.Set(AdvanceMarker, ...); save()" before the advance, with the injected kill "after-advance" returning before any output is written.
internal/wake/advance.go:188-189  recovery deletes the marker only after the carried list is fully listed; note "bus advance was interrupted; recovered %d notes from OPEN".
internal/bounded/bounded.go:107-111  a line is counted shown only if its write to the stream succeeded, so a failed stdout is never marked printed=.
cmd/nova-wake/main.go:1511-1578  printQueue: item lines to stdout, then per-kind WAKE MORE line with shown/total/n, then MarkPrinted per line that reached the stream; kill points after-observed/after-lines/after-marks at main.go:1192,1557,1196.
cmd/nova-wake/main.go:1647  the advance marker is cleared only after w.save() (output durable).

GUARDED-BY cmd/nova-wake/main_test.go:721 TestDeliveryIsThePrintedLine  (also internal/wake/state_test.go:74 TestAnObservationIsAppendedBehindAnUnprintedOne, internal/wake/state_test.go:341 TestTheStoredFormOfAWatchedKeyCarriesThePrintedLabel, cmd/nova-wake/main_test.go:1230 TestAKillAtEachOrderBoundaryReplaysRatherThanLoses, cmd/nova-wake/main_test.go:1046 TestAFailedWriteIsNotADelivery, cmd/nova-wake/advance_test.go:378 TestTheRealBusAdvanceRecoversAnInterruptedRead, cmd/nova-wake/advance_test.go:483 TestTheRecoveryReadsTheCountAndNeverAConstant, cmd/nova-wake/recovery_test.go:141 TestAnUnresolvedRecoveryHoldsTheCursorAndKeepsItsMarker)

Greps run: `grep -rn "WAKE MORE" --include='*.go' .`, `grep -rn "printed=" internal/wake`, `grep -rn "bus:advance=inflight|carrying=|recovered|WAKE BUS" internal/wake`, `grep -rn "WAKE MORE|max-lines|MaxLines|maxLines" .`, `grep -rn "func Test" --include='*_test.go' internal/wake/ cmd/nova-wake/ | grep -i "delivery|printed"`, `grep -rn "shown=|MORE|MarkPrinted|pending=|printed=" cmd/nova-wake`, `grep -rn "red first|red, then green|unprinted|two WAKE ENTRY" --include='*_test.go' .`. Files read: internal/wake/source.go, state.go, advance.go, run.go, internal/bounded/bounded.go, cmd/nova-wake/main.go, main_test.go, advance_test.go, recovery_test.go, events_test.go, internal/wake/state_test.go, docs/SPEC-WAKE.md (The races, the surrounding rules).

Left owed

Verified by run: `GOMAXPROCS=8 go test ./internal/wake/ -count=1 -run 'TestAnObservationIsAppendedBehindAnUnprintedOne|TestTheStoredFormOfAWatchedKeyCarriesThePrintedLabel|TestTheStateFileSaysWhatReachedStdout'` -> ok; `GOMAXPROCS=8 go test ./cmd/nova-wake/ -count=1 -run 'TestDeliveryIsThePrintedLine'` -> ok.

git status --short (in ./repo):