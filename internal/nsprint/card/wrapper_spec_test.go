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
	// headBy and baseBy answer TEST by the name it selects (-run ^<name>$),
	// before head and base
	headBy, baseBy map[string]gateAnswer
	calls          []string
}

// runName is the test a go test argv selects with -run ^<name>$.
func runName(argv []string) string {
	for i, a := range argv {
		if a == "-run" && i+1 < len(argv) {
			return strings.TrimSuffix(strings.TrimPrefix(argv[i+1], "^"), "$")
		}
	}
	return ""
}

// testPass and testFail are go test -json's events for one test.
func testPass(name string) gateAnswer {
	return gateAnswer{exit: 0, out: `{"Action":"run","Package":"example.com/x","Test":"` + name + `"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"` + name + `","Output":"--- PASS: ` + name + ` (0.00s)\n"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x","Test":"` + name + `","Elapsed":0}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"ok  \texample.com/x\t0.01s\n"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x","Elapsed":0.01}` + "\n"}
}

func testFail(name string) gateAnswer {
	return gateAnswer{exit: 1, out: `{"Action":"run","Package":"example.com/x","Test":"` + name + `"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Test":"` + name + `","Output":"--- FAIL: ` + name + ` (0.00s)\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Test":"` + name + `","Elapsed":0}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Elapsed":0.01}` + "\n"}
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
		if a, ok := f.baseBy[runName(argv)]; ok {
			return answer(a)
		}
		return answer(f.base)
	case argv[0] == "go":
		if a, ok := f.headBy[runName(argv)]; ok {
			return answer(a)
		}
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
	pass, fail := testPass("TestY"), testFail("TestY")
	// exit 0 with no pass event for TestY: vacuous, never green
	vacuous := gateAnswer{exit: 0, out: `{"Action":"output","Package":"example.com/x","Output":"ok  \texample.com/x\t0.01s [no tests to run]\n"}` + "\n" + `{"Action":"pass","Package":"example.com/x","Elapsed":0}` + "\n"}
	noFiles := gateAnswer{exit: 0, out: `{"Action":"output","Package":"example.com/q","Output":"?   \texample.com/q\t[no test files]\n"}` + "\n" + `{"Action":"skip","Package":"example.com/q","Elapsed":0}` + "\n"}
	// a subtest's pass is not its parent's
	subOnly := gateAnswer{exit: 0, out: `{"Action":"pass","Package":"example.com/x","Test":"TestY/sub","Elapsed":0}` + "\n" + `{"Action":"pass","Package":"example.com/x","Elapsed":0}` + "\n"}
	// the package is not there at base: go test could not set it up, no test ran
	setup := gateAnswer{exit: 1, out: `{"ImportPath":"./x","Action":"build-output","Output":"stat /r/x: directory not found\n"}` + "\n" + `{"ImportPath":"./x","Action":"build-fail"}` + "\n" +
		`{"Action":"output","Package":"./x","Output":"FAIL\t./x [setup failed]\n"}` + "\n" + `{"Action":"fail","Package":"./x","Elapsed":0,"FailedBuild":"./x"}` + "\n"}
	// the diff's test does not build at base (it calls what the change adds): a red
	buildRed := gateAnswer{exit: 1, out: `{"ImportPath":"example.com/x [example.com/x.test]","Action":"build-output","Output":"x/x_test.go:3:30: undefined: Z\n"}` + "\n" +
		`{"ImportPath":"example.com/x [example.com/x.test]","Action":"build-fail"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Elapsed":0,"FailedBuild":"example.com/x [example.com/x.test]"}` + "\n"}
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
		files  map[string]string // a path's content; "x" when absent
	}{
		{name: "no TEST line", test: "", paths: []string{"x/x.go", "x/x_test.go"}, reason: card.GateNoTest, why: "no TEST line"},
		{name: "bare none", test: "none", paths: []string{"x/x.go", "x/x_test.go"}, reason: card.GateNoTest, why: "TEST: none says no why"},
		{name: "no test file in the diff", test: "./x TestY", paths: []string{"x/x.go"}, reason: card.GateNoTest, why: "adds or changes no test file"},
		{name: "not green at head", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: fail}, reason: card.GateNotGreen, why: "is not green at head " + head[:12], calls: 1},
		{name: "vacuous at head", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: vacuous}, reason: card.GateNotGreen, why: "ran no TestY in ./x: [no tests to run]", calls: 1},
		{name: "no test files at head", test: "./q TestAnything", paths: []string{"q/q.go", "x/x_test.go"}, fake: gateFake{head: noFiles}, reason: card.GateNotGreen, why: "ran no TestAnything in ./q: [no test files]", calls: 1},
		{name: "a subtest's pass at head", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: subOnly}, reason: card.GateNotGreen, why: "ran no TestY in ./x", calls: 1},
		{name: "not set up at base", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: setup}, reason: card.GateNotRed, why: "did not run at base-sha " + base[:12]},
		{name: "build red at base", test: "./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: buildRed, ci: ciGreen}, reason: card.GatePass,
			files: map[string]string{"x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestY(t *testing.T) { Z() }\n"}},
		{name: "tagged", test: "-tags functional ./x TestY", paths: []string{"x/x.go", "x/x_test.go"}, fake: gateFake{head: pass, base: fail, ci: ciGreen}, reason: card.GatePass},
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
				body, ok := tc.files[p]
				if !ok {
					body = "x"
				}
				if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
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
			if got.Passed() && tc.name == "tagged" && !strings.Contains(strings.Join(fake.calls, "\n"), "go test -tags functional ./x -run ^TestY$ -count=1 -json") {
				t.Errorf("a tagged TEST runs with its tags:\n%s", strings.Join(fake.calls, "\n"))
			}
			if got.Passed() && tc.test == "./x TestY" && tc.name == "green" {
				if got.Check != "pass" || got.Green == "" || !strings.Contains(got.Red, "fail") {
					t.Errorf("a green gate carries no RED and GREEN: %+v", got)
				}
				calls := strings.Join(fake.calls, "\n")
				for _, want := range []string{"go test ./x -run ^TestY$ -count=1 -json", "worktree add --detach --force", " " + base + "\n", "checkout " + head + " -- x/x_test.go", "worktree remove --force", "nova-ci local --base " + base} {
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

// TestFixCopyIsGatedOnItsFindingTest is the fix round's item 2 on
// nova-tools#4401: a fix copy's commit sits on the PR head, where the
// primary's TEST is green already, so holding the fix to it ends every fix
// test-not-red. The fix is held to the finding test it names on RESULT.md
// line 3, at the PR head (red) and at its commit (green); a fix that names
// none is refused with the fix's remedy; the fix card carries no primary
// TEST header and tells the model to name its test.
func TestFixCopyIsGatedOnItsFindingTest(t *testing.T) {
	t.Parallel()
	const (
		prHead = "0123456789abcdef0123456789abcdef01234567"
		commit = "89abcdef0123456789abcdef0123456789abcdef"
	)
	cc := card.CopyCard{ID: "p1~2", Primary: "p1", Leg: "fix", Kind: "build", Repo: "mas-bandwidth/nova-tools", PR: "4401", Head: prHead,
		Base: "dev", BaseSHA: "fedcba9876543210fedcba9876543210fedcba98", Paths: "x/x.go x/x_test.go", Test: "./x TestPrimary",
		Finding: "the gate misses a case", Branch: "rowan/p1", Stream: "swarm: cards"}
	if cc.GateBase() != prHead {
		t.Fatalf("GateBase = %s, want the PR head", cc.GateBase())
	}
	if work := (card.CopyCard{Leg: "work", Test: "./x TestPrimary", BaseSHA: "b"}); work.GateTest("./x TestFix") != "./x TestPrimary" || work.GateBase() != "b" {
		t.Fatalf("a work copy is held to the primary's TEST at base-sha: %q %q", work.GateTest("./x TestFix"), work.GateBase())
	}
	out := filepath.Join(t.TempDir(), "out")
	repo := filepath.Join(out, "repo")
	for _, p := range []string{"x/x.go", "x/x_test.go"} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: p1-c2 sha=0123\nDONE\nTEST: ./x TestFix\n## Notes\nTEST: ./x TestNot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finding := card.FindingTestOf(out)
	if finding != "./x TestFix" || cc.GateTest(finding) != "./x TestFix" {
		t.Fatalf("finding test %q, gate test %q; want ./x TestFix", finding, cc.GateTest(finding))
	}
	// the primary's test is green at the PR head; the fix's own is red there
	fake := func() *gateFake {
		return &gateFake{headBy: map[string]gateAnswer{"TestPrimary": testPass("TestPrimary"), "TestFix": testPass("TestFix")},
			baseBy: map[string]gateAnswer{"TestPrimary": testPass("TestPrimary"), "TestFix": testFail("TestFix")},
			ci:     gateAnswer{exit: 0, out: "nova-ci local: packages=1 seconds=0.4 red=0 make-exit=0\n"}}
	}
	in := card.GateInput{Repo: repo, Base: cc.GateBase(), Head: commit, Paths: []string{"x/x.go", "x/x_test.go"}, Fix: true}
	f := fake()
	in.Test, in.Run = cc.GateTest(finding), f.run
	if g := card.RunSpecGate(context.Background(), in); !g.Passed() || !strings.Contains(strings.Join(f.calls, "\n"), "-run ^TestFix$") {
		t.Fatalf("the finding test: %s %q\n%s", g.Reason, g.Why, strings.Join(f.calls, "\n"))
	}
	// what the gate did before the fix round: the primary's TEST at the PR head
	f = fake()
	in.Test, in.Run, in.Fix = cc.Test, f.run, false
	if g := card.RunSpecGate(context.Background(), in); g.Reason != card.GateNotRed {
		t.Fatalf("the primary's TEST at the PR head: %s, want test-not-red (why the fix is held to its own)", g.Reason)
	}
	f = fake()
	in.Test, in.Run, in.Fix = cc.GateTest(card.FindingTestOf(t.TempDir())), f.run, true
	if g := card.RunSpecGate(context.Background(), in); g.Reason != card.GateNoTest || !strings.Contains(g.Why, "a fix names its finding test") || len(f.calls) != 0 {
		t.Fatalf("no finding test: %s %q, want no-test with the fix's remedy and nothing run", g.Reason, g.Why)
	}
	body, err := card.RenderCopy(cc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "\nTEST: ./x TestPrimary\n") || !strings.Contains(string(body), "\nFINDING-TEST: "+card.FindingTestLine+"\n") {
		t.Errorf("the fix card:\n%s", body)
	}
}

// TestRedAtBaseIsTheNamedTestsOwn is the nova-tools#4401 read's item 1: a
// red at base is the named test's own fail event, or its package failing to
// build only when the diff's test files add or change that test; a pass of
// the named test at base is never red, whatever else failed. Before it, an
// old always-green test beside a new test file calling new code (d1), or a
// base that does not compile (d2), read as red and passed the gate.
func TestRedAtBaseIsTheNamedTestsOwn(t *testing.T) {
	t.Parallel()
	const (
		base = "0123456789abcdef0123456789abcdef01234567"
		head = "89abcdef0123456789abcdef0123456789abcdef"
	)
	buildFail := gateAnswer{exit: 1, out: `{"ImportPath":"example.com/x [example.com/x.test]","Action":"build-output","Output":"x/new_test.go:5:2: undefined: NewThing\n"}` + "\n" +
		`{"ImportPath":"example.com/x [example.com/x.test]","Action":"build-fail"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Elapsed":0,"FailedBuild":"example.com/x [example.com/x.test]"}` + "\n"}
	// the named test passed; the package failed after it (TestMain, a leak check)
	passThenFail := gateAnswer{exit: 1, out: `{"Action":"pass","Package":"example.com/x","Test":"TestOld","Elapsed":0}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"FAIL\texample.com/x\t0.01s\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Elapsed":0.01}` + "\n"}
	// the package panicked outside the named test: no event of its own
	panicked := gateAnswer{exit: 1, out: `{"Action":"output","Package":"example.com/x","Output":"panic: init\n"}` + "\n" +
		`{"Action":"fail","Package":"example.com/x","Elapsed":0.01}` + "\n"}
	const (
		oldTest  = "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Parallel() }\n"
		newTest  = "package x\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {\n\tNewThing()\n}\n"
		oldTest2 = "package x\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {\n\tNewThing()\n}\n"
	)
	ciGreen := gateAnswer{exit: 0, out: "nova-ci local: packages=1 seconds=0.4 red=0 make-exit=0\n"}
	for _, tc := range []struct {
		name     string
		test     string
		baseTest string            // x/old_test.go at base; "" for none
		files    map[string]string // the diff's files at head
		baseRun  gateAnswer
		reason   string
		why      string
	}{
		{name: "d1: an old test beside a new file that calls new code", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/new_test.go": newTest}, baseRun: buildFail, reason: card.GateNotRed, why: "do not add or change TestOld"},
		{name: "d1 control: the new test the file adds", test: "./x TestNew", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/new_test.go": newTest}, baseRun: buildFail, reason: card.GatePass},
		{name: "d1: an old test changed to call new code", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/old_test.go": oldTest2}, baseRun: buildFail, reason: card.GatePass},
		{name: "d1: an old test the diff's file carries unchanged", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/old_test.go": oldTest + "\nfunc TestNew(t *testing.T) { NewThing() }\n"}, baseRun: buildFail, reason: card.GateNotRed, why: "do not add or change TestOld"},
		{name: "d2: a base that does not compile", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/other_test.go": "package x\n"}, baseRun: buildFail, reason: card.GateNotRed, why: "does not build at base-sha " + base[:12]},
		{name: "a test in another package", test: "./x TestNew", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "y/new_test.go": "package y\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {}\n"}, baseRun: buildFail, reason: card.GateNotRed, why: "do not add or change TestNew"},
		{name: "a pass of the named test beside a package fail", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/new_test.go": newTest}, baseRun: passThenFail, reason: card.GateNotRed, why: "passes at base-sha " + base[:12]},
		{name: "a package fail outside the named test", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/old_test.go": oldTest2}, baseRun: panicked, reason: card.GateNotRed, why: "did not run at base-sha " + base[:12]},
		{name: "the named test's own fail", test: "./x TestOld", baseTest: oldTest,
			files: map[string]string{"x/x.go": "package x\n", "x/old_test.go": oldTest2}, baseRun: testFail("TestOld"), reason: card.GatePass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(t.TempDir(), "out")
			repo := filepath.Join(out, "repo")
			var paths []string
			for p, body := range tc.files {
				full := filepath.Join(repo, filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, p)
			}
			fake := &gateFake{head: testPass(strings.Fields(tc.test)[1]), base: tc.baseRun, ci: ciGreen}
			// the base worktree holds base's own test file before the
			// diff's are checked out over it
			run := func(ctx context.Context, c card.Cmd) (int, error) {
				exit, err := fake.run(ctx, c)
				if c.Argv[0] == "git" && strings.Contains(strings.Join(c.Argv, " "), "worktree add") && tc.baseTest != "" {
					dir := filepath.Join(c.Argv[len(c.Argv)-2], "x")
					if err := os.MkdirAll(dir, 0o755); err != nil {
						return -1, err
					}
					if err := os.WriteFile(filepath.Join(dir, "old_test.go"), []byte(tc.baseTest), 0o644); err != nil {
						return -1, err
					}
				}
				return exit, err
			}
			g := card.RunSpecGate(context.Background(), card.GateInput{Repo: repo, Base: base, Head: head, Test: tc.test, Paths: paths, Run: run})
			if g.Reason != tc.reason || !strings.Contains(g.Why, tc.why) {
				t.Fatalf("gate %s why=%q, want %s with %q\n%s", g.Reason, g.Why, tc.reason, tc.why, strings.Join(g.Rows, "\n"))
			}
		})
	}
}

// TestFixFindingTestIsNeverNone is the nova-tools#4401 read's item 4: a fix
// copy's finding test names a test. `none <why>` excused a fix from the class
// test; the finding is a defect at the PR head, so its test fails there. The
// fix is refused no-test with the fix's remedy and nothing runs; a work
// copy's `none <why>` is still a declaration.
func TestFixFindingTestIsNeverNone(t *testing.T) {
	t.Parallel()
	const prHead, commit = "0123456789abcdef0123456789abcdef01234567", "89abcdef0123456789abcdef0123456789abcdef"
	repo := filepath.Join(t.TempDir(), "out", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"none cosmetic", "none: the finding is a typo", "none"} {
		f := &gateFake{ci: gateAnswer{exit: 0, out: "nova-ci local: packages=1 seconds=0.4 red=0 make-exit=0\n"}}
		g := card.RunSpecGate(context.Background(), card.GateInput{Repo: repo, Base: prHead, Head: commit, Test: v, Fix: true, Paths: []string{"docs/a.md"}, Run: f.run})
		if g.Reason != card.GateNoTest || !strings.Contains(g.Why, "a fix") || !strings.Contains(g.Why, card.FindingTestLine) || len(f.calls) != 0 {
			t.Errorf("fix with TEST %q: %s %q (%d calls), want no-test with the fix's remedy and nothing run", v, g.Reason, g.Why, len(f.calls))
		}
	}
	f := &gateFake{ci: gateAnswer{exit: 0, out: "nova-ci local: packages=1 seconds=0.4 red=0 make-exit=0\n"}}
	if g := card.RunSpecGate(context.Background(), card.GateInput{Repo: repo, Base: prHead, Head: commit, Test: "none cosmetic", Paths: []string{"docs/a.md"}, Run: f.run}); !g.Passed() {
		t.Errorf("a work copy's none <why>: %s %q, want pass", g.Reason, g.Why)
	}
	if strings.Contains(card.FindingTestLine, "TEST: none") {
		t.Errorf("FindingTestLine still offers none: %q", card.FindingTestLine)
	}
}
