package ctxindex

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	write(t, repo, "go.mod", "module example.com/fix\n\ngo 1.22\n")
	write(t, repo, "docs/SPEC-A.md", "1. `alpha-rule-one`: alpha is refused\n   when empty.\n\n2. `beta-rule-two`: beta is kept.\n")
	write(t, repo, "notes.md", "1. `alpha-rule-one`: a stale copy that must not win.\n")
	write(t, repo, "pkg/a/a.go", "package a\n\ntype T struct{}\n\nfunc (T) Run() int { return 1 }\n\nfunc Alpha() int { return 2 }\n")
	write(t, repo, "pkg/a/b.go", "package a\n\nfunc Beta() int { return 3 }\n")
	write(t, repo, "pkg/a/a_test.go", "package a\n\nimport \"testing\"\n\n"+
		"// alpha-rule-one: the empty alpha.\nfunc TestAlpha(t *testing.T) { _ = Alpha(); _ = T{}.Run() }\n\n"+
		"func TestBeta(t *testing.T) { _ = Beta() }\n")
	write(t, repo, "pkg/a/testdata/x.go", "package broken(\n")
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "one")
	return repo
}

// Build writes the three indices at HEAD and each lookup answers by key.
func TestBuildIndexesSpecsTestsAndSymbols(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "ix")
	st, err := Build(repo, out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Specs != 2 || st.Tests != 2 || st.Guarded != 1 || st.Symbols != 4 {
		t.Fatalf("stats = %+v, want specs=2 tests=2 guarded=1 symbols=4", st)
	}
	ix, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	s, ok, err := ix.Spec("alpha-rule-one")
	if err != nil || !ok {
		t.Fatalf("spec lookup ok=%v err=%v", ok, err)
	}
	if s.File != "docs/SPEC-A.md" || s.Line != 1 || s.Paragraph != "1. `alpha-rule-one`: alpha is refused when empty." {
		t.Errorf("spec = %+v; the docs/SPEC copy wins and the paragraph ends at the blank line", s)
	}
	if strings.Join(s.Tests, ",") != "pkg/a.TestAlpha" {
		t.Errorf("guarding tests = %v", s.Tests)
	}
	tst, ok, _ := ix.Test("pkg/a.TestAlpha")
	if !ok || strings.Join(tst.Covers, ",") != "pkg/a/a.go" || tst.Line != 6 {
		t.Errorf("test = %+v ok=%v, want covers pkg/a/a.go at line 6", tst, ok)
	}
	sym, ok, _ := ix.Symbol("pkg/a.T.Run")
	if !ok || sym.File != "pkg/a/a.go" || sym.Line != 5 || strings.Join(sym.Tests, ",") != "pkg/a.TestAlpha" {
		t.Errorf("symbol = %+v ok=%v", sym, ok)
	}
	if b, ok, _ := ix.Symbol("pkg/a.Beta"); !ok || strings.Join(b.Tests, ",") != "pkg/a.TestBeta" {
		t.Errorf("Beta = %+v ok=%v", b, ok)
	}
	if _, ok, _ := ix.Spec("no-such-rule"); ok {
		t.Error("a missing ID answered")
	}
}

// A lookup reads the one bucket its key hashes to and nothing else: every other bucket
// is garbage and the lookup still answers (Glenn 2026-09-23: indexed, never a linear scan).
func TestLookupReadsOnlyItsBucket(t *testing.T) {
	old := BucketSize
	BucketSize = 1
	t.Cleanup(func() { BucketSize = old })
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "ix")
	if _, err := Build(repo, out); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{
		filepath.Join("spec", bucketName(bucketOf("alpha-rule-one", ix.buckets["spec"]))):  true,
		filepath.Join("test", bucketName(bucketOf("pkg/a.TestAlpha", ix.buckets["test"]))): true,
	}
	garbled := 0
	for _, sub := range []string{"spec", "test", "symbol"} {
		if ix.buckets[sub] < 2 && sub != "symbol" {
			t.Fatalf("%s has %d bucket(s); BucketSize=1 must spread it", sub, ix.buckets[sub])
		}
		entries, err := os.ReadDir(filepath.Join(ix.dir, sub))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if keep[filepath.Join(sub, e.Name())] {
				continue
			}
			garbled++
			if err := os.WriteFile(filepath.Join(ix.dir, sub, e.Name()), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if garbled < 4 {
		t.Fatalf("garbled %d buckets; the control needs other buckets to exist", garbled)
	}
	block, err := ix.Context("fix alpha-rule-one please")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SPEC alpha-rule-one (docs/SPEC-A.md:1)", "GUARDING TEST: pkg/a.TestAlpha (pkg/a/a_test.go:6)", "FILES: pkg/a/a.go, pkg/a/a_test.go"} {
		if !strings.Contains(block, want) {
			t.Errorf("context lacks %q:\n%s", want, block)
		}
	}
}

// A rebuild at a later landing answers from the new head only: a spec ID removed there
// is gone.
func TestRebuildAnswersFromTheNewHeadOnly(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "ix")
	if _, err := Build(repo, out); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "docs/SPEC-A.md", "1. `alpha-rule-one`: alpha is refused.\n")
	gitIn(t, repo, "commit", "-q", "-am", "drop beta")
	if _, err := Build(repo, out); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ix.Spec("beta-rule-two"); ok {
		t.Error("a spec ID removed at the new head answered")
	}
	if s, ok, _ := ix.Spec("alpha-rule-one"); !ok || s.Paragraph != "1. `alpha-rule-one`: alpha is refused." {
		t.Errorf("alpha at the new head = %+v ok=%v", s, ok)
	}
	if block, _ := ix.Context("beta-rule-two"); block != "" {
		t.Errorf("context for a removed ID = %q, want empty", block)
	}
}

// A build that stops partway (or is still running) never changes what a cut reads:
// CURRENT still names the last complete head, whose directory answers whole. The next
// complete build swaps CURRENT and the new head answers.
func TestPartialBuildLeavesThePreviousIndexWhole(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "ix")
	st1, err := Build(repo, out)
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "docs/SPEC-A.md", "1. `alpha-rule-one`: alpha is refused.\n")
	gitIn(t, repo, "commit", "-q", "-am", "drop beta")
	stop := errors.New("killed partway")
	afterBuckets = func(kind string) error {
		if kind == "test" {
			return stop
		}
		return nil
	}
	_, err = Build(repo, out)
	afterBuckets = func(string) error { return nil }
	if !errors.Is(err, stop) {
		t.Fatalf("partial build err = %v, want %v", err, stop)
	}
	head2 := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	if _, err := os.Stat(filepath.Join(out, head2, "spec")); err != nil {
		t.Fatalf("the control needs the partial build's spec buckets on disk: %v", err)
	}
	ix, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Head != st1.Head {
		t.Fatalf("after a partial build CURRENT = %s, want the complete head %s", ix.Head, st1.Head)
	}
	block, err := ix.Context("beta-rule-two alpha-rule-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SPEC beta-rule-two", "SPEC alpha-rule-one (docs/SPEC-A.md:1): 1. `alpha-rule-one`: alpha is refused when empty.", "GUARDING TEST: pkg/a.TestAlpha"} {
		if !strings.Contains(block, want) {
			t.Errorf("context from the previous complete head lacks %q:\n%s", want, block)
		}
	}
	st2, err := Build(repo, out)
	if err != nil {
		t.Fatal(err)
	}
	ix, err = Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Head != st2.Head || ix.Head != head2 {
		t.Fatalf("after the complete build CURRENT = %s, want %s", ix.Head, head2)
	}
	if _, ok, _ := ix.Spec("beta-rule-two"); ok {
		t.Error("the new head answered a spec ID it removed")
	}
}

// A bucket in the CURRENT head's directory that is missing or carries another head is
// a damaged index: the lookup, and so the cut, is refused rather than silently missing.
func TestDamagedBucketRefusesTheLookup(t *testing.T) {
	repo := fixture(t)
	out := filepath.Join(t.TempDir(), "ix")
	if _, err := Build(repo, out); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(ix.dir, "spec", bucketName(bucketOf("alpha-rule-one", ix.buckets["spec"])))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	foreign := strings.Replace(string(raw), ix.Head, strings.Repeat("0", 40), 1)
	if foreign == string(raw) {
		t.Fatal("the control needs the bucket to carry its head")
	}
	if err := os.WriteFile(p, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Context("alpha-rule-one"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("foreign-head bucket: Context err = %v, want a damaged-index error", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Context("alpha-rule-one"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("missing bucket: Context err = %v, want a damaged-index error", err)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func TestOpenRefusesAnUnbuiltIndex(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "never built") {
		t.Fatalf("err = %v", err)
	}
}
