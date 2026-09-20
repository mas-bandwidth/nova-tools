RESULT tools22-pre-2145-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2145 at head 0a7578201778: pulse: provisioning tracks go.mod's go directive (#1500)
PREREAD 2145 claims=7 proven=6 unproven=1 defects=2 high=0

PR 2145
HEAD 0a7578201778bf0ad9222d7fa46d57a40f42318a
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 6 production, 3 test
LINES +310 -17

CLAIMS
1. `fleet standard` with an empty --go holds a bench's Go toolchain to go.mod's `go` directive instead of a hardcoded go1.26.5.
   PROVEN-BY internal/pulse/fleetstandard_go_test.go:39 TestFleetStandardChecksEmptyGoWantReadsGoMod — asserts the go check's Want equals go.mod's go line for linux, darwin and windows.
2. An explicit --go is never rewritten from go.mod; it stays the override.
   PROVEN-BY internal/pulse/fleetstandard_go_test.go:30 TestFleetStandardChecksGoWantOverride — passes go1.22.0 and asserts the go check's Want is exactly that.
3. A bench running a Go older than go.mod's go line now DRIFTs with exit 2 instead of reading as conforming.
   PROVEN-BY internal/pulse/fleetstandard_go_test.go:80 TestFleetStandardEmptyGoWantDriftsWhenGoIsBehindGoMod — a go1.26.5 bench against a go1.26.6 tree prints `go DRIFT` and exits 2.
4. tools/bench-standard.sh derives NOVA_GO from its checkout's go.mod when NOVA_GO is unset, rather than a hardcoded default.
   PROVEN-BY internal/ci/benchstandard_go_test.go:21 TestBenchStandardGoWantTracksGoMod — runs the script with a fake go1.26.5 `go` on PATH and asserts the go DRIFT line names go.mod's version.
5. `fleet survey` pins NOVA_GO from the running tree's go.mod onto the piped script, because a script piped through `bash -s` cannot read go.mod itself.
   PROVEN-BY cmd/nova-pulse/fleet_survey_test.go:211 TestFleetSurveySendsTheStandardScriptToEveryBench — asserts each bench is sent exactly `NOVA_GO=<go.mod line>` + the whole script.
6. No hardcoded go1.X.Y default remains in fleetstandard.go or bench-standard.sh.
   PROVEN-BY internal/pulse/fleetstandard_go_test.go:62 TestFleetStandardDefaultGoWantIsNotAHardcodedPatch — a source-scan guard refusing the literals `goWant = "go1.` and `NOVA_GO="${NOVA_GO:-go1.`.
7. When go.mod cannot be read, the fleet standard fails closed (drifts) rather than passing.
   UNPROVEN — GoDirective's empty input is covered by TestGoDirectiveReadsTheGoLine, but no test drives resolveGoWant's "go.mod-unread" sentinel: every test runs inside the checkout, so GoWantFromTree always succeeds. The claim rests on the sentinel being chosen to never appear in `go version` output, which no test asserts.

DEFECTS
DEFECT medium internal/pulse/fleetstandard.go:105 — an unreadable tree makes the go check's Want the sentinel "go.mod-unread", so `fleet standard` with no --go run from a non-checkout drifts every bench with `want=contains:go.mod-unread`, a token naming no remedy — this silently changes the empty--go default for any operator running outside a checkout (previously the go1.26.5 literal), and the line breaks the repo's refusal-names-the-remedy convention. Fix: refuse with a remedy (`--go` or a checkout) instead of emitting the sentinel in the STANDARD output.
DEFECT low docs/BENCH-WINDOWS.md:37,66 — the Windows bench standard still documents `go1.26.5` as the default go want (`Version matches repository go.mod (default go1.26.5)` and `contains:go1.26.5`), while the windows go check now takes its Want from go.mod's go line via resolveGoWant — a doc sentence the code now contradicts. Fix: name go.mod's go line with --go as the override, matching SPEC-PULSE.md.

QUESTIONS
1. `fleet survey` pins NOVA_GO from the coordinator's current checkout and holds every bench to it. When the fleet is deliberately being walked up a toolchain version (bump go.mod, then upgrade benches), the whole fleet goes red at once — is that the intended pacing, or is a survey-time override wanted?
2. resolveGoWant's sentinel "go.mod-unread" is a deliberate fail-closed, but was a refusal-with-remedy (the repo's convention) considered for the empty--go-from-outside-a-checkout case, or is that case genuinely expected to never happen?
3. The drift tests in internal/pulse and internal/ci couple to the fake SDK being go1.26.5 exactly one patch behind the tree; if go.mod ever lands on exactly `go 1.26.5`, TestFleetStandardEmptyGoWantDriftsWhenGoIsBehindGoMod fails. Is that coupling intended to hold (go.mod should always stay ahead of the benches), or is it a latent fragility?

Left owed — read in full: internal/pulse/fleetstandard.go, internal/pulse/fleetstandard_go_test.go, internal/ci/benchstandard_go_test.go, cmd/nova-pulse/fleet_survey_test.go, cmd/nova-pulse/fleet.go (survey command and readBenchStandard hunks plus surrounding context), tools/bench-standard.sh, go.mod, and the three doc diffs. Not read: the untouched remainder of cmd/nova-pulse/fleet.go (surveyOneBench onward, unchanged by this diff) and the full docs files beyond the changed hunks. No tests were run (none required by the card); `go build ./internal/pulse ./cmd/nova-pulse ./internal/ci` passes.

git status --short: (empty — clean)
git rev-parse HEAD: 0a7578201778bf0ad9222d7fa46d57a40f42318a===FILE=== card-tools22-pre-2145-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2145-r1	1	2026-09-20T19:28:14Z	2026-09-20T19:41:04Z	0	opencode	deepseek-v4-flash	66580	30390	0	2017792	0	0.0743
