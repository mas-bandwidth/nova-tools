package pulse

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The gate's own negative control, SPEC-TOOLWORK.md §1 rules 6-8 (PR #1637), issue
// #1649: it must be seen red before its green counts. The fixture is the one the binary
// ships, cmd/nova-pulse/testdata/accept, read here from the tree.

const shippedFixtures = "../../cmd/nova-pulse/testdata/accept"

func shipped(t *testing.T) fs.FS {
	t.Helper()
	if _, err := os.Stat(filepath.Join(shippedFixtures, "card.md")); err != nil {
		t.Fatalf("the shipped fixtures are not at %s: %v", shippedFixtures, err)
	}
	return os.DirFS(shippedFixtures)
}

// copyFixtures copies the shipped fixtures into a directory a test may edit.
func copyFixtures(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "fixtures")
	err := fs.WalkDir(os.DirFS(shippedFixtures), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		raw, err := os.ReadFile(filepath.Join(shippedFixtures, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

type selftestRun struct {
	code           int
	stdout, stderr string
	root           string
}

func runSelftest(t *testing.T, fixtures fs.FS) selftestRun {
	t.Helper()
	specs := fakeSandboxOnly(t)
	root := t.TempDir()
	fakeTool(t, specs, "nova-sandbox", fakeSpec{Log: filepath.Join(root, "wall.log"), Default: fakeRule{Exec: true}})
	cert := filepath.Join(root, "cert.txt")
	if err := os.WriteFile(cert, []byte("bench=lab legs=go,git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Selftest(SelftestInput{
		Fixtures: fixtures, Root: root, Bench: "lab", Cert: cert, Build: "test-build",
		Timeout: 5 * time.Minute, Max: 0, Stdout: &out, Stderr: &errb, Now: time.Now,
	})
	return selftestRun{code: code, stdout: out.String(), stderr: errb.String(), root: root}
}

func (r selftestRun) line(prefix string) string {
	for _, l := range strings.Split(r.stdout+r.stderr, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func controlFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "accept", "control"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

var seedNames = []string{"fix-reverted", "vacuous", "no-test", "wrong-author", "stray", "wide", "secret", "broken", "vetted", "renamed", "wrong-name", "skipped"}

// The positive control: the shipped fixtures pass, every seed draws its token with
// edits=1, and the passing selftest is on file under its control id.
func TestSelftestPassesOverTheShippedFixturesAndWritesTheControl(t *testing.T) {
	r := runSelftest(t, shipped(t))
	sum := r.line("ACCEPT SELFTEST ")
	if r.code != 0 || !strings.HasSuffix(sum, " PASS") {
		t.Fatalf("exit %d, line %q\nstdout:%s\nstderr:%s", r.code, sum, r.stdout, r.stderr)
	}
	want := regexp.MustCompile(`^ACCEPT SELFTEST control=([0-9a-f]{12}) accepted=1/1 rejected=12/12 edits=1 build=test-build fixtures=[0-9a-f]{12} bench=lab PASS$`)
	m := want.FindStringSubmatch(sum)
	if m == nil {
		t.Fatalf("summary %q does not match the grammar", sum)
	}
	for _, name := range seedNames {
		l := r.line("ACCEPT SEED name=" + name + " ")
		if l == "" || !strings.Contains(l, " edits=1 ") || !strings.HasSuffix(l, " ok") {
			t.Fatalf("seed %s: %q\n%s", name, l, r.stdout)
		}
	}
	if files := controlFiles(t, r.root); len(files) != 1 || files[0] != m[1] {
		t.Fatalf("control on file = %v, want [%s]", files, m[1])
	}
}

// selftest-wrong-token-is-a-fail: one seed's expected token changed; the seed draws its
// real token, the row is WRONG, the run is FAIL and nothing is put on file.
func TestSelftestWrongTokenIsAFail(t *testing.T) {
	dir := copyFixtures(t)
	spec := filepath.Join(dir, "seeds", "wrong-author", "seed.txt")
	raw, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, []byte(strings.Replace(string(raw), "want=identity", "want=stray-file", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runSelftest(t, os.DirFS(dir))
	if r.code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", r.code, r.stdout, r.stderr)
	}
	if l := r.line("ACCEPT SEED name=wrong-author "); !strings.Contains(l, " want=stray-file got=identity WRONG") {
		t.Fatalf("seed line %q", l)
	}
	if sum := r.line("ACCEPT SELFTEST "); !strings.Contains(sum, " rejected=11/12 ") || !strings.HasSuffix(sum, " FAIL") {
		t.Fatalf("summary %q", sum)
	}
	if files := controlFiles(t, r.root); len(files) != 0 {
		t.Fatalf("a failing selftest was put on file: %v", files)
	}
}

// selftest-every-seed-is-one-edit: a two-line seed is refused by count before any gate
// runs, exit 2, and the count is the one git applied.
func TestSelftestEverySeedIsOneEdit(t *testing.T) {
	dir := copyFixtures(t)
	patch := filepath.Join(dir, "seeds", "fix-reverted", "seed.patch")
	two := "diff --git a/sign/sign.go b/sign/sign.go\n--- a/sign/sign.go\n+++ b/sign/sign.go\n@@ -8,7 +8,7 @@ func Describe(n int) string { return fmt.Sprintf(\"sign(%d)\", n) }\n // Sign is 0 at zero and 1 elsewhere.\n func Sign(n int) int {\n \tif n == 0 {\n-\t\treturn 0\n+\t\treturn 1\n \t}\n-\treturn 1\n+\treturn 2\n }\n"
	if err := os.WriteFile(patch, []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runSelftest(t, os.DirFS(dir))
	if r.code != 2 {
		t.Fatalf("exit %d, want 2\n%s%s", r.code, r.stdout, r.stderr)
	}
	if l := r.line("ACCEPT REFUSED: seed fix-reverted made 2 edits, want exactly 1"); l == "" {
		t.Fatalf("no count refusal:\n%s%s", r.stdout, r.stderr)
	}
	if files := controlFiles(t, r.root); len(files) != 0 {
		t.Fatalf("a refused selftest was put on file: %v", files)
	}
}

// control-id-changes-with-the-build: the id is sha12 over (build identity, fixture
// digest, cert id), and each of the three moves it.
func TestControlIDChangesWithTheBuild(t *testing.T) {
	a := ControlID("build-1", "fffffffffff1", "hand")
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(a) {
		t.Fatalf("control id %q is not sha12", a)
	}
	for _, other := range []string{
		ControlID("build-2", "fffffffffff1", "hand"),
		ControlID("build-1", "fffffffffff2", "hand"),
		ControlID("build-1", "fffffffffff1", "c0ffee"),
	} {
		if other == a {
			t.Fatalf("a changed input kept the id %s", a)
		}
	}
	if ControlID("build-1", "fffffffffff1", "hand") != a {
		t.Fatal("the id is not a function of its inputs")
	}
	d1, err := FixtureDigest(shipped(t))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(d1) {
		t.Fatalf("digest %q, %v", d1, err)
	}
	dir := copyFixtures(t)
	d2, err := FixtureDigest(os.DirFS(dir))
	if err != nil || d2 != d1 {
		t.Fatalf("a byte-identical copy digests differently: %s vs %s (%v)", d1, d2, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seeds", "stray", "seed.txt"), []byte("kind=file\nwant=stray-file\nfile=sign/OUTCOME\ntext=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d3, _ := FixtureDigest(os.DirFS(dir)); d3 == d1 {
		t.Fatal("a changed fixture kept its digest")
	}
}

// ok-without-a-control-on-file-is-refused: with no passing selftest on file and no
// fixtures to run one, the gate never prints ACCEPT OK (ABSTAIN control-stale); given
// the fixtures it runs the selftest itself, and the OK it then prints carries the id
// that is now on file.
func TestAcceptOKWithoutAControlOnFileIsRefused(t *testing.T) {
	l := newAcceptLab(t)
	l.goodFix(t)
	l.noControl = true
	card := l.card(t, fixRedHeader)
	wantVerdict(t, l.run(t, card, nil), 2, "ACCEPT ABSTAIN label=CARD-7 kind=fix-red reason=control-stale ")
	if files := controlFiles(t, l.root); len(files) != 0 {
		t.Fatalf("something was put on file with no selftest run: %v", files)
	}
	l.fixtures = shipped(t)
	r := l.run(t, card, nil)
	v := wantVerdict(t, r, 0, "ACCEPT OK ")
	m := regexp.MustCompile(` control=([0-9a-f]{12}) `).FindStringSubmatch(v)
	if m == nil {
		t.Fatalf("no control id on %q", v)
	}
	if !strings.Contains(r.stdout, "ACCEPT SELFTEST control="+m[1]) {
		t.Fatalf("the gate did not run the selftest it needed:\n%s", r.stdout)
	}
	if files := controlFiles(t, l.root); len(files) != 1 || files[0] != m[1] {
		t.Fatalf("control on file = %v, want [%s]", files, m[1])
	}
	// A REJECT carries the id too, and never runs a selftest for it.
	acceptWrite(t, l.job, "other/other.go", "package other\n\n// Other is a package the card's PATHS: do not name.\nfunc Other() int { return 2 } // touched\n")
	l.commit(t, "wide")
	r = l.run(t, card, nil)
	wantVerdict(t, r, 1, " reason=out-of-path ", " control="+m[1]+" ")
}

// a-card-that-weakens-the-gate-is-rejected (eligibility rule 10): the card's diff touches
// the gate's sources, so the BASE's seeds are run against the HEAD's gate -- the head's
// nova-pulse built in the gate's worktree, its selftest over the base's fixtures -- and
// a seed that no longer draws its token is gate-weakened. The fixture repo carries a
// tiny cmd/nova-pulse whose selftest answer is decided by internal/pulse/gate.go.
func TestAcceptRejectsACardThatWeakensTheGate(t *testing.T) {
	l := newAcceptLab(t)
	gateMain := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\n\t\"fixture/internal/pulse\"\n)\n\nfunc main() {\n\tif pulse.SelftestPasses() {\n\t\tfmt.Println(\"ACCEPT SELFTEST control=000000000000 accepted=1/1 rejected=12/12 edits=1 build=fixture fixtures=000000000000 bench=lab PASS\")\n\t\tos.Exit(0)\n\t}\n\tfmt.Println(\"ACCEPT SEED name=stray edits=1 want=stray-file got=ACCEPT WRONG\")\n\tfmt.Println(\"ACCEPT SELFTEST control=000000000000 accepted=1/1 rejected=11/12 edits=1 build=fixture fixtures=000000000000 bench=lab FAIL\")\n\tos.Exit(1)\n}\n"
	acceptGit(t, l.job, nil, "checkout", "-q", "main")
	acceptWrite(t, l.job, "cmd/nova-pulse/main.go", gateMain)
	acceptWrite(t, l.job, "cmd/nova-pulse/testdata/accept/card.md", "RESULT FIXTURE sha=0\nKIND: fix-red\n")
	acceptWrite(t, l.job, "internal/pulse/gate.go", "package pulse\n\n// SelftestPasses stands for the whole gate here.\nfunc SelftestPasses() bool { return true }\n")
	acceptWrite(t, l.job, "internal/pulse/gate_test.go", "package pulse\n\nimport \"testing\"\n\nfunc TestGate(t *testing.T) {\n\tif !SelftestPasses() {\n\t\tt.Fatal(\"gate\")\n\t}\n}\n")
	l.commit(t, "the fixture's own gate")
	acceptGit(t, l.job, nil, "checkout", "-q", "card")
	acceptGit(t, l.job, nil, "reset", "-q", "--hard", "main")
	// The card: a fix in sign, and one line in the gate's sources that makes the head's
	// gate stop catching the stray seed.
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", fixTest)
	acceptWrite(t, l.job, "internal/pulse/gate.go", "package pulse\n\n// SelftestPasses stands for the whole gate here.\nfunc SelftestPasses() bool { return false }\n")
	acceptWrite(t, l.job, "internal/pulse/gate_test.go", "package pulse\n\nimport \"testing\"\n\nfunc TestGate(t *testing.T) {\n\tif SelftestPasses() {\n\t\tt.Fatal(\"gate\")\n\t}\n}\n")
	l.commit(t, "a fix, and a weaker gate")
	header := "KIND: fix-red\nPATHS: sign/**, internal/pulse/**\nTEST: sign TestSignZero\nLEGS: go\nSOURCE: fixture#1\nTEST-EDIT: internal/pulse/gate_test.go\n"
	r := l.run(t, l.card(t, header), nil)
	wantVerdict(t, r, 1, " reason=gate-weakened at=stray ")

	// The twin: a card that touches the gate's sources and leaves every seed drawing
	// its token is judged like any other.
	acceptGit(t, l.job, nil, "reset", "-q", "--hard", "main")
	acceptWrite(t, l.job, "sign/sign.go", fixtureFix)
	acceptWrite(t, l.job, "sign/sign_test.go", fixTest)
	acceptWrite(t, l.job, "internal/pulse/gate.go", "package pulse\n\n// SelftestPasses stands for the whole gate here; a comment changed.\nfunc SelftestPasses() bool { return true }\n")
	l.commit(t, "a fix, and a comment in the gate")
	r = l.run(t, l.card(t, "KIND: fix-red\nPATHS: sign/**, internal/pulse/**\nTEST: sign TestSignZero\nLEGS: go\nSOURCE: fixture#1\n"), nil)
	wantVerdict(t, r, 0, "ACCEPT OK ")
	if !strings.Contains(r.wall, " -- go build ") || !strings.Contains(r.wall, "accept --selftest") {
		t.Fatalf("the head's gate was not built and run inside the wall:\n%s", r.wall)
	}
}
