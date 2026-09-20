RESULT tools22-pre-930-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#930 at head 8aff35343d82: CARD-8473 nova-tools #509 fixed with its red test first: SPEC-PULSE: handoff and takeover verbs
PREREAD 930 claims=10 proven=10 unproven=0 defects=2 high=0

## HEAD 8aff35343d827b4524b8ab02f196f334de763203
## BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
## MERGE-BASE 0f0e46c0e26bfbab7fc84c0dfead4f551fb1c503
## BEHIND 21
## FILES 4 production, 2 test
## LINES +889 -11

---

## PR 930

Three commits above merge-base `0f0e46c`: `5a80d0b` (add handoff/takeover verbs), `4c18705` (merge dev, resolve conflict), `8aff353` (merge remote-tracking branch). Ten files changed: two new production files (`cmd/nova-pulse/handoff.go`, `internal/pulse/handoff.go`), two modified production files (`cmd/nova-pulse/main.go`, `internal/pulse/cli.go`), one modified test file (`internal/pulse/fake_test.go`), one new test file (`internal/pulse/handoff_test.go`, 359 lines), and four documentation updates.

### CLAIMS

1. **handoff ends the duty shift** — prints `SHIFT END` and `HANDOFF OK` on stdout, removes queue/OWNER, removes queue/HARVEST.LOCK read-guard, rewrites LOOP as stopped, writes queue/HANDOFF record, posts one bus note to the successor. PROVEN-BY internal/pulse/handoff_test.go:126 TestHandoffWritesRecordAndNote — asserts SHIFT END / HANDOFF OK in stdout, OWNER removed, HANDOFF carries to/from/width/bench counts, LOOP stopped, argv contains nova-wake awake and nova-bus send.

2. **handoff refuses when the successor is asleep** — calls `nova-wake awake --bus <clone>`, checks for `FRIEND <name> awake`; any other answer exits 2, prints HANDOFF REFUSED on stderr, leaves OWNER unchanged, writes no HANDOFF record, sends no bus note. PROVEN-BY internal/pulse/handoff_test.go:173 TestHandoffRefusesAsleepSuccessor — two subtests (non-zero exit, asleep FRIEND line), asserts exit 2, HANDOFF REFUSED in stderr, OWNER unchanged, no HANDOFF written, no nova-bus send in argv.

3. **handoff refuses mid-harvest** — detects queue/HARVEST.LOCK, exits 2 with HANDOFF REFUSED naming harvest, OWNER and HARVEST.LOCK untouched. PROVEN-BY internal/pulse/handoff_test.go:212 TestHandoffFinishesHarvestFirst — writes HARVEST.LOCK, asserts refusal with harvest mention, HARVEST.LOCK still present, OWNER unchanged, no HANDOFF record.

4. **takeover refuses live owner** — reads queue/OWNER, checks host reachability via `os.Hostname()` and process liveness via `swarm.Alive(pid)`; if both pass, exits 2 with TAKEOVER REFUSED, leaves OWNER untouched. PROVEN-BY internal/pulse/handoff_test.go:240 TestTakeoverRefusesLiveOwner — writes OWNER with live pid, asserts TAKEOVER REFUSED in stderr with owner/pid/host, OWNER unchanged, exit 2.

5. **takeover takes stale lock with NOTE** — if OWNER names dead process (swarm.Alive returns false) or unreachable host (hostname mismatch), writes new OWNER, rewrites LOOP as running, prints ONE NOTE line + TAKEOVER OK on stdout. PROVEN-BY internal/pulse/handoff_test.go:257 TestTakeoverTakesStaleLockWithNote — two subtests (dead process pid=2147483647, unreachable host "bench-that-never-was"), asserts exactly one NOTE line, TAKEOVER OK, OWNER rewritten with caller name, LOOP running.

6. **takeover inherits HANDOFF record counts** — parses the eight-tab-field HANDOFF record, extracts inflight/pending/escalations, prints `TAKEOVER OK from=<prev_owner> inherited=<inflight>/<pending>/<escalations>`. PROVEN-BY internal/pulse/handoff_test.go:308 TestTakeoverInheritsQueue — performs handoff then takeover, asserts TAKEOVER OK from=Rowan inherited=1/2/1 matching the queued card counts.

7. **handoff moves work ownership** — when --work is provided, bumps generation in work/OWNER, draws fresh random token via `crypto/rand`, overwrites work/OWNER, appends `:handoff` event to work/EVENTS. PROVEN-BY internal/pulse/handoff_test.go:319 TestHandoffMovesWorkOwnership — sets work/OWNER at generation=3, asserts generation=4, name=Stella, fresh token, :handoff event in EVENTS.

8. **handoff command-line flags** — `--queue`, `--to` (required), `--bus`, `--roots`, `--as`, `--work` (optional), `--max` (default 20); both entry points (main.go/cmdHandoff and cli.go/handoffVerb) parse these identically. PROVEN-BY-EXISTING cmd/nova-pulse/handoff.go:14 cmdHandoff AND internal/pulse/cli.go:229 handoffVerb — each defines the same seven flags with same defaults and required-ness validation, calling `pulse.Handoff(pulse.HandoffInput{...})`.

9. **takeover command-line flags** — `--queue`, `--as` (required), `--bus`, `--roots` (symmetry-only), `--max` (default 20); both entry points parse these identically. PROVEN-BY-EXISTING cmd/nova-pulse/handoff.go:38 cmdTakeover AND internal/pulse/cli.go:263 takeoverVerb — each defines the five flags, calls `pulse.Takeover(pulse.TakeoverInput{...})`.

10. **help text updated** — pulseVerbs string, run() help block, docs/SPEC-PULSE.md, docs/spec-pulse/04-the-verbs.md, docs/spec-pulse/05-handoff.md all consistently list handoff and takeover in the shipped verbs block and remove them from the "behind" list. PROVEN-BY-EXISTING multiple doc files AND cmd/nova-pulse/main.go:33, internal/pulse/cli.go:19 — handoff and takeover appear in every help block; open-questions docs delete them from the gaps paragraph.

---

### DEFECTS

**DEFECT medium internal/pulse/cli.go:50 dual CLI implementation without cross-layer coverage — handoff and takeover have two independent CLI layers (main.go's run()/cmdHandoff and cli.go's Main()/handoffVerb), each with its own flag struct, error formatting style (flags.problems vs refusal()), and usage message; the seven new tests exercise only the internal Handoff/Takeover functions, never dispatching through either CLI entry point, leaving the routing logic and error-format parity completely untested.** — If one entry point gains a new flag or error path and the other does not, callers will see inconsistent behaviour depending on how they invoke the binary (via `main()` directly or via `Main()` as a library). A test that walks `run(args)` end-to-end or `Main(args)` end-to-end covering handoff/takeover would catch divergence.

**DEFECT low cmd/nova-pulse/handoff.go:1 handoff.go comment says "handoff and takeover verbs" but handoff.go only defines `cmdHandoff` and `cmdTakeover`; the companion file `internal/pulse/handoff.go` (new, 368 lines) holds `Handoff` and `Takeover` — the comment belongs on internal/pulse/handoff.go, not cmd/nova-pulse/handoff.go which is purely a flag-shim.** — Misleading comment location; a reader skimming cmd/nova-pulse/handoff.go looking for the Handoff/Takeover implementations will find none. Moving the comment block to internal/pulse/handoff.go where the real implementations live resolves this.

---

### QUESTIONS FOR THE REVIEWER

1. Why do handoff/takeover get dual CLI implementations? `main.go`'s `run()` routes to `cmdHandoff`/`cmdTakeover` and `cli.go`'s `Main()` routes to `handoffVerb`/`takeoverVerb`, both calling identical `pulse.Handoff`/`pulse.Takeover`. Is this intentional (e.g. `Main()` for test harnesses, `run()` for the binary), or did a merge conflict during commit `4c18705e` leave residue?

2. The `--max` flag is documented in all three help blocks (pulseVerbs, main.go help, run() switch block) but I could not find any code that actually uses it beyond passing it through to HandoffInput/TakeoverInput. Does the core implementation cap anything, or is `--max` a future-use scaffold?

3. In `moveWorkOwnership`, generation is parsed by scanning all tokens on every line of work/OWNER rather than reading a structured field. If the OWNER format ever changes (e.g. additional fields on the same line), could generation be mis-parsed? Is there a canonical OWNER parser elsewhere that should be shared?

4. The `hostReachable` variable is a global package-level func defaulting to `os.Hostname()` comparison but has no injection point for tests. The takeover tests rely on the real hostname and real OS. How confident am I that the "unreachable host" test path works reliably in CI environments where the hostname might not match expectations?

5. After handoff, the HANDOFF NOTE bus send failure is reported on stderr but the command still exits 0 and still writes HANDOFF/LOOP/OOWNER. Is this the intended semantics — that a bus failure after a successful handoff does not roll back the handoff — or should the bus failure escalate to a non-zero exit?

---

Left owed: I did not read `internal/pulse/fake_test.go` fully (only its single-line change adding "nova-wake" to fakeTools), nor did I read `internal/pulse/run.go` (which defines busNotifier used by handoff.go) beyond confirming the Note() method signature matches the call site. These are pre-existing files whose content I assumed correct based on the diff context showing no errors from go vet/build.

git status --short
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
