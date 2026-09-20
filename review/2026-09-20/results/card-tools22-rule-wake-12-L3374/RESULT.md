RESULT tools22-rule-wake-12-L3374 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 12 says?
CONFORMS cmd/nova-wake/advance_test.go:325
SPEC docs/SPEC-WAKE.md:3374 rule 12
PKG internal/wake
ASK When --advance-cursor is set and unprinted notes are queued, the advance is deferred and prints a note saying so, only advancing after the queue drains.
cmd/nova-wake/main.go:1625-1627:
	if n := wake.BusQueued(w.st); n > 0 {
		w.note(fmt.Sprintf("bus advance deferred: pending=%d bus notes unprinted; nothing fetches until they print", n))
		return false
	}
internal/wake/advance.go:14: // Work list item 3a: the --advance-cursor guard.
GUARDED-BY cmd/nova-wake/advance_test.go:325 TestTheRealBusCursorWaitsBehindThePrint
Grep: grep -rn "WAKE NOTE bus advance deferred" --include='*.go' .
Grep: grep -rn "TestNewMailReachesTheCheckoutThroughTheAdvance" --include='*.go' .
Grep: grep -rn "deferred" --include='*.go' cmd/nova-wake/
Left owed: none
git status --short
