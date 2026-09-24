// THE SCAFFOLDING VERBS, proven the way a contributor meets them (nova-tools#2498 S5 part 2).
//
// `nova-ci new-rule <name>` lays down a new class rule's skeleton:
// - internal/ci/<name>_class_test.go
// - internal/ci/testdata/<name>/fixture.txt
// - make/rule_<name>.mk
//
// `nova-ci new-verb <tool> <verb>` lays down a new CLI verb's skeleton:
// - cmd/<tool>/<verb>.go
// - cmd/<tool>/<verb>_test.go
// - cmd/<tool>/testdata/<verb>/fixture.txt
// - make/verb_<tool>_<verb>.mk
//
// Both verbs ensure the new skeleton immediately builds, tests, and conforms to
// write confinement.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

func TestNewRuleYieldsABuildingTestingSkeleton(t *testing.T) {
	for _, tool := range []string{"make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH", tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	bin := buildCLI(t)
	tree := t.TempDir()

	for _, f := range []string{"go.mod", "go.sum", "Makefile"} {
		src := filepath.Join(root, f)
		if _, err := os.Stat(src); err == nil {
			copyScaffoldFile(t, src, filepath.Join(tree, f))
		}
	}

	// Copy required packages to scratch tree
	for _, dir := range []string{"cmd", "internal", "tools", "profiles"} {
		srcDir := filepath.Join(root, dir)
		if _, err := os.Stat(srcDir); err == nil {
			copyScaffoldTree(t, srcDir, filepath.Join(tree, dir), func(rel string) bool {
				return true
			})
		}
	}

	// Run new-rule verb
	out := runScaffoldCmd(t, bin, "new-rule", "--root", tree, "sample")
	for _, want := range []string{
		"internal/ci/sample_class_test.go",
		"internal/ci/testdata/sample/fixture.txt",
		"make/rule_sample.mk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("new-rule did not report writing %s:\n%s", want, out)
		}
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(want))); err != nil {
			t.Errorf("new-rule did not write %s: %v", want, err)
		}
	}

	// BUILDING: the copied tree builds cleanly
	runIn(t, tree, "go", "build", "./internal/ci/...")
	runIn(t, tree, "go", "vet", "./internal/ci/...")

	// TESTING: the fixture test passes
	if got := runIn(t, tree, "go", "test", "-v", "-count=1", "-run", "TestNoSampleViolations", "./internal/ci"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("go test of the class rule skeleton did not pass:\n%s", got)
	}

	// Makefile integration: make test-rule-sample runs and passes
	if got := runIn(t, tree, "make", "-f", "Makefile", "test-rule-sample"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("make test-rule-sample did not pass:\n%s", got)
	}

	// Write discipline: second run refuses rather than overwrite
	cmd := exec.Command(bin, "new-rule", "--root", tree, "sample")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "already exists") {
		t.Errorf("second new-rule run was not refused (err %v):\n%s", err, got)
	}

	// Invalid names are refused
	cmd = exec.Command(bin, "new-rule", "--root", tree, "Invalid Name!")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "rule name") {
		t.Errorf("new-rule with invalid name was not refused (err %v):\n%s", err, got)
	}
}

func TestNewVerbYieldsABuildingTestingSkeleton(t *testing.T) {
	for _, tool := range []string{"make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH", tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	bin := buildCLI(t)
	tree := t.TempDir()

	for _, f := range []string{"go.mod", "go.sum", "Makefile"} {
		src := filepath.Join(root, f)
		if _, err := os.Stat(src); err == nil {
			copyScaffoldFile(t, src, filepath.Join(tree, f))
		}
	}

	// Copy required packages to scratch tree
	for _, dir := range []string{"cmd", "internal", "tools", "profiles"} {
		srcDir := filepath.Join(root, dir)
		if _, err := os.Stat(srcDir); err == nil {
			copyScaffoldTree(t, srcDir, filepath.Join(tree, dir), func(rel string) bool {
				return true
			})
		}
	}

	// Run new-verb verb
	out := runScaffoldCmd(t, bin, "new-verb", "--root", tree, "nova-ci", "probe")
	for _, want := range []string{
		"cmd/nova-ci/probe.go",
		"cmd/nova-ci/probe_test.go",
		"cmd/nova-ci/testdata/probe/fixture.txt",
		"make/verb_nova-ci_probe.mk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("new-verb did not report writing %s:\n%s", want, out)
		}
		if _, err := os.Stat(filepath.Join(tree, filepath.FromSlash(want))); err != nil {
			t.Errorf("new-verb did not write %s: %v", want, err)
		}
	}

	// BUILDING: the copied tree builds cleanly
	runIn(t, tree, "go", "build", "./cmd/nova-ci/...")
	runIn(t, tree, "go", "vet", "./cmd/nova-ci/...")

	// TESTING: the fixture test passes
	if got := runIn(t, tree, "go", "test", "-v", "-count=1", "-run", "TestCmdProbe", "./cmd/nova-ci"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("go test of the CLI verb skeleton did not pass:\n%s", got)
	}

	// Makefile integration: make test-verb-nova-ci-probe runs and passes
	if got := runIn(t, tree, "make", "-f", "Makefile", "test-verb-nova-ci-probe"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("make test-verb-nova-ci-probe did not pass:\n%s", got)
	}

	// Write discipline: second run refuses rather than overwrite
	cmd := exec.Command(bin, "new-verb", "--root", tree, "nova-ci", "probe")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "already exists") {
		t.Errorf("second new-verb run was not refused (err %v):\n%s", err, got)
	}

	// Invalid names are refused
	cmd = exec.Command(bin, "new-verb", "--root", tree, "nova-ci", "Invalid Verb!")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "verb name") {
		t.Errorf("new-verb with invalid verb name was not refused (err %v):\n%s", err, got)
	}
}

func buildCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-ci")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = goenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build nova-ci: %v\n%s", err, out)
	}
	return bin
}

func runScaffoldCmd(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", bin, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runIn(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s in %s: %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func copyScaffoldTree(t *testing.T, from, to string, keep func(rel string) bool) {
	t.Helper()
	err := filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if info.IsDir() {
			return nil
		}
		if !keep(filepath.ToSlash(rel)) {
			return nil
		}
		copyScaffoldFile(t, p, filepath.Join(to, rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyScaffoldFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}
