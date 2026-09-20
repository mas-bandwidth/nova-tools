RESULT tools22-rule-wake-13-L2188 sha=5298f6be12ea
CONFORMS cmd/nova-wake/main.go:826
SPEC docs/SPEC-WAKE.md:2188 rule 13
PKG internal/wake
ASK The implementation must ensure every blocking wait between polls is tied to a source's polling schedule (not an arbitrary sleep), and refuse to run if no sources are named.
internal/wake/main.go:826-828:
	if len(entries) == 0 && len(reports) == 0 && *busDir == "" && !forge && len(runs) == 0 && len(locks) == 0 {
		p.add("no source named; refusing to guess", "  "+sourceHint+"\n")
	}

internal/wake/main.go:1286-1298 (rest) and 1304-1319 (until):
func (w *watcher) rest(d time.Duration) {
	if d <= 0 {
		return
	}
	if _, real := w.clock.(wake.Real); real && w.stopCh != nil {
		select {
		case <-w.stopCh:
			return
		case <-time.After(d):
			return
		}
	}
	w.clock.Sleep(d)
}

func (w *watcher) until(now, deadline time.Time) time.Duration {
	next := deadline
	for _, s := range w.sources {
		if s.due.Before(next) {
			next = s.due
		}
	}
	if w.lines != nil && w.lineDue.Before(next) {
		next = w.lineDue
	}
	d := next.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

GUARDED-BY internal/wake/waitbookkeepingspec_test.go:32 TestTheSpecNamesEveryWaitShapeTheCodeSuppresses
GREPS: grep -rn "sleep" --include='*.go' internal/wake/; grep -rn "poll" --include='*.go' internal/wake/; grep -rn "\.Sleep(" --include='*.go' internal/wake/
Left owed: none

git status --short
