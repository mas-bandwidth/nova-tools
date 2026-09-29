package tlc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The embedded list is written by hand because go:embed cannot leave the tests
// out; it must be exactly the directory's non-test Go files, or a runner file
// would change what a result means without changing the fingerprint.
func TestEmbeddedSourcesAreTheNonTestFiles(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			want = append(want, RunnerDir+"/"+n)
		}
	}
	files, err := RunnerFiles()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for name, raw := range files {
		got = append(got, name)
		disk, err := os.ReadFile(filepath.Join(".", filepath.Base(name)))
		if err != nil || string(disk) != string(raw) {
			t.Errorf("%s: the embedded bytes are not the file's", name)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded %v, directory holds %v: add the file to the go:embed line", got, want)
	}
}

// independent computes the fingerprint the way internal/ci does, from a
// checkout's files.
func independent(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	paths = append(paths, filepath.Join(root, "tla", CasesFile))
	for _, glob := range []string{filepath.Join(root, "tla", "*.tla"), filepath.Join(root, "tla", "MC*.cfg")} {
		m, _ := filepath.Glob(glob)
		paths = append(paths, m...)
	}
	runner, _ := filepath.Glob(filepath.Join(root, RunnerDir, "*.go"))
	for _, p := range runner {
		if !strings.HasSuffix(p, "_test.go") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestFingerprintOfTheRepositoryIsWhatTheChecksComputeFromFiles(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	got, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := independent(t, root); got != want {
		t.Fatalf("fingerprint %s, from files %s", got, want)
	}
}

func TestFingerprintChangesWithEveryInput(t *testing.T) {
	t.Parallel()
	base := map[string]string{"CASES.tsv": "plan\n", "MCA.tla": "model\n", "MCA.cfg": "bounds\n", "Plain.tla": "module\n", "README.md": "not an input\n"}
	root := tree(t, base)
	first, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Fingerprint(root); again != first {
		t.Fatal("the fingerprint is not deterministic")
	}
	edits := map[string]string{"the plan": "CASES.tsv", "a model": "MCA.tla", "a configuration": "MCA.cfg", "a plain module": "Plain.tla"}
	for name, file := range edits {
		root := tree(t, base)
		if err := os.WriteFile(filepath.Join(root, "tla", file), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := Fingerprint(root); got == first {
			t.Errorf("editing %s left the fingerprint unchanged", name)
		}
	}
	for name, add := range map[string]string{"a new model": "New.tla", "a new configuration": "MCNew.cfg"} {
		root := tree(t, base)
		if err := os.WriteFile(filepath.Join(root, "tla", add), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := Fingerprint(root); got == first {
			t.Errorf("adding %s left the fingerprint unchanged", name)
		}
	}
	other := tree(t, base)
	if err := os.WriteFile(filepath.Join(other, "tla", "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Fingerprint(other); got != first {
		t.Error("a file that is not an input changed the fingerprint")
	}
	if _, err := Fingerprint(t.TempDir()); err == nil {
		t.Error("a tree with no plan was fingerprinted")
	}
}
