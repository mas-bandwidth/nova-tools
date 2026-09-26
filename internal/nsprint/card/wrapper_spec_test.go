package card_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// gateFake answers the gate's child processes from a table: TEST at head
// (the checkout) and at base (the worktree), git, nova-ci local and the
// touched-packages fallback. It starts nothing.
type gateFake struct {
	head, base, ci, fallback gateAnswer
	calls                    []string
}

type gateAnswer struct {
	exit int
	out  string
	err  error
}

func (f *gateFake) run(_ context.Context, c card.Cmd) (int, error) {
	f.calls = append(f.calls, strings.Join(c.Argv, " "))
	argv := c.Argv
	answer := func(a gateAnswer) (int, error) {
		_, _ = io.WriteString(c.Out, a.out)
		return a.exit, a.err
	}
	switch {
	case argv[0] == "git" && strings.Contains(strings.Join(argv, " "), "worktree add"):
		// the worktree is a directory TEST runs in
		if err := os.MkdirAll(argv[len(argv)-2], 0o755); err != nil {
			return -1, err
		}
		return 0, nil
	case argv[0] == "git":
		return 0, nil
	case argv[0] == "go" && strings.HasSuffix(c.Dir, string(filepath.Separator)+"base"):
		return answer(f.base)
	case argv[0] == "go":
		return answer(f.head)
	case argv[0] == "nova-ci":
		return answer(f.ci)
	case argv[0] == "nice":
		return answer(f.fallback)
	}
	return -1, errors.New("unexpected command " + strings.Join(argv, " "))
}

// TestSpecGateHoldsTheCardToItsSpec is nova-tools#4313's DONE-WHEN: a copy's
// commit is proved against the card before it is pushed. The class test the
// card's TEST names must fail at BASE with the diff's test files and pass at
// HEAD; the diff must carry a test file; a card with TEST: none says why or
// is refused; and CI's answer for the diff (nova-ci local, else the touched
// packages) must be green. Every refusal is one typed reason and one line
// with the remedy, and the red names are in the rows RESULT.md carries.
func TestSpecGateHoldsTheCardToItsSpec(t *testing.T) {
	t.Parallel()

	const (
		base = "0123456789abcdef0123456789abcdef01234567"
		head = "89abcdef0123456789abcdef0123456789abcdef"
	)
	pass := gateAnswer{exit: 0, out: "ok  \texample.com/x\t0.01s\n"}
	fail := gateAnswer{exit: 1, out: "--- FAIL: TestY (0.00s)\nFAIL\n"}
	ciGreen := gateAnswer{exit: 0, out: "nova-ci local: base=x merge-base=0123 packages=1 ./x\nPKG ok 0.4s ./x\nnova-ci local: packages=1 seconds=0.4 red=0 make-exit=0\n"}
	ciRed := gateAnswer{exit: 1, out: "PKG FAIL 0.4s ./x\nRED package=./x test=TestZ\n    --- FAIL: TestZ\nRED package=./y test=TestQ\nnova-ci local: packages=2 seconds=0.8 red=2 make-exit=1\n"}
	ciRefused := gateAnswer{exit: 2, out: "nova-ci local: REFUSED /r has no .github/scripts/select-packages.sh, so there is no CI selection to match\n"}
	events := gateAnswer{exit: 1, out: `{"Action":"run","Package":"example.com/x","Test":"TestZ"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Test":"TestZ","Elapsed":0.1}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Elapsed":0.2}` + "\n" +
		`{"Action":"fail","Package":"example.com/y","Elapsed":0.2}` + "\n"}

	for _, tc := range []struct {
		name   string
		test   string
		paths  []string
		fake   gateFake
		reason string
		why    string // in the refusal
		reds   []string
		calls  int
	}{
		{name: "no TEST line", test: "", paths: []string{"x/x.go", "x/x_test.go"}, reason: card.GateNoTest, why: "no TEST line"},
		{name: "bare none", test: "none", paths: []string{"x/x.go", "x/x_test.go"}, reason: card.GateNoTest, why: "TEST: none says no why"},
		{name: "no test file in the diff", test: "./x TestY", paths: []string{"x/x.go"}, reason: card.GateNoTest, why: "adds or changes no test file"},
		{name: "not green at head", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: fail}, reason: card.GateNotGreen, why: "is not green at head " + head[:12], calls: 1},
		{name: "vacuous at head", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: gateAnswer{out: "ok  \texample.com/x\t0.01s [no tests to run]\n"}}, reason: card.GateNotGreen, why: "names no test in ./x", calls: 1},
		{name: "not red at base", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: pass}, reason: card.GateNotRed, why: "passes at base-sha " + base[:12] + " with your test files (x/x_test.go)"},
		{name: "nova-ci local red", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: fail, ci: ciRed}, reason: card.GateCIRed,
			why: "nova-ci local red: RED package=./x test=TestZ; RED package=./y test=TestQ", reds: []string{"RED package=./x test=TestZ", "RED package=./y test=TestQ"}},
		{name: "touched packages red where nova-ci local cannot run", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go", "y/y.go"}, fake: gateFake{head: pass, base: fail, ci: ciRefused, fallback: events}, reason: card.GateCIRed,
			why:  "go test -p 2 -count=1 ./x ./y red: RED package=example.com/x test=TestZ; RED package=example.com/y test=-",
			reds: []string{"RED package=example.com/x test=TestZ", "RED package=example.com/y test=- (the package failed outside a test: a build error, a panic, TestMain or the -timeout)"}},
		{name: "CI could not run", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: fail, ci: gateAnswer{exit: -1, err: errors.New("exec: nova-ci: not found")}, fallback: gateAnswer{exit: -1, err: errors.New("exec: nice: not found")}}, reason: card.GateCIRed, why: "CI could not run for the diff"},
		{name: "green", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: fail, ci: ciGreen}, reason: card.GatePass},
		{name: "none with a why", test: "none one docs page; the reader checks it", paths: []string{"docs/a.md"}, fake: gateFake{ci: ciGreen}, reason: card.GatePass, calls: 1},
		{name: "none, no Go package, nova-ci local not on PATH", test: "none one docs page", paths: []string{"docs/a.md"}, fake: gateFake{ci: gateAnswer{exit: -1, err: errors.New("exec: nova-ci: not found")}}, reason: card.GatePass, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(t.TempDir(), "out")
			repo := filepath.Join(out, "repo")
			for _, p := range tc.paths {
				full := filepath.Join(repo, filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			fake := tc.fake
			got := card.RunSpecGate(context.Background(), card.GateInput{Repo: repo, Base: base, Head: head, Test: tc.test, Paths: tc.paths, Run: fake.run})
			if got.Reason != tc.reason || (tc.reason != card.GatePass) != (got.Why != "") || !strings.Contains(got.Why, tc.why) || strings.ContainsAny(got.Why, "\n") {
				t.Fatalf("gate = %s why=%q, want %s with %q on one line\ncalls: %s", got.Reason, got.Why, tc.reason, tc.why, strings.Join(fake.calls, "\n"))
			}
			if strings.Join(got.Reds, "|") != strings.Join(tc.reds, "|") {
				t.Errorf("reds %q, want %q", got.Reds, tc.reds)
			}
			for _, r := range got.Reds {
				if !contains(got.Rows, r) {
					t.Errorf("rows lack the red %q:\n%s", r, strings.Join(got.Rows, "\n"))
				}
			}
			if tc.calls > 0 && len(fake.calls) != tc.calls {
				t.Errorf("%d commands ran, want %d:\n%s", len(fake.calls), tc.calls, strings.Join(fake.calls, "\n"))
			}
			if got.Passed() && tc.test == "./x TestY" {
				if got.Check != "pass" || got.Green == "" || !strings.Contains(got.Red, "fail") {
					t.Errorf("a green gate carries no RED and GREEN: %+v", got)
				}
				calls := strings.Join(fake.calls, "\n")
				for _, want := range []string{"go test ./x -run ^TestY$ -count=1", "worktree add --detach --force", " " + base + "\n", "checkout " + head + " -- x/x_test.go", "worktree remove --force", "nova-ci local --base " + base} {
					if !strings.Contains(calls, want) {
						t.Errorf("calls lack %q:\n%s", want, calls)
					}
				}
				if _, err := os.Stat(filepath.Join(out, "base")); err == nil {
					t.Error("the base worktree is still there")
				}
			}
			if got.Passed() && strings.HasPrefix(tc.test, "none") && !strings.Contains(got.Rows[0], "no class test required") {
				t.Errorf("rows[0] = %q, want the none row", got.Rows[0])
			}
			if !got.Passed() && !strings.Contains(got.Line(), "gate="+tc.reason) {
				t.Errorf("Line() = %q", got.Line())
			}
			if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: x\nDONE\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := card.AppendGates(out, got.Rows); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(out, "RESULT.md"))
			if !strings.HasPrefix(string(raw), "RESULT: x\nDONE\n## Gates\n- ") {
				t.Errorf("RESULT.md:\n%s", raw)
			}
			for _, r := range got.Reds {
				if !strings.Contains(string(raw), "\n- "+r+"\n") {
					t.Errorf("RESULT.md lacks the red %q:\n%s", r, raw)
				}
			}
		})
	}
	if err := card.AppendGates(t.TempDir(), []string{"x"}); err != nil {
		t.Errorf("no RESULT.md: %v, want nothing written and no error", err)
	}
}

func contains(rows []string, want string) bool {
	for _, r := range rows {
		if r == want {
			return true
		}
	}
	return false
}
