//go:build functional

package card_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// realRepo is a git repository at <tmp>/out/repo holding a Go module: the
// base commit's files, then the head commit's over them. It returns the
// checkout and the two shas.
func realRepo(t *testing.T, baseFiles, headFiles map[string]string) (repo, base, head string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "out", "repo")
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=g@example.com", "-c", "user.name=g", "-c", "core.hooksPath=/dev/null"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	write := func(files map[string]string) {
		for p, body := range files {
			full := filepath.Join(repo, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	write(map[string]string{"go.mod": "module example.com/m\n\ngo 1.26\n"})
	write(baseFiles)
	git("commit", "-q", "-m", "base")
	base = git("rev-parse", "HEAD")
	write(headFiles)
	git("commit", "-q", "-m", "head")
	return repo, base, git("rev-parse", "HEAD")
}

// TestRealGateHoldsARealRepoToItsSpec is the fix round's item 1 on
// nova-tools#4401, run for real: git and go over a temp module, no fake
// Runner. Green at head is the named test's own pass event, so a TEST over
// a package with [no test files] or a -run that selects [no tests to run]
// is not green; a test green at base is not red; a red at base with the
// change's test and a pass at head passes, with a tagged TEST run under its
// tags; a fix copy is held to the finding test it names at the PR head, and
// the base worktree is removed every time. The nova-tools#4401 read's probes
// run here too: an old green test beside a new file calling new code (d1)
// and a base that does not compile (d2) are not red, a `./...` TEST whose
// other package fails to build at base is refused (d3), and a fix's finding
// test of `none <why>` is refused.

func TestRealGateHoldsARealRepoToItsSpec(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal("go is not on PATH: the real gate runs go test")
	}
	const (
		addWrong = "package x\n\nfunc Add(a, b int) int { return a - b }\n"
		addRight = "package x\n\nfunc Add(a, b int) int { return a + b }\n"
		addTest  = "package x\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tt.Parallel()\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(\"2+2\")\n\t}\n}\n"
		oldTest  = "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Parallel() }\n"
		newThing = "package x\n\nfunc Add(a, b int) int { return a + b }\n\nfunc NewThing() int { return 1 }\n"
		newTest  = "package x\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {\n\tt.Parallel()\n\tif NewThing() != 1 {\n\t\tt.Fatal(\"new\")\n\t}\n}\n"
		broken   = "package x\n\nfunc Add(a, b int) int { return a + }\n"
	)
	fixed := map[string]string{"x/x.go": addRight, "x/x_test.go": addTest}
	for _, tc := range []struct {
		name       string
		base, head map[string]string
		test       string
		reason     string
		row        string // in the rows
		why        string // in the refusal
	}{
		{name: "red at base, green at head", base: map[string]string{"x/x.go": addWrong}, head: fixed, test: "./x TestAdd",
			reason: card.GatePass, row: "at base "},
		{name: "no test files", base: map[string]string{"x/x.go": addWrong}, head: map[string]string{"x/x.go": addRight, "x/x_test.go": addTest, "q/q.go": "package q\n\nfunc Q() {}\n"},
			test: "./q TestAnything", reason: card.GateNotGreen, row: "[no test files]"},
		{name: "no tests to run", base: map[string]string{"x/x.go": addWrong}, head: fixed, test: "./x TestMissing",
			reason: card.GateNotGreen, row: "[no tests to run]"},
		{name: "green at base", base: map[string]string{"x/x.go": addRight, "x/old_test.go": oldTest}, head: map[string]string{"x/x_test.go": addTest},
			test: "./x TestOld", reason: card.GateNotRed, row: "pass"},
		{name: "tagged", base: map[string]string{"x/x.go": addWrong}, head: map[string]string{"x/x.go": addRight, "x/x_test.go": "//go:build functional\n\n" + addTest},
			test: "-tags functional ./x TestAdd", reason: card.GatePass, row: "TEST -tags functional ./x TestAdd at head "},
		{name: "d1: an old green test beside a new file calling new code", base: map[string]string{"x/x.go": addRight, "x/old_test.go": oldTest},
			head: map[string]string{"x/x.go": newThing, "x/new_test.go": newTest}, test: "./x TestOld", reason: card.GateNotRed, row: "at base ", why: "do not add or change TestOld"},
		{name: "d1 control: the new test the file adds", base: map[string]string{"x/x.go": addRight, "x/old_test.go": oldTest},
			head: map[string]string{"x/x.go": newThing, "x/new_test.go": newTest}, test: "./x TestNew", reason: card.GatePass, row: "your test files add or change TestNew"},
		{name: "d2: a base that does not compile", base: map[string]string{"x/x.go": broken, "x/old_test.go": oldTest},
			head: map[string]string{"x/x.go": addRight, "x/x_test.go": addTest}, test: "./x TestOld", reason: card.GateNotRed, row: "at base ", why: "does not build at base-sha"},
		{name: "d3: a package pattern over another package that fails to build at base", base: map[string]string{"x/x.go": addRight, "x/x_test.go": addTest, "y/y.go": "package y\n\nfunc Y() int { return }\n"},
			head: map[string]string{"y/y.go": "package y\n\nfunc Y() int { return 1 }\n", "x/x_test.go": addTest + "\n// touched\n"}, test: "./... TestAdd", reason: card.GateNoTest, row: "not one package", why: "not one package"},
		{name: "d3: the named package, green at base", base: map[string]string{"x/x.go": addRight, "x/x_test.go": addTest, "y/y.go": "package y\n\nfunc Y() int { return }\n"},
			head: map[string]string{"y/y.go": "package y\n\nfunc Y() int { return 1 }\n", "x/x_test.go": addTest + "\n// touched\n"}, test: "./x TestAdd", reason: card.GateNotRed, row: "with the diff's tests: pass", why: "passes at base-sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, base, head := realRepo(t, tc.base, tc.head)
			paths, err := card.ChangedPaths(repo, base)
			if err != nil {
				t.Fatal(err)
			}
			g := card.RunSpecGate(context.Background(), card.GateInput{Repo: repo, Base: base, Head: head, Test: tc.test, Paths: paths})
			rows := strings.Join(g.Rows, "\n")
			if g.Reason != tc.reason || !strings.Contains(rows, tc.row) || !strings.Contains(g.Why, tc.why) {
				t.Fatalf("gate %s why=%q, want %s with %q in the rows:\n%s", g.Reason, g.Why, tc.reason, tc.row, rows)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(repo), "base")); !os.IsNotExist(err) {
				t.Errorf("the base worktree is still there: %v", err)
			}
		})
	}
	t.Run("fix copy", func(t *testing.T) {
		t.Parallel()
		// the PR head: the primary's TestAdd is green, but the read found
		// Add wrong for a negative a. The fix corrects Add and carries
		// TestAddNegative, red at the PR head.
		prHead := map[string]string{"x/x.go": "package x\n\nfunc Add(a, b int) int {\n\tif a < 0 {\n\t\treturn 0\n\t}\n\treturn a + b\n}\n", "x/x_test.go": addTest}
		fix := map[string]string{"x/x.go": addRight,
			"x/neg_test.go": "package x\n\nimport \"testing\"\n\nfunc TestAddNegative(t *testing.T) {\n\tt.Parallel()\n\tif Add(-1, 2) != 1 {\n\t\tt.Fatal(\"-1+2\")\n\t}\n}\n"}
		repo, prSHA, commit := realRepo(t, prHead, fix)
		cc := card.CopyCard{ID: "p1~2", Primary: "p1", Leg: "fix", Head: prSHA, BaseSHA: "0000000000000000000000000000000000000000", Test: "./x TestAdd"}
		for _, fc := range []struct {
			finding, reason string
		}{{"./x TestAddNegative", card.GatePass}, {"", card.GateNoTest}, {"none cosmetic", card.GateNoTest}} {
			g, err := card.GateFriendCopy(context.Background(), card.FriendGate{Copy: cc, Finding: fc.finding, Repo: repo, Head: commit})
			if err != nil || g.Reason != fc.reason {
				t.Fatalf("finding %q: gate %s why=%q err=%v, want %s\n%s", fc.finding, g.Reason, g.Why, err, fc.reason, strings.Join(g.Rows, "\n"))
			}
		}
		// held to the primary's TEST at the PR head, the same fix is not red
		paths, _ := card.ChangedPaths(repo, prSHA)
		if g := card.RunSpecGate(context.Background(), card.GateInput{Repo: repo, Base: prSHA, Head: commit, Test: cc.Test, Paths: paths}); g.Reason != card.GateNotRed {
			t.Fatalf("the primary's TEST at the PR head: %s, want test-not-red", g.Reason)
		}
		if _, err := card.GateFriendCopy(context.Background(), card.FriendGate{Copy: cc, Finding: "./x TestAddNegative", Repo: repo, Head: prSHA}); err == nil || !strings.Contains(err.Error(), "not --head") {
			t.Fatalf("a checkout at another head: %v, want a refusal", err)
		}
	})
}
