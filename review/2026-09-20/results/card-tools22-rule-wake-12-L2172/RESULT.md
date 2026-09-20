RESULT tools22-rule-wake-12-L2172 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 12 says?
CONFORMS internal/wake/advance.go:227
SPEC docs/SPEC-WAKE.md:2172 rule 12
PKG internal/wake
ASK New mail reaches the checkout exclusively through `nova-bus inbox --advance`, with a derived version pin checked before the opening line, two-poll freshness guarantee behind an empty bus queue, and deferred advances announced by name.

DECIDING LINES:
- advance.go:227 — func (a *Advancer) Advance(): writes the bus:advance marker, runs `nova-bus inbox --advance --remote <name> --branch <name>` (line 242), classifies output, returns
- bus.go:49 — func AcceptBus(tool, found string): string equality check between this build's version and the nova-bus reported version, called before any source polling
- advance.go:268 — func BusQueued(st *State): counts bus:note: queue records; non-zero blocks advance
- advance.go:237 — if Interrupted(st) { return ... ErrRecoveryPending }: recovery outstanding blocks advance
- main.go:902 — case !wake.AcceptBus(Version(), found): refused() called before opening line (opened at 968)
- main.go:1213 — if w.clock.Now().Before(deadline) && w.busRead { if w.advanceOrDefer(ctx, now) }: advance only inside a successful bus poll before deadline
- main.go:1625 — if n := wake.BusQueued(w.st); n > 0: defer message "bus advance deferred: pending=<n> bus notes unprinted; nothing fetches until they print"
- advance.go:271 — for r := range st.Queue() { if strings.HasPrefix(r.Key, "bus:note:") { n++ } }: only bus notes block advancement
- main.go:973 — if *busDir != "" && !*refresh && !*advance: prints WAKE NOTE "bus checkout is read as it stands; nothing fetches without --advance-cursor; freshness is head-at="
- main.go:1728 — fmt.Fprintf(stdout, "WAKE SOURCE bus ... head=%s head-at=%s\n"): structured output shows standing checkout freshness

GUARDED-BY cmd/nova-wake/main_test.go:770 TestNewMailReachesTheCheckoutThroughTheAdvance (without --advance-cursor nothing fetches; head-at= verified on WAKE SOURCE line; version mismatch refuses before opening line; --refresh moves no cursor; --refresh + --advance-cursor exits 2; --advance-cursor without --as exits 2)
GUARDED-BY cmd/nova-wake/advance_test.go:264 TestTheRealBusRelaysNewMailWithinTwoAdvancingPolls (real nova-bus fixture: mail relayed within two polls; cursor moves past printed note; plain inbox sees carrying=1 but no NOTE line; checkout moved only via advance)
GUARDED-BY cmd/nova-wake/advance_test.go:325 TestTheRealBusCursorWaitsBehindThePrint (defer message printed when items queued; cursor stays put; pending= in verdict; advances after queue drains)
GUARDED-BY cmd/nova-wake/advance_test.go:378 TestTheRealBusAdvanceRecoversAnInterruptedRead (kill point recovers consumed notes; marker survives crash; OPEN list listing; carried count matches)
GUARDED-BY cmd/nova-wake/advance_test.go:543 TestTheRealBusAdvanceLockIsExclusiveAndInvisible (one advancing watcher per (bus, as); lock invisible to git checkout; second caller refused)
GUARDED-BY cmd/nova-wake/advance_test.go:574 TestTheAdvanceRunsOnlyInsideABusPollBeforeTheDeadline (advance only during bus poll iterations; not during entry-only or deadline-only iterations)
GUARDED-BY cmd/nova-wake/pin_test.go:25 TestAWakeBuiltAtAVersionRunsAgainstTheBusOfTheSameRelease (derived version pin test)
GUARDED-BY cmd/nova-wake/recovery_test.go:79 TestTheRecoveryCountsEveryCarriedEntryAndNotAMaxOfPartialSums (recovery completeness counting all carried entry types)
GUARDED-BY cmd/nova-wake/recovery_test.go:141 TestAnUnresolvedRecoveryHoldsTheCursorAndKeepsItsMarker (unresolved recovery blocks advance)

GREPS RAN:
grep -rn "inbox.*--advance" --include='*.go' internal/wake/ cmd/nova-wake/
grep -rn "AcceptBus\|func Test" --include='*_test.go' cmd/nova-wake/
grep -rn "head-at=" --include='*.go' internal/wake/ cmd/nova-wake/
grep -rn "\.Advance\(" --include='*.go' internal/wake/ cmd/nova-wake/
grep -rn "BusQueued" --include='*.go' internal/wake/ cmd/nova-wake/
ls internal/wake/

Left owed.
