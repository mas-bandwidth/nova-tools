package pulse

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// The accept gate, SPEC-TOOLWORK.md §1 rules 2-5 (PR #1637), issue #1648. Every test
// here runs the REAL go build, vet and test over a fixture module, through a fake
// nova-sandbox that logs its argv and runs the command after `--`, so "every command
// went through the wall" is a line in a log and not a sentence in a comment.

const (
	fixtureBug = "package sign\n\n// Sign is 1 for every n; the bug is that Sign(0) should be 0.\nfunc Sign(n int) int { return 1 }\n"
	fixtureFix = "package sign\n\nfunc Sign(n int) int {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn 1\n}\n"
	baseTest   = "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 1 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n"
	// fixTest adds the red-first test for the defect.
	fixTest = baseTest + "\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n"
	// vacuousTest adds a test that holds with and without the fix.
	vacuousTest = baseTest + "\nfunc TestSignZero(t *testing.T) {\n\tif Sign(1) != 1 {\n\t\tt.Fatal(\"one\")\n\t}\n}\n"
)

type acceptLab struct {
	root, slot, job, cards, cert string
}

func acceptGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(),
		"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func acceptWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newAcceptLab is a swarm root with one slot and one job whose clone holds the base
// commit on main and a card branch checked out, ready for the card's commit.
func newAcceptLab(t *testing.T) *acceptLab {
	t.Helper()
	root := t.TempDir()
	l := &acceptLab{root: root, slot: filepath.Join(root, "slot"), cards: filepath.Join(root, "cards"), cert: filepath.Join(root, "cert.txt")}
	l.job = filepath.Join(l.slot, "jobs", "CARD-7")
	if err := os.MkdirAll(l.job, 0o755); err != nil {
		t.Fatal(err)
	}
	acceptGit(t, l.job, nil, "init", "-q", "-b", "main")
	acceptWrite(t, l.job, "go.mod", "module fixture\n\ngo 1.26\n")
	acceptWrite(t, l.job, "sign/sign.go", fixtureBug)
	acceptWrite(t, l.job, "sign/sign_test.go", baseTest)
	acceptWrite(t, l.job, "other/other.go", "package other\n\nfunc Other() int { return 2 }\n")
	acceptWrite(t, l.job, "other/other_test.go", "package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {\n\tif Other() != 2 {\n\t\tt.Fatal(\"two\")\n\t}\n}\n")
	acceptGit(t, l.job, nil, "add", "-A")
	acceptGit(t, l.job, nil, "commit", "-q", "-m", "base")
	acceptGit(t, l.job, nil, "checkout", "-q", "-b", "card")
	acceptWrite(t, root, "cert.txt", "bench=lab legs=go,git\n")
	return l
}

func (l *acceptLab) commit(t *testing.T, msg string, env ...string) {
	t.Helper()
	acceptGit(t, l.job, env, "add", "-A")
	acceptGit(t, l.job, env, "commit", "-q", "-m", msg)
}

// goodFix is the known-good card: the fix and its red-first test, one commit.
func (l *acceptLab) goodFix(t *testing.T) {
	t.Helper()
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", fixTest)
	l.commit(t, "sign: Sign(0) is 0, red test first")
}

const fixRedHeader = "KIND: fix-red\nPATHS: sign/**\nTEST: sign TestSignZero\nLEGS: go\nSOURCE: fixture#1\n"

// card writes the card file `cut` would have written: the contract line, the header,
// then the prose a worker reads.
func (l *acceptLab) card(t *testing.T, header string) string {
	t.Helper()
	p := filepath.Join(l.cards, "CARD-7.md")
	acceptWrite(t, l.root, "cards/CARD-7.md", "RESULT CARD-7 sha=0123456789ab\n"+header+"You are a worker. Job directory only.\nSTEP 1. git clone ...\n")
	return p
}

type acceptRun struct {
	code           int
	stdout, stderr string
	wall           string // the fake sandbox's argv log
}

// run drives Accept over the lab with the fake wall in front of PATH. `sandbox` is the
// fake's spec; nil means "log everything and run the command after --".
func (l *acceptLab) run(t *testing.T, card string, sandbox *fakeSpec) acceptRun {
	t.Helper()
	specs := fakeSandboxOnly(t)
	log := filepath.Join(l.root, "wall.log")
	spec := fakeSpec{Log: log, Default: fakeRule{Exec: true}}
	if sandbox != nil {
		spec = *sandbox
		spec.Log = log
	}
	fakeTool(t, specs, "nova-sandbox", spec)
	var out, errb bytes.Buffer
	code := Accept(AcceptInput{
		Job: l.job, Card: card, Base: "main", Bench: "lab", Cert: l.cert,
		Identities: []hyg.Identity{{Name: "Rowan", Email: "rowan@example.com"}},
		Timeout:    3 * time.Minute, Max: 20, Stdout: &out, Stderr: &errb, Now: time.Now,
	})
	raw, _ := os.ReadFile(log)
	return acceptRun{code: code, stdout: out.String(), stderr: errb.String(), wall: string(raw)}
}

func (r acceptRun) verdict(t *testing.T) string {
	t.Helper()
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "ACCEPT OK ") || strings.HasPrefix(line, "ACCEPT REJECT ") || strings.HasPrefix(line, "ACCEPT ABSTAIN ") {
			return line
		}
	}
	for _, line := range strings.Split(r.stderr, "\n") {
		if strings.HasPrefix(line, "ACCEPT REFUSED") {
			return line
		}
	}
	t.Fatalf("no ACCEPT line\nstdout:%s\nstderr:%s", r.stdout, r.stderr)
	return ""
}

func wantVerdict(t *testing.T, r acceptRun, code int, contains ...string) string {
	t.Helper()
	v := r.verdict(t)
	if r.code != code {
		t.Fatalf("exit %d, want %d: %s\nstdout:%s\nstderr:%s", r.code, code, v, r.stdout, r.stderr)
	}
	for _, c := range contains {
		if !strings.Contains(v, c) {
			t.Fatalf("verdict %q lacks %q\nstdout:%s\nstderr:%s", v, c, r.stdout, r.stderr)
		}
	}
	return v
}

// The positive control: the known-good fix is ACCEPT OK, and every go command the gate
// ran went through the wall's wrap.
func TestAcceptOKOnAGoodFix(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	r := l.run(t, l.card(t, fixRedHeader), nil)
	v := wantVerdict(t, r, 0, "ACCEPT OK label=CARD-7 kind=fix-red ", " base=", " edits=- control=- bench=lab cert=hand took=")
	if !regexp.MustCompile(` head=[0-9a-f]{12} `).MatchString(v) {
		t.Fatalf("no sha12 head on %q", v)
	}
	if !strings.Contains(v, " red_without=1 ") {
		t.Fatalf("want red_without=1 (TestSignZero red with the fix reverted): %q", v)
	}
	for _, want := range []string{" -- go build ", " -- go vet ", " -- go test "} {
		if !strings.Contains(r.wall, want) {
			t.Fatalf("the wall log holds no %q; every command runs inside nova-sandbox:\n%s", want, r.wall)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(r.wall), "\n") {
		if !strings.Contains(line, "--write ") || !strings.Contains(line, "--cwd ") {
			t.Fatalf("a wrap without a write root or a cwd: %q", line)
		}
	}
}

// accept-never-opens-result-md: RESULT.md is a fifo nobody writes. A gate that read it
// would hang here forever; this one finishes with the same verdict as above.
func TestAcceptNeverOpensResultMD(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	mkfifo(t, filepath.Join(l.job, "RESULT.md"))
	done := make(chan acceptRun, 1)
	card := l.card(t, fixRedHeader)
	go func() { done <- l.run(t, card, nil) }()
	select {
	case r := <-done:
		wantVerdict(t, r, 0, "ACCEPT OK ")
	case <-time.After(2 * time.Minute):
		t.Fatal("accept did not finish with a fifo RESULT.md in the job: something opened it")
	}
}

// accept-rejects-a-vacuous-test: the card's test holds without the fix, so it proves
// nothing about the fix.
func TestAcceptRejectsAVacuousTest(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", vacuousTest)
	l.commit(t, "a fix and a test that would pass anyway")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, "ACCEPT REJECT label=CARD-7 kind=fix-red ", " reason=vacuous-test at=TestSignZero ")
}

// A pre-existing test that stays green with the fix reverted is NOT vacuous: it was never
// about this fix. Only a test the card wrote or changed can be.
func TestAcceptDoesNotCallAPreExistingGreenVacuous(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 0, "ACCEPT OK ")
}

// accept-rejects-when-named-test-stays-green: the card names TestSign, a test that
// exists and passes with and without the fix. The fix's own red test is there and goes
// red, so mutate PASSes; the NAMED test did not, and the card's claim is about it.
func TestAcceptRejectsWhenNamedTestStaysGreen(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	r := l.run(t, l.card(t, "KIND: fix-red\nPATHS: sign/**\nTEST: sign TestSign\nLEGS: go\nSOURCE: fixture#1\n"), nil)
	wantVerdict(t, r, 1, " reason=named-test-not-red at=TestSign ")
}

// accept-never-reruns-a-red: the fix is wrong and its test is red at head. The verdict
// is red-at-head, and the wall log shows the package's tests were run ONCE.
func TestAcceptNeverRerunsARed(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n == 0 {\n\t\treturn 7\n\t}\n\treturn 1\n}\n")
	acceptWrite(t, l.job, "sign/sign_test.go", fixTest)
	l.commit(t, "a wrong fix")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=red-at-head at=TestSignZero ")
	runs := 0
	for _, line := range strings.Split(r.wall, "\n") {
		// The overlay of the base's test file runs its tests with -run and is a different
		// question over a different tree; the head's suite is the one that must run once.
		if strings.Contains(line, " -- go test ") && strings.Contains(line, "./sign/") && !strings.Contains(line, " -run ") {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("go test ./sign/ ran %d times, want exactly 1: a gate that reruns until green accepts every flaky fix\n%s", runs, r.wall)
	}
}

// accept-abstains-on-a-bench-red: the wall refuses. That is the bench's, not the card's.
func TestAcceptAbstainsOnABenchRed(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	r := l.run(t, l.card(t, fixRedHeader), &fakeSpec{Default: fakeRule{Stderr: "SANDBOX REFUSED reason=bad_read path=/opt/go: --read /opt/go does not exist", Exit: 2}})
	wantVerdict(t, r, 2, "ACCEPT ABSTAIN label=CARD-7 kind=fix-red reason=toolchain bench=lab took=")
}

// a-red-the-card-did-not-touch-is-run-once-at-base and base-red-is-an-abstain: the base
// already carries a red test in the package the card touched. The card neither changed
// nor named it. It is run once at the base, red there too, and the gate abstains.
func TestAcceptRunsAnUntouchedRedOnceAtBaseAndAbstains(t *testing.T) {
	l := newAcceptLab(t)
	// Rewrite the base so TestSign is red there: Sign(3) is 1 at the base and the
	// test wants 3.
	acceptGit(t, l.job, nil, "checkout", "-q", "main")
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 3 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n")
	l.commit(t, "base with a red test")
	acceptGit(t, l.job, nil, "checkout", "-q", "card")
	acceptGit(t, l.job, nil, "reset", "-q", "--hard", "main")
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 3 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "the fix, beside a red the card did not touch")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 2, "ACCEPT ABSTAIN ", " reason=base-red ")
	baseRuns := 0
	for _, line := range strings.Split(r.wall, "\n") {
		// The base worktree is <run>/base; the overlay runs the same -run in the head tree.
		if strings.Contains(line, " -- go test ") && strings.Contains(line, "-run ^(TestSign)$") && strings.Contains(line, string(filepath.Separator)+"base ") {
			baseRuns++
		}
	}
	if baseRuns != 1 {
		t.Fatalf("the untouched red was run at the base %d times, want once\n%s", baseRuns, r.wall)
	}
}

// a-deleted-base-test-is-test-weakened.
func TestAcceptDeletedBaseTestIsTestWeakened(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "the fix, and TestSign quietly gone")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=test-weakened at=TestSign ")
}

// a-skip-added-to-a-base-test-is-test-weakened.
func TestAcceptSkipAddedToABaseTestIsTestWeakened(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tt.Skip(\"later\")\n\tif Sign(3) != 1 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "the fix, and TestSign skipped")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=test-weakened at=TestSign ")
}

// base-tests-overlaid-on-head-must-pass: the card changed TestSign's assertion so that
// the base's TestSign, put back over the head, fails.
func TestAcceptBaseTestOverlaidOnHeadMustPass(t *testing.T) {
	l := newAcceptLab(t)
	// The "fix" changes what Sign(3) returns and rewrites TestSign to agree with it.
	acceptWrite(t, l.job, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n")
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 3 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "changes the contract and the test that held it")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=test-weakened at=TestSign ")
}

// test-edit-excuses-only-the-named-file, and a-test-edit-body-is-listed-for-the-reader.
func TestAcceptTestEditExcusesTheNamedFileAndListsIt(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", "package sign\n\nfunc Sign(n int) int {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n")
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 3 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "changes the contract, and says so")
	r := l.run(t, l.card(t, fixRedHeader+"TEST-EDIT: sign/sign_test.go\n"), nil)
	wantVerdict(t, r, 0, "ACCEPT OK ")
	if !strings.Contains(r.stdout, "ACCEPT NOTE test-edit=sign/sign_test.go") {
		t.Fatalf("the edited test body is not listed for the reader:\n%s", r.stdout)
	}
}

// A deleted test file is never excused, whatever TEST-EDIT: says.
func TestAcceptTestEditNeverExcusesADeletedFile(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	if err := os.Remove(filepath.Join(l.job, "sign", "sign_test.go")); err != nil {
		t.Fatal(err)
	}
	acceptWrite(t, l.job, "sign/zero_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "the fix, the old file gone")
	r := l.run(t, l.card(t, fixRedHeader+"TEST-EDIT: sign/sign_test.go\n"), nil)
	wantVerdict(t, r, 1, " reason=test-weakened at=sign/sign_test.go ")
}

// an-untracked-file-in-the-workers-copy-cannot-turn-the-gate-green: the test reads a
// file the worker left untracked in its clone. The gate runs in its own worktree of the
// commit, where the file is not, and the test is red there.
func TestAcceptUntrackedFileCannotTurnTheGateGreen(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", baseTest+"\nfunc TestSignZero(t *testing.T) {\n\tif _, err := os.ReadFile(\"testdata/answer.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	// The test file needs os; rewrite its import block.
	acceptWrite(t, l.job, "sign/sign_test.go", strings.Replace(baseTest, "import \"testing\"", "import (\n\t\"os\"\n\t\"testing\"\n)", 1)+"\nfunc TestSignZero(t *testing.T) {\n\tif _, err := os.ReadFile(\"testdata/answer.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "a test that needs a file the commit lacks")
	acceptWrite(t, l.job, "sign/testdata/answer.txt", "42\n") // untracked, in the worker's copy only
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=red-at-head at=TestSignZero ")
}

// The shape check: a fix-red card that changed no test file.
func TestAcceptRejectsNoTest(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	l.commit(t, "a fix with no test")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=no-test ")
}

// The shape check: the named test is not at the head.
func TestAcceptRejectsNamedTestMissing(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	r := l.run(t, l.card(t, "KIND: fix-red\nPATHS: sign/**\nTEST: sign TestSignNope\nLEGS: go\nSOURCE: fixture#1\n"), nil)
	wantVerdict(t, r, 1, " reason=named-test-missing at=TestSignNope ")
}

// Hygiene comes first, and the first failure decides: a stranger's commit is rejected
// before any go command runs.
func TestAcceptRejectsHygieneFirstAndRunsNothingAfterIt(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", fixTest)
	l.commit(t, "somebody else's fix", "GIT_COMMITTER_NAME=Bench", "GIT_COMMITTER_EMAIL=bench@elsewhere.example")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=identity at=")
	if strings.TrimSpace(r.wall) != "" {
		t.Fatalf("go ran after a hygiene finding; the first failure decides:\n%s", r.wall)
	}
}

// out-of-path through the gate: one line changed outside PATHS:.
func TestAcceptRejectsOutOfPath(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	acceptWrite(t, l.job, "other/other.go", "package other\n\nfunc Other() int { return 2 } // touched\n")
	l.commit(t, "and a line elsewhere")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 1, " reason=out-of-path at=other/other.go ")
}

// The bench's certification: no record, or a record for another bench, is an abstain.
func TestAcceptAbstainsWithoutACert(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	card := l.card(t, fixRedHeader)
	if err := os.Remove(l.cert); err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, l.run(t, card, nil), 2, "ACCEPT ABSTAIN ", " reason=bench-uncertified ")
	acceptWrite(t, l.root, "cert.txt", "bench=elsewhere legs=go\n")
	wantVerdict(t, l.run(t, card, nil), 2, " reason=bench-uncertified ")
	acceptWrite(t, l.root, "cert.txt", "bench=lab legs=sbcl\n")
	wantVerdict(t, l.run(t, card, nil), 2, " reason=bench-uncertified ")
}

// accept-abstains-on-an-unknown-kind (T06's red test, held here because the table lives
// here now); and a kind with no gate is refused, never judged.
func TestAcceptAbstainsOnAnUnknownKindAndRefusesAnUngatedOne(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	wantVerdict(t, l.run(t, l.card(t, "KIND: frobnicate\nPATHS: sign/**\nTEST: sign TestSignZero\nLEGS: go\nSOURCE: fixture#1\n"), nil), 2, "ACCEPT ABSTAIN ", " kind=frobnicate reason=unknown-kind ")
	wantVerdict(t, l.run(t, l.card(t, "KIND: read\nPATHS: none\nTEST: none\nLEGS: go\nSOURCE: fixture#1\n"), nil), 2, "ACCEPT REFUSED: kind read declares no gate")
	wantVerdict(t, l.run(t, l.card(t, "You are a worker.\n"), nil), 2, "ACCEPT REFUSED: ", "no KIND")
}

// The gate's own tree is removed on every path, and the job's clone is left as it was.
func TestAcceptRemovesItsWorktreeOnEveryPath(t *testing.T) {
	l := newAcceptLab(t)
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", vacuousTest)
	l.commit(t, "rejected on the way")
	before := acceptGit(t, l.job, nil, "status", "--porcelain")
	wantVerdict(t, l.run(t, l.card(t, fixRedHeader), nil), 1, " reason=vacuous-test ")
	if list := acceptGit(t, l.job, nil, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Fatalf("a worktree was left behind:\n%s", list)
	}
	entries, _ := os.ReadDir(filepath.Join(l.slot, "accept"))
	if len(entries) != 0 {
		t.Fatalf("the accept directory is not empty after the run: %d entries", len(entries))
	}
	if after := acceptGit(t, l.job, nil, "status", "--porcelain"); after != before {
		t.Fatalf("the job's clone changed: before %q after %q", before, after)
	}
}

// The header parser, on the card as cut writes it.
func TestReadCardHeaderReadsTheTypedLinesAndStopsAtTheProse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "card.md")
	body := "RESULT CARD-9 sha=abcdefabcdef\nKIND: fix-red\nPATHS: cmd/x/**, internal/y/*.go\nTEST: internal/y TestY\nLEGS: go,git\nSOURCE: o/r#12\nTEST-EDIT: internal/y/y_test.go\n\nYou are a worker.\nKIND: not-a-header-any-more\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := ReadCardHeader(p)
	if err != nil {
		t.Fatal(err)
	}
	if h.Label != "CARD-9" || h.Kind != "fix-red" || len(h.Paths) != 2 || h.TestPkg != "internal/y" || h.TestName != "TestY" || len(h.Legs) != 2 || h.Source != "o/r#12" || !h.IsTestEdit("internal/y/y_test.go") {
		t.Fatalf("header = %+v", h)
	}
	if m := h.Missing(); len(m) != 0 {
		t.Fatalf("missing = %v", m)
	}
	for _, bad := range []string{
		"RESULT C sha=x\nKIND: fix-red\nPATHS: ../**\nTEST: a TestA\n",
		"RESULT C sha=x\nKIND: fix-red\nPATHS: a/**\nTEST: ../a TestA\n",
		"RESULT C sha=x\nKIND: fix-red\nPATHS: a/**\nTEST: a notATest\n",
		"RESULT C sha=x\nKIND: fix-red\nPATHS: a/**\nTEST: a\n",
	} {
		if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadCardHeader(p); err == nil {
			t.Fatalf("a bad header was accepted: %q", bad)
		}
	}
	if err := os.WriteFile(p, []byte("RESULT C sha=x\nKIND: fix-red\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err = ReadCardHeader(p)
	if err != nil {
		t.Fatal(err)
	}
	if m := h.Missing(); strings.Join(m, ",") != "PATHS,TEST" {
		t.Fatalf("missing = %v, want PATHS,TEST", m)
	}
	if legs := h.LegsOrDefault(); len(legs) != 1 || legs[0] != "go" {
		t.Fatalf("a card with no LEGS: gets go, got %v", legs)
	}
}

// The kinds table has no default and every gated kind names its steps.
func TestKindsTableHasNoDefaultAndGatedKindsNameTheirSteps(t *testing.T) {
	if _, ok := KindNamed(""); ok {
		t.Fatal("the empty kind resolved to a row: there is no default kind")
	}
	fix, ok := KindNamed("fix-red")
	if !ok || !fix.Gated() || !fix.Step(StepMutate) || !fix.ControlBuilt() {
		t.Fatalf("fix-red = %+v", fix)
	}
	tt, ok := KindNamed("transcript-test")
	if !ok || !tt.Gated() || tt.Step(StepMutate) || tt.ControlBuilt() {
		t.Fatalf("transcript-test = %+v (its control is not built until T08/T13, and accept must not say OK without it)", tt)
	}
	for _, name := range []string{"read", "probe", "text", "tone"} {
		k, ok := KindNamed(name)
		if !ok || k.Gated() {
			t.Fatalf("%s should be an ungated kind: %+v", name, k)
		}
	}
}

// fakeSandboxOnly puts ONE fake in front of PATH, nova-sandbox, and leaves git and go
// real: the gate's own git reads and the go it runs behind the wall are the point of
// these tests. fakePATH's shared bin would shadow git with the loud fake.
func fakeSandboxOnly(t *testing.T) string {
	t.Helper()
	shared := fakeBins(t)
	raw, err := os.ReadFile(filepath.Join(shared, "nova-sandbox"+exeSuffix()))
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "nova-sandbox"+exeSuffix()), raw, 0o755); err != nil {
		t.Fatal(err)
	}
	specs := filepath.Join(t.TempDir(), "fakes")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", own+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NOVA_PULSE_FAKE_DIR", specs)
	return specs
}

// The same rule reached the other way: the untouched red sits in a test FILE the card
// did not change, so no overlay ever runs it, and the head's own suite is where it is
// first seen. It is still run once at the base before it is charged, and it is the base's.
func TestAcceptRunsAnUntouchedRedInAnUnchangedFileOnceAtBase(t *testing.T) {
	l := newAcceptLab(t)
	acceptGit(t, l.job, nil, "checkout", "-q", "main")
	acceptWrite(t, l.job, "sign/sign_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(3) != 3 {\n\t\tt.Fatal(\"three\")\n\t}\n}\n")
	l.commit(t, "base with a red test")
	acceptGit(t, l.job, nil, "checkout", "-q", "card")
	acceptGit(t, l.job, nil, "reset", "-q", "--hard", "main")
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/zero_test.go", "package sign\n\nimport \"testing\"\n\nfunc TestSignZero(t *testing.T) {\n\tif Sign(0) != 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	l.commit(t, "the fix in a new test file; sign_test.go untouched")
	r := l.run(t, l.card(t, fixRedHeader), nil)
	wantVerdict(t, r, 2, "ACCEPT ABSTAIN ", " reason=base-red ")
	baseRuns, headRuns := 0, 0
	for _, line := range strings.Split(r.wall, "\n") {
		if !strings.Contains(line, " -- go test ") {
			continue
		}
		if strings.Contains(line, string(filepath.Separator)+"base ") {
			baseRuns++
		} else if strings.Contains(line, "./sign/") {
			headRuns++
		}
	}
	if baseRuns != 1 || headRuns != 1 {
		t.Fatalf("head runs %d (want 1), base runs %d (want 1): once each, never a rerun\n%s", headRuns, baseRuns, r.wall)
	}
}
