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
// write confinement. new-verb refuses a tool with no func main and prints the
// exact dispatch case to add; it never edits the switch itself.
//
// Each test copies into its scratch tree only the module's packages that the
// patterns it builds depend on (go list -deps -test), never all of cmd/,
// internal/, tools/ and profiles/ (rowan hold 6 on #3616: 0.15 s -> 54 s), and
// the two tests run in parallel.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

func TestNewVerbYieldsABuildingTestingSkeleton(t *testing.T) {
	t.Parallel()
	_, bin, tree := scaffoldTree(t, "./cmd/nova-ci")

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

	// DISPATCH: new-verb prints the exact case to add and leaves the switch alone
	dispatch := "\tcase \"probe\":\n\t\treturn cmdProbe(args[1:], stdout, stderr)\n"
	if !strings.Contains(out, "add to the dispatch switch in cmd/nova-ci") || !strings.Contains(out, dispatch) {
		t.Errorf("new-verb did not print the dispatch case %q:\n%s", dispatch, out)
	}
	mainGo := filepath.Join(tree, "cmd", "nova-ci", "main.go")
	src, err := os.ReadFile(mainGo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "cmdProbe") {
		t.Errorf("new-verb edited the dispatch switch in %s; it must only print the case", mainGo)
	}

	// BUILDING: the copied tree builds cleanly
	runIn(t, tree, "go", "build", "./cmd/nova-ci")
	runIn(t, tree, "go", "vet", "./cmd/nova-ci")

	// TESTING: the fixture test passes
	if got := runIn(t, tree, "go", "test", "-v", "-count=1", "-run", "TestCmdProbe", "./cmd/nova-ci"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("go test of the CLI verb skeleton did not pass:\n%s", got)
	}

	// Makefile integration: make test-verb-nova-ci-probe runs and passes
	if got := runIn(t, tree, "make", "-f", "Makefile", "test-verb-nova-ci-probe"); !strings.Contains(got, "PASS") && !strings.Contains(got, "ok") {
		t.Errorf("make test-verb-nova-ci-probe did not pass:\n%s", got)
	}

	// The printed case is exact: pasted under the switch, the verb runs
	const sw = "\tswitch args[0] {\n"
	if !strings.Contains(string(src), sw) {
		t.Fatalf("%s has no %q to paste the case under", mainGo, sw)
	}
	if err := os.WriteFile(mainGo, []byte(strings.Replace(string(src), sw, sw+dispatch, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runIn(t, tree, "go", "run", "./cmd/nova-ci", "probe"); !strings.Contains(got, "nova-ci probe: OK") {
		t.Errorf("nova-ci probe after pasting the printed case did not run the verb:\n%s", got)
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

	// A tool with no func main is refused and nothing is written
	cmd = exec.Command(bin, "new-verb", "--root", tree, "ghost", "probe")
	cmd.Env = goenv.Clean(os.Environ())
	if got, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(got), "no func main") {
		t.Errorf("new-verb into a tool with no func main was not refused (err %v):\n%s", err, got)
	}
	if _, err := os.Stat(filepath.Join(tree, "cmd", "ghost")); err == nil {
		t.Errorf("new-verb into a tool with no func main wrote cmd/ghost")
	}
}

// scaffoldTree builds the CLI and lays out a scratch module holding go.mod,
// go.sum, the Makefile, and only the module packages that pattern depends on,
// tests included: each such package's top-level files plus its embed files.
func scaffoldTree(t *testing.T, pattern string) (root, bin, tree string) {
	t.Helper()
	for _, tool := range []string{"make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH", tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin = buildCLI(t)
	tree = t.TempDir()

	for _, f := range []string{"go.mod", "go.sum", "Makefile"} {
		src := filepath.Join(root, f)
		if _, err := os.Stat(src); err == nil {
			copyScaffoldFile(t, src, filepath.Join(tree, f))
		}
	}

	list := exec.Command("go", "list", "-deps", "-test", "-f",
		`{{.Dir}}{{range .EmbedFiles}}|{{.}}{{end}}{{range .TestEmbedFiles}}|{{.}}{{end}}`, pattern)
	list.Dir = root
	list.Env = goenv.Clean(os.Environ())
	out, err := list.Output()
	if err != nil {
		t.Fatalf("go list -deps -test %s: %v", pattern, err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "|")
		dir := parts[0]
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || seen[line] {
			continue // the standard library, the module cache, or seen already
		}
		seen[line] = true
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if e.Type().IsRegular() {
				copyScaffoldFile(t, filepath.Join(dir, e.Name()), filepath.Join(tree, rel, e.Name()))
			}
		}
		for _, emb := range parts[1:] {
			copyScaffoldFile(t, filepath.Join(dir, emb), filepath.Join(tree, rel, emb))
		}
	}
	return root, bin, tree
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
