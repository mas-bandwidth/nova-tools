package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

func TestSpecNamesSparseCheckoutOfPATHSPackages(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("SPEC-SWARM.md is missing: %s", err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	for _, phrase := range []string{
		"## Sparse checkout of PATHS packages (#2498 S10)",
		"minimal tree",
		"those packages and their in-module dependencies only",
		"A package the card did not name is not materialized",
		"The named package's tests still run",
		"TestSparseCheckoutDoesNotMaterializeAnUnrelatedPackage",
		"TestPrepareStagesASparseJobClone",
		"valid empty set",
		"An import that cannot be resolved is not empty",
		"TestSparseCheckoutRefusesAMissingInModuleImport",
		"TestSparseCheckoutEmptyInModuleSetStillChecksOutPATHS",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-SWARM.md does not name the sparse-checkout rule keyed by %q", phrase)
		}
	}
}

// #2498 S10: staging for a card with PATHS checks out the declared packages and
// their in-module dependencies only. A fixture PATHS list must not materialize
// an unrelated package; the named package's tests still run.
func TestSparseCheckoutDoesNotMaterializeAnUnrelatedPackage(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	writeSparseFixture(t, src)

	dest := filepath.Join(root, "job", "repo")
	card := []byte("" +
		"RESULT: s10-sparse sha=000000000000\n" +
		"KIND: fix-red\n" +
		"PATHS: pkg/named/**\n" +
		"TEST: ./pkg/named TestHello\n" +
		"LEGS: go\n" +
		"SOURCE: mas-bandwidth/nova-tools#2498\n")
	if err := StageJobTree(src, dest, card); err != nil {
		t.Fatalf("StageJobTree: %v", err)
	}

	named := filepath.Join(dest, "pkg", "named", "named.go")
	if _, err := os.Stat(named); err != nil {
		t.Fatalf("the named package was not checked out: %v", err)
	}
	dep := filepath.Join(dest, "pkg", "dep", "dep.go")
	if _, err := os.Stat(dep); err != nil {
		t.Fatalf("the named package's dependency was not checked out: %v", err)
	}
	unrelated := filepath.Join(dest, "pkg", "unrelated", "unrelated.go")
	if _, err := os.Stat(unrelated); !os.IsNotExist(err) {
		t.Fatalf("PATHS named pkg/named/** but the unrelated package was materialized at %s (err=%v)", unrelated, err)
	}

	cmd := exec.Command("go", "test", "./pkg/named")
	cmd.Dir = dest
	cmd.Env = append(goenv.Clean(os.Environ()), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "FAIL") {
		t.Fatalf("the named package's tests still have to run in the sparse clone: %v\n%s", err, out)
	}
}

func writeSparseFixture(t *testing.T, src string) {
	t.Helper()
	mustWrite(t, filepath.Join(src, "go.mod"), "module example.com/s10sparse\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(src, "pkg", "named", "named.go"), ""+
		"package named\n\n"+
		"import \"example.com/s10sparse/pkg/dep\"\n\n"+
		"func Hello() string { return dep.Word() }\n")
	mustWrite(t, filepath.Join(src, "pkg", "named", "named_test.go"), ""+
		"package named\n\n"+
		"import \"testing\"\n\n"+
		"func TestHello(t *testing.T) {\n"+
		"	if Hello() != \"ok\" {\n"+
		"		t.Fatalf(\"Hello() = %q\", Hello())\n"+
		"	}\n"+
		"}\n")
	mustWrite(t, filepath.Join(src, "pkg", "dep", "dep.go"), ""+
		"package dep\n\n"+
		"func Word() string { return \"ok\" }\n")
	mustWrite(t, filepath.Join(src, "pkg", "unrelated", "unrelated.go"), ""+
		"package unrelated\n\n"+
		"func Noise() string { return \"no\" }\n")
	gitT(t, "", "init", "-q", "-b", "main", src)
	gitT(t, src, "add", "-A")
	gitT(t, src, "commit", "-q", "-m", "fixture")
}

// prepare is the staging path. A production run does not set CloneFrom by hand:
// a PATHS card takes the pool's reference checkout ref/<owner>/<name>@<rev>.
// Leaving CloneFrom empty with that checkout present must still stage the
// sparse job clone. A run that is handed CloneFrom stages from that checkout.
func TestPrepareStagesASparseJobClone(t *testing.T) {
	root := t.TempDir()
	workerDir := filepath.Join(root, "home")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w := Worker{WorkerDir: workerDir, Provider: "opencode", Model: "x", EnvVar: "X_API_KEY"}

	pool := filepath.Join(root, "pool")
	ref := filepath.Join(pool, "ref", "example", "s10@fixture")
	writeSparseFixture(t, ref)
	// A launch refuses a pool with no identity row (staging.go LoadPoolIdentity).
	writePoolIdentity(t, pool, "rowan", "Rowan Friend", "rowan@example.com")
	jobDir := w.JobDir(1, "s10")
	run := RunInput{Worker: w, Pool: &Pool{Dir: pool}}
	card := []byte("SOURCE: example/s10@fixture\nPATHS: pkg/named/**\nTEST: ./pkg/named TestHello\n")
	if err := run.prepare(Sidecar{ID: "s10"}, card, 1, jobDir); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	assertSparseJob(t, jobDir)

	src := filepath.Join(root, "src")
	writeSparseFixture(t, src)
	jobDir2 := w.JobDir(1, "s10b")
	handed := RunInput{Worker: w, CloneFrom: src, Pool: &Pool{Dir: pool}} // dev's prepare stages under the pool identity
	card2 := []byte("PATHS: pkg/named/**\nTEST: ./pkg/named TestHello\n")
	if err := handed.prepare(Sidecar{ID: "s10b"}, card2, 1, jobDir2); err != nil {
		t.Fatalf("prepare with CloneFrom: %v", err)
	}
	assertSparseJob(t, jobDir2)
}

func assertSparseJob(t *testing.T, jobDir string) {
	t.Helper()
	repo := filepath.Join(jobDir, JobRepo)
	if _, err := os.Stat(filepath.Join(repo, "pkg", "named", "named.go")); err != nil {
		t.Fatalf("prepare did not stage the named package: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "pkg", "dep", "dep.go")); err != nil {
		t.Fatalf("prepare did not stage the named package's dependency: %v", err)
	}
	unrelated := filepath.Join(repo, "pkg", "unrelated", "unrelated.go")
	if _, err := os.Stat(unrelated); !os.IsNotExist(err) {
		t.Fatalf("prepare materialized an unrelated package at %s (err=%v)", unrelated, err)
	}
}

// A missing in-module import is not an empty dependency set. Staging must
// refuse rather than check out PATHS with that dependency omitted, and the
// destination directory itself must not exist afterward.
func TestSparseCheckoutRefusesAMissingInModuleImport(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustWrite(t, filepath.Join(src, "go.mod"), "module example.com/s10absent\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(src, "pkg", "named", "named.go"), ""+
		"package named\n\n"+
		"import \"example.com/s10absent/pkg/absent\"\n\n"+
		"func Hello() string { return absent.Word() }\n")
	mustWrite(t, filepath.Join(src, "pkg", "unrelated", "unrelated.go"), ""+
		"package unrelated\n\n"+
		"func Noise() string { return \"no\" }\n")
	gitT(t, "", "init", "-q", "-b", "main", src)
	gitT(t, src, "add", "-A")
	gitT(t, src, "commit", "-q", "-m", "fixture")

	dest := filepath.Join(root, "job", "repo")
	card := []byte("PATHS: pkg/named/**\nTEST: ./pkg/named TestHello\n")
	err := StageJobTree(src, dest, card)
	if err == nil || !strings.Contains(err.Error(), "example.com/s10absent/pkg/absent") {
		t.Fatalf("StageJobTree err = %v, want the missing in-module import refused", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("staging refused (%v) but destination %s exists (stat %v)", err, dest, statErr)
	}
	named := filepath.Join(dest, "pkg", "named", "named.go")
	if _, statErr := os.Stat(named); !os.IsNotExist(statErr) {
		t.Fatalf("staging refused (%v) but still materialized %s (stat %v)", err, named, statErr)
	}
}

// No Go packages under PATHS is a valid empty dependency set: staging still
// checks out that path, and does not treat the empty lookup as a failure.
func TestSparseCheckoutEmptyInModuleSetStillChecksOutPATHS(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mustWrite(t, filepath.Join(src, "go.mod"), "module example.com/s10empty\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(src, "docs", "readme.md"), "# notes\n")
	mustWrite(t, filepath.Join(src, "pkg", "unrelated", "unrelated.go"), ""+
		"package unrelated\n\n"+
		"func Noise() string { return \"no\" }\n")
	gitT(t, "", "init", "-q", "-b", "main", src)
	gitT(t, src, "add", "-A")
	gitT(t, src, "commit", "-q", "-m", "fixture")

	dest := filepath.Join(root, "job", "repo")
	card := []byte("PATHS: docs/readme.md\n")
	if err := StageJobTree(src, dest, card); err != nil {
		t.Fatalf("a PATHS list with no Go packages is a valid empty dependency set, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "docs", "readme.md")); err != nil {
		t.Fatalf("the named non-Go path was not checked out: %v", err)
	}
	unrelated := filepath.Join(dest, "pkg", "unrelated", "unrelated.go")
	if _, err := os.Stat(unrelated); !os.IsNotExist(err) {
		t.Fatalf("empty dependency set materialized an unrelated package at %s (err=%v)", unrelated, err)
	}
}
