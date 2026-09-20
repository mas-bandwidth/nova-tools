RESULT tools22-rule-wake-11-L4117 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 11 says?
CONFORMS internal/wake/line.go:45 cmd/nova-wake/main.go:968
SPEC docs/SPEC-WAKE.md:4117 rule 11
PKG internal/wake
ASK Every part of rule 11 must be present: line.go implements --line as a git-log-over-the-bus view yielding per-name last-sign with sha|state|stamp output and exactly-one OFFLINE/BACK transitions; main.go echoes --on-deadline on the opening line and verdict, wires --entry-interval with per-source due times, records the pid in <state>.lock, prints WAKE SOURCE counts per forge, and fires WAKE BROKEN on three consecutive failures.

deciding lines:
internal/wake/line.go:45-110 func (l *Lines) Poll(ctx context.Context, now time.Time) (Result, error): runs `git -C l.Bus log --format=%H%x1f%an%x1f%cI` under the timeout (line 46-52), collects last sign per name (line 65-75), produces `<sha>|<state>|<stamp>` via Compose(sha, state, stamp) (line 107), reports OFFLINE when silence exceeds After (line 89-90) and BACK otherwise (line 88), emits each state once (lines 92-103).

cmd/nova-wake/main.go:968-970 fmt.Fprintf(stdout, "WAKE at=%s ... on-deadline=%s ..."): --on-deadline echoed on the opening line.

cmd/nova-wake/main.go:1259-1260 fmt.Fprintf(w.stdout, "WAKE QUIET after=%s polls=%d default=%s ..."): --on-deadline (as "default=%s") echoed on the quiet verdict.

cmd/nova-wake/main.go:730-732 entryEveryDur = parseDur(&p, "entry-interval", *entryEvery): parses --entry-interval.

cmd/nova-wake/main.go:919 sources appended with due: now / due: next call based on entryEveryDur.

cmd/nova-wake/main.go:1091 lineDue time.Time: per-source due time tracking for lines.

internal/wake/state.go:185-187 LockName(path string) string { return path + ".lock" }: the <state>.lock file path helper.

internal/wake/lock.go:25-26 "The lock file holds the pid of the run that took it": pid recorded in the lock file.

cmd/nova-wake/main.go:1724-1755 fmt.Fprintf(..., "WAKE SOURCE bus read=%d ..."), fmt.Fprintf(..., "WAKE SOURCE prs read=%d ..."): WAKE SOURCE counts printed per source type.

cmd/nova-wake/main.go:1228 if n, _, _ := w.st.Streak("bus"); n >= 3 { broken = "bus" } + line 1237 "WAKE BROKEN source=%s failures=%d since=%s": three-in-a-row streak triggers WAKE BROKEN.

GUARDED-BY cmd/nova-wake/lessons_test.go:130 TestTenMinutesSilentIsOfflineOnce guards OFFLINE once and BACK once for line view.
GUARDED-BY cmd/nova-wake/main_test.go:608 TestBecomesBrokenAfterThreeConsecutiveFailures guards three-in-a-row WAKE BROKEN verdict.
GUARDED-BY cmd/nova-wake/main_test.go:707 TestTheToolStampsAndATypedTimeIsData guards opening line includes formatted content.
GUARDED-BY internal/wake/state_test.go:292 TestALinesLastSignIsNotCappedByACommitCount guards against git log commit cap in line.go.
GUARDED-BY cmd/nova-wake/main_test.go:507 TestSourceLinePrintedBeforeAnyWaiting guards WAKE SOURCE count output.
GUARDED-BY cmd/nova-wake/events_test.go:98 TestEntryIntervalHasAFloor guards --entry-interval parsing and floor enforcement.

greps run:
grep -rn "<sha>|<state>|<stamp>" --include='*.go' .
grep -rn "\"line\"" --include='*.go' . | head -20
grep -rn "\"on-deadline\"" --include='*.go' . | head -20
grep -rn "\"entry-interval\"" --include='*.go' . | head -20
grep -rn "\"WAKE SOURCE\"" --include='*.go' .
grep -rn "\"WAKE BROKEN\"" --include='*.go' .
grep -rn "\.lock" --include='*.go' internal/wake/ | head -20
grep -rn "pid\|os.Getpid\|Getpid" --include='*.go' internal/wake/ | head -20
grep -rn "func Test" --include='*_test.go' internal/wake/ | head -30
grep -rn "due time.Time" --include='*.go' cmd/nova-wake/
ls internal/wake/

Left owed

	git status --short
