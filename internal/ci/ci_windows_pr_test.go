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
	if !strings.Contains(job, "shard: [0, 1, 2]") {
		t.Error("test-windows-pr does not deal its work over the three shards `shard: [0, 1, 2]`; #1332 measured four packages at or over the 100 s per-package ceiling under -short, which is thirteen minutes of work under a six-minute cap when one job carries it")
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

// windowsShardBudget is the seconds of Windows work a shard should carry, and it
// is the number ci.yml's shard plan compares each measurement against: at or
// over it a package's tests are dealt across the three slots, under it the
// package runs whole in one.
const windowsShardBudget = 60.0

// TestWindowsPRShardPlanIsDerivedFromMeasurements keeps the three numbers that
// decide this leg's cost — the measurements, the shard budget, and the
// per-package timeout — in one place and in step with each other.
//
// The leg shipped with the hosted convention of 100 s per package. Its first
// real run (#1332) found four packages ON that ceiling under -short: 100.0 and
// 100.1 are not sizes but the timeout, so their true sizes are unknown and at
// least that. A ceiling a tree's packages sit on is not naming a hang, it IS the
// hang, so the ceiling became the measured PR_TIMEOUT in the Makefile. This test
// holds that it can never drop back below a size actually observed.
func TestWindowsPRShardPlanIsDerivedFromMeasurements(t *testing.T) {
	root := repoRoot(t)
	raw := readFile(t, filepath.Join(root, "testdata", "ci", "package-sizes-windows.tsv"))

	const modulePath = "github.com/mas-bandwidth/nova-tools/"
	sizes := map[string]float64{}
	largest := 0.0
	for n, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			t.Errorf("%s:%d: want exactly two tab-separated fields (import path, seconds), got %d: %q", windowsSizesPath, n+1, len(fields), line)
			continue
		}
		pkg := fields[0]
		if !strings.HasPrefix(pkg, modulePath) {
			t.Errorf("%s:%d: %q is not an import path in this module; the table is keyed the way `go list` prints, like its idle-Linux sibling", windowsSizesPath, n+1, pkg)
			continue
		}
		secs, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Errorf("%s:%d: %q is not a number of seconds: %v", windowsSizesPath, n+1, fields[1], err)
			continue
		}
		if _, dup := sizes[pkg]; dup {
			t.Errorf("%s:%d: %s is measured twice; the plan reads the first row and the second is a silent lie", windowsSizesPath, n+1, pkg)
		}
		sizes[pkg] = secs
		if secs > largest {
			largest = secs
		}
		// A measured package must still exist. A row for a package that has
		// left is a row the plan will never read and nobody will ever correct.
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath)))
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s:%d: %s is in the table but not in the tree; delete the row", windowsSizesPath, n+1, pkg)
		}
	}
	if len(sizes) == 0 {
		t.Fatalf("%s carries no measurements; the shard plan would guess at every package", windowsSizesPath)
	}

	// The four packages #1332 measured at the ceiling are the reason the leg is
	// sharded at all. If they ever fall out of the table the plan silently stops
	// dealing them, so they are named here.
	for _, pkg := range []string{"cmd/nova-bus", "cmd/nova-merge", "cmd/nova-review", "cmd/nova-wake"} {
		secs, ok := sizes[modulePath+pkg]
		if !ok {
			t.Errorf("%s has no row for %s, the package whose Windows size (#1332) is the reason this leg is sharded", windowsSizesPath, pkg)
			continue
		}
		if secs < windowsShardBudget {
			t.Errorf("%s says %s is %.1fs, under the %.0fs shard budget, so the plan would run it whole; #1332 measured it at or over 100 s on windows-latest under -short", windowsSizesPath, pkg, secs, windowsShardBudget)
		}
	}

	// The per-package ceiling lives in the Makefile, where `make test-pr` reads
	// it, and must be above every size this tree has been SEEN to have on
	// Windows — the censored rows included, since their true size is at least
	// what was recorded.
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	raw, ok := mk.vars["PR_TIMEOUT"]
	if !ok {
		t.Fatal("the Makefile declares no PR_TIMEOUT; the Windows PR leg's per-package ceiling has nowhere to live but a workflow line nobody can run")
	}
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("PR_TIMEOUT = %q is not a Go duration: %v", raw, err)
	}
	if d.Seconds() <= largest {
		t.Errorf("PR_TIMEOUT = %s, at or under the largest measured Windows size (%.1fs in %s); a ceiling a package sits on is the hang, not the detector — that is what #1332 found at 100 s", d, largest, windowsSizesPath)
	}
	if !strings.Contains(readFile(t, filepath.Join(root, "Makefile")), "#1332") {
		t.Error("the Makefile does not say where PR_TIMEOUT's number comes from; a ceiling is a claim about the machine and belongs in the repository with its measurement")
	}
}
