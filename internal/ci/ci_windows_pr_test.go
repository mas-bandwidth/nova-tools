package ci

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ci_windows_pr_test.go holds the Windows-on-a-pull-request leg shut.
//
// The cost of not having it: Windows ran only in the merge group
// (test-hosted-merge) and on push, so a Windows-only failure — an assertion on
// the execute bit, a backslash where the test expected "a/b", a fake binary
// written without the .exe suffix — was found after a PR was enqueued, where a
// red shard drops the whole group and restarts every PR behind it. One PR's
// small mistake became every PR's delay.
//
// The leg that fixes that is only worth its hosted minutes if it stays cheap, so
// the shape is the contract and this test is the shape: THREE shards on
// windows-latest, dealt from the measured sizes in
// testdata/ci/package-sizes-windows.tsv, over the packages
// .github/scripts/select-packages.sh selects — the same script the self-hosted
// shards call, so there is one answer to "what does this change test" — skipped
// when the PR moves no Go file, guarded against fork code, and aggregated by
// ci-ok. Read as text, like the rest of this package: go.mod carries no YAML
// library.
func TestPullRequestsGetAWindowsLeg(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	job := jobBody(src, "test-windows-pr")
	if job == "" {
		t.Fatal("no test-windows-pr job in ci.yml: a pull request gets no Windows leg, so a Windows-only break is found in the merge group, where it drops the whole group")
	}

	if !strings.Contains(job, "runs-on: windows-latest") {
		t.Error("test-windows-pr does not run on windows-latest; there is no self-hosted Windows runner, so the leg has nowhere else to go")
	}
	// The matrix is the shard list and NOTHING else. Every slot is billed
	// Windows minutes, so a second dimension — an OS, a Go version — is a
	// multiplication of the bill and is refused here rather than reviewed later.
	if !strings.Contains(job, "shard: [0, 1, 2, 3]") {
		t.Error("test-windows-pr does not deal its work over the four shards `shard: [0, 1, 2, 3]`; the largest measured package, cmd/nova-merge at 203.9 s of -short Windows time, is 68 s a shard over three slots and 51 s over four, and 60 s is the budget")
	}
	if strings.Contains(job, "os:") || strings.Contains(job, "go-version: [") {
		t.Error("test-windows-pr's matrix has a second dimension; the shard list is the whole matrix, because every extra slot is billed Windows minutes")
	}
	if !strings.Contains(job, windowsSizesPath) {
		t.Errorf("test-windows-pr does not read %s; the shard count per package must be DERIVED from the measurements, not guessed", windowsSizesPath)
	}
	if !strings.Contains(job, "github.event_name == 'pull_request'") {
		t.Error("test-windows-pr is not guarded to pull_request; the merge group and the push already run Windows, and paying twice is the one thing this leg must not do")
	}
	if !strings.Contains(job, "github.event.pull_request.head.repo.full_name == github.repository") {
		t.Error("test-windows-pr has no head-repo guard; a fork PR would run untrusted code on hosted compute")
	}
	if !strings.Contains(job, ".github/scripts/select-packages.sh") {
		t.Error("test-windows-pr does not select its packages with .github/scripts/select-packages.sh; a second answer to `what does this change test` is a second thing to keep in step with the shards")
	}
	if !strings.Contains(job, "'**/*.go'") {
		t.Error("test-windows-pr has no path filter on **/*.go; a docs or lisp PR would pay for a Windows runner that has nothing to test")
	}

	// The aggregate must see it: a leg no required check reads is a leg whose
	// red nobody has to fix.
	i := strings.Index(src, "\n  ci-ok:")
	if i < 0 {
		t.Fatal("no ci-ok job in ci.yml")
	}
	ciok := src[i:]
	if !strings.Contains(ciok, "test-windows-pr") {
		t.Error("ci-ok does not aggregate test-windows-pr; the only required check would be green over a red Windows leg")
	}
	if !strings.Contains(ciok, "needs.test-windows-pr.result") {
		t.Error("ci-ok's verdict steps do not read needs.test-windows-pr.result; listing a job in `needs` alone does not make its red a red verdict")
	}
}

// windowsSizesPath is the measured windows-latest size of each package, the
// table the Windows PR leg derives its shard count from. Its sibling
// testdata/ci/package-sizes.tsv records the idle-Linux size; the two are
// different measurements and neither predicts the other.
const windowsSizesPath = "testdata/ci/package-sizes-windows.tsv"

// windowsPRShards is the number of slots test-windows-pr deals its work over,
// and it is arithmetic: the largest measured package, cmd/nova-merge at 203.9 s
// of -short Windows time, is 68 s a shard over three and 51 s over four, and the
// budget below is 60.
const windowsPRShards = 4.0

// windowsShardBudget is the seconds of Windows work a shard should carry, and it
// is the number ci.yml's shard plan compares each measurement against: at or
// over it a package's tests are dealt across the three slots, under it the
// package runs whole in one.
const windowsShardBudget = 60.0

// windowsSize is one row's reading of a size column: the number, and whether the
// run that produced it was CENSORED (it hit its timeout, so the true size is at
// least this) or absent altogether. Both plans deal a censored or missing size
// across every slot the group opened, because an unknown size must never be
// guessed downward — that is the mistake that dropped integration-4's group.
type windowsSize struct {
	secs     float64
	measured bool
	censored bool
}

// readWindowsSizes parses testdata/ci/package-sizes-windows.tsv into its two
// columns, keyed by import path, reporting every malformed row. It is the same
// reading ci.yml's two shard plans do in awk, so a row that would confuse them
// is a red run here first.
func readWindowsSizes(t *testing.T, root string) (short, full map[string]windowsSize) {
	t.Helper()
	raw := readFile(t, filepath.Join(root, "testdata", "ci", "package-sizes-windows.tsv"))
	short, full = map[string]windowsSize{}, map[string]windowsSize{}
	read := func(n int, field string) (windowsSize, bool) {
		if field == "-" {
			return windowsSize{}, true
		}
		censored := strings.HasSuffix(field, "+")
		secs, err := strconv.ParseFloat(strings.TrimSuffix(field, "+"), 64)
		if err != nil {
			t.Errorf("%s:%d: %q is neither a number of seconds, a censored number ending in +, nor - for unmeasured: %v", windowsSizesPath, n+1, field, err)
			return windowsSize{}, false
		}
		return windowsSize{secs: secs, measured: true, censored: censored}, true
	}
	for n, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Errorf("%s:%d: want exactly three tab-separated fields (import path, short, full), got %d: %q", windowsSizesPath, n+1, len(fields), line)
			continue
		}
		pkg := fields[0]
		if !strings.HasPrefix(pkg, modulePath) {
			t.Errorf("%s:%d: %q is not an import path in this module; the table is keyed the way `go list` prints, like its Linux sibling", windowsSizesPath, n+1, pkg)
			continue
		}
		if _, dup := short[pkg]; dup {
			t.Errorf("%s:%d: %s is measured twice; the plans read the first row and the second is a silent lie", windowsSizesPath, n+1, pkg)
		}
		// A measured package must still exist. A row for a package that has
		// left is a row the plans will never read and nobody will ever correct.
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath)))
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s:%d: %s is in the table but not in the tree; delete the row", windowsSizesPath, n+1, pkg)
		}
		if v, ok := read(n, fields[1]); ok {
			short[pkg] = v
		}
		if v, ok := read(n, fields[2]); ok {
			full[pkg] = v
		}
	}
	return short, full
}

// TestWindowsPRShardPlanIsDerivedFromMeasurements keeps the three numbers that
// decide this leg's cost — the measurements, the shard budget, and the
// per-package timeout — in one place and in step with each other.
//
// The leg shipped with the hosted convention of 100 s per package. Its first
// real run (#1332) found four packages ON that ceiling under -short: 100.0 and
// 100.1 are not sizes but the timeout, so their true sizes are unknown and at
// least that. A ceiling a tree's packages sit on is not naming a hang, it IS the
// hang, so the ceiling became the measured WINDOWS_TIMEOUT in the Makefile. This
// test holds that it can never drop back below a size actually observed.
func TestWindowsPRShardPlanIsDerivedFromMeasurements(t *testing.T) {
	root := repoRoot(t)
	short, _ := readWindowsSizes(t, root)
	if len(short) == 0 {
		t.Fatalf("%s carries no measurements; the shard plan would guess at every package", windowsSizesPath)
	}
	largest := 0.0
	for _, v := range short {
		if v.measured && v.secs > largest {
			largest = v.secs
		}
	}

	// The four packages #1332 measured at the ceiling are the reason the leg is
	// sharded at all. If they ever fall out of the table the plan silently stops
	// dealing them, so they are named here.
	for _, pkg := range windowsForcingPackages {
		v, ok := short[modulePath+pkg]
		if !ok || !v.measured {
			t.Errorf("%s has no -short row for %s, the package whose Windows size (#1332) is the reason this leg is sharded", windowsSizesPath, pkg)
			continue
		}
		if !v.censored && v.secs < windowsShardBudget {
			t.Errorf("%s says %s is %.1fs, under the %.0fs shard budget, so the plan would run it whole; run 35352593117 measured it over the budget on windows-latest under -short. If a real re-measurement put it under, take it off windowsForcingPackages in the same edit", windowsSizesPath, pkg, v.secs, windowsShardBudget)
		}
	}

	// The per-package ceiling lives in the Makefile, where `make test-pr` and the
	// windows merge leg both read it. It bounds ONE `go test` invocation, which
	// is one shard's share of a dealt package or the whole of a package that
	// runs in one slot — not the whole of a dealt package, which no single
	// invocation ever runs.
	//
	// The margin is not decoration. Tests are dealt by NAME INDEX, not by time,
	// so the shares come out uneven: run 35352593117 dealt cmd/nova-bus as
	// 40.7 + 46.5 + 98.7, a worst shard 1.6x the mean. Twice the largest share
	// is the room that unevenness needs, and a ceiling under it would be the
	// 100 s mistake again.
	largestShare := 0.0
	for _, v := range short {
		if !v.measured {
			continue
		}
		share := v.secs
		if v.censored || share >= windowsShardBudget {
			share = v.secs / windowsPRShards
		}
		if share > largestShare {
			largestShare = share
		}
	}
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	raw, ok := mk.vars["WINDOWS_TIMEOUT"]
	if !ok {
		t.Fatal("the Makefile declares no WINDOWS_TIMEOUT; the Windows per-package ceiling has nowhere to live but a workflow line nobody can run")
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("WINDOWS_TIMEOUT = %q is not a Go duration: %v", raw, err)
	}
	if d.Seconds() < 2*largestShare {
		t.Errorf("WINDOWS_TIMEOUT = %s, under twice the largest per-invocation share the plan can hand one `go test` (%.1fs of %.1fs, from %s); the dealing is by test name and comes out as uneven as 40.7/46.5/98.7, so a ceiling without that room is the 100 s mistake again", d, largestShare, largest, windowsSizesPath)
	}
	if !strings.Contains(readFile(t, filepath.Join(root, "Makefile")), "#1332") {
		t.Error("the Makefile does not say where WINDOWS_TIMEOUT's number comes from; a ceiling is a claim about the machine and belongs in the repository with its measurement")
	}
}

// TestMergeGateWindowsLegDealsFromTheWindowsTable is integration-4's lesson, held
// shut. The merge group's windows leg dealt its shards from the LINUX table:
// cmd/nova-bus at 6.4 s bought three shards, and shard 0 was killed at the 100 s
// ceiling with three tests still running, so one third of that package is over
// 100 s on windows-latest. Linux could not have said so — cmd/nova-bus is 10.0 s
// on hulk and at least 100 s there, cmd/nova-swarm 51.0 s on hulk and 37 s
// there — so the windows leg reads the Windows table, and its ceiling is the
// Makefile's WINDOWS_TIMEOUT rather than the linux and darwin 100 s.
func TestMergeGateWindowsLegDealsFromTheWindowsTable(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	step := stepBody(src, "test (full, the packages this group changes, shard ${{ matrix.shard }} of ${{ needs.plan-merge.outputs.slots }})")
	if strings.TrimSpace(step) == "" {
		t.Fatal("no merge-gate test step in ci.yml; the shard plan moved and this test is looking in the wrong place")
	}
	if !strings.Contains(step, windowsSizesPath) {
		t.Errorf("the merge gate's shard plan never reads %s; its windows leg would deal from the Linux column again, which is how integration-4's group was dropped", windowsSizesPath)
	}
	// The leg must be told apart by name, and the Windows branch must read the
	// FULL column ($3) rather than the -short one the PR leg uses.
	if !strings.Contains(step, `leg=${{ matrix.leg.name }}`) || !strings.Contains(step, `[ "$leg" = "windows" ]`) {
		t.Error("the merge gate's shard plan does not branch on the windows leg; linux and darwin must keep the Linux table, which is their measurement")
	}
	if !strings.Contains(step, "print $3") {
		t.Error("the merge gate's windows branch does not read the FULL column of the Windows table; the merge group runs without -short, and the -short column is a different measurement")
	}
	// Censored and unmeasured both get every slot the group opened.
	for _, want := range []string{"''|'-')", "*+)"} {
		if !strings.Contains(step, want) {
			t.Errorf("the merge gate's windows branch does not handle %s (unmeasured or censored); an unknown Windows size must be dealt across every slot, never guessed downward", want)
		}
	}
	if !strings.Contains(step, "make -s windows-timeout") {
		t.Error("the merge gate's windows leg does not take its ceiling from `make -s windows-timeout`; the Windows number would be written twice and drift")
	}
	if !strings.Contains(step, "MERGE_TIMEOUT") {
		t.Error("the merge gate's windows leg does not export MERGE_TIMEOUT; the Makefile's `?=` is what lets the Windows ceiling win over the linux and darwin default")
	}

	// And the Makefile end of that handshake: a target that prints the number,
	// and a merge target that reads MERGE_TIMEOUT rather than a literal.
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	if got := strings.Join(mk.recipeFor("windows-timeout"), "\n"); !strings.Contains(got, "echo 180s") {
		t.Errorf("`make windows-timeout` does not print the Windows ceiling, it runs %q; the workflow reads the number from here", got)
	}
	if got := strings.Join(mk.recipeFor("test-merge"), "\n"); !strings.Contains(got, "-timeout 100s") {
		t.Errorf("make test-merge does not carry the 100 s linux and darwin ceiling: %q", got)
	}
	if _, ok := mk.vars["MERGE_TIMEOUT"]; !ok {
		t.Error("the Makefile declares no MERGE_TIMEOUT; the windows leg has no variable to override")
	}
}

// windowsForcingPackages are the packages whose MEASURED -short Windows size is
// over the shard budget, so the plan must deal them across every slot. Naming
// them here means a table that quietly loses one is a red run.
//
// cmd/nova-review used to be on this list and is not any more, and that is the
// point of measuring rather than censoring: its row read 100.1+ because the leg
// killed it at a 100 s ceiling, and the real number is 38.6 s. It runs whole in
// one slot now. cmd/nova-merge went the other way — 100.0+ turned out to be
// 203.9 s — and internal/bus, never measured at all, turned out to be 63.3 s and
// joined the list. A censored row is not a conservative estimate; it is no
// estimate.
var windowsForcingPackages = []string{"cmd/nova-bus", "cmd/nova-merge", "cmd/nova-wake", "internal/bus"}

// modulePath is this module, the prefix both size tables are keyed by.
const modulePath = "github.com/mas-bandwidth/nova-tools/"

// TestOnlyOneWindowsLegRunsOnAPullRequest is the consolidation, held shut.
//
// For one day a pull request ran TWO Windows legs: this file's sharded
// test-windows-pr, and test-hosted-pr's older windows entry, which ran a fixed
// package list unsharded behind a path filter. integration-4 ran both on the
// same commit: the three sharded shards passed at 13:37-13:43Z, and the
// unsharded leg was cancelled by its own six-minute timeout at 13:43:39 and
// failed the run. A second leg that can only fail is not redundancy, it is a
// second thing to fix.
//
// So the windows entry was retired and its rule folded in here. The retirement
// is only correct if NOTHING it covered was lost, and that is what this test
// checks: the two packages it ran and the three paths it watched are both
// present in test-windows-pr, and no other pull_request job reaches Windows.
// The paths matter on their own — a change to a fixture under cmd/nova-bus
// moves no .go file, so select-packages.sh cannot see it, and without the
// `platform` filter that change would take the Windows runner away from exactly
// the package that needs it.
func TestOnlyOneWindowsLegRunsOnAPullRequest(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	// Exactly one job runs on a pull request and reaches windows-latest.
	var onWindows []string
	for _, name := range jobNames(src) {
		job := jobBody(src, name)
		if strings.Contains(job, "windows-latest") && strings.Contains(job, "github.event_name == 'pull_request'") {
			onWindows = append(onWindows, name)
		}
	}
	if len(onWindows) != 1 || onWindows[0] != "test-windows-pr" {
		t.Errorf("the pull_request jobs that reach windows-latest are %v, want exactly [test-windows-pr]; two Windows legs on one PR is a second thing to fix and integration-4 is what it costs", onWindows)
	}

	hosted := jobBody(src, "test-hosted-pr")
	if hosted == "" {
		t.Fatal("no test-hosted-pr job in ci.yml; the Linux sandbox leg is not part of this consolidation and must stay")
	}
	if strings.Contains(hosted, "windows-latest") {
		t.Error("test-hosted-pr still has a windows leg; it was retired into test-windows-pr on 2026-09-18 after being cancelled by its own timeout on a commit the sharded leg passed")
	}
	if !strings.Contains(hosted, "./internal/sandbox") {
		t.Error("test-hosted-pr no longer runs ./internal/sandbox; the consolidation retires the WINDOWS entry only — the sandbox is darwin-only in the self-hosted matrix, so its Linux leg has to stay")
	}

	// Nothing the retired leg covered may be lost: its trigger and its packages
	// both live in test-windows-pr now.
	windows := jobBody(src, "test-windows-pr")
	for _, path := range retiredWindowsLegPaths {
		if !strings.Contains(windows, path) {
			t.Errorf("test-windows-pr's filters do not carry %s, which the retired windows leg watched; a change under it that moves no .go file would now get no Windows runner at all", path)
		}
	}
	for _, pkg := range retiredWindowsLegPackages {
		if !strings.Contains(windows, pkg) {
			t.Errorf("test-windows-pr never adds %s, which the retired windows leg ran; select-packages.sh cannot select it from a change that moves no .go file", pkg)
		}
	}
	if !strings.Contains(windows, "steps.filter.outputs.platform") {
		t.Error("test-windows-pr does not read the `platform` filter output; the retired leg's trigger would be declared and never used")
	}
}

// retiredWindowsLegPaths and retiredWindowsLegPackages are what test-hosted-pr's
// windows entry watched and ran before it was retired on 2026-09-18. They are
// written down here so the retirement can be checked for loss rather than
// trusted: every one of them must still reach a Windows runner through
// test-windows-pr.
var (
	retiredWindowsLegPaths    = []string{"'cmd/nova-bus/**'", "'internal/bus/**'", "'**/*_windows.go'"}
	retiredWindowsLegPackages = []string{"./cmd/nova-bus", "./internal/bus"}
)
