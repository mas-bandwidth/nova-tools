package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The selection verbs run against a selFake: every process they would start is
// answered from a table keyed by its command line, and no test here starts git,
// go, gofmt, make or a network connection.

type selReply struct {
	out  string
	err  string
	code int
}

type selFake struct {
	mu      sync.Mutex
	replies map[string]selReply
	calls   []string
	envs    map[string][]string
	cores   int
	paths   map[string]string
	client  selHTTP
}

func newSelFake(replies map[string]selReply) *selFake {
	return &selFake{replies: replies, envs: map[string][]string{}, cores: 16, paths: map[string]string{}}
}

// answer is the reply table as the answer of the one fake runner
// (cmdrun_fake_test.go): the command line, with a GOOS= entry of its
// environment in front, keys the reply.
func (f *selFake) answer(c cmdSpec) (string, int, error) {
	key := strings.Join(append([]string{c.Name}, c.Args...), " ")
	for _, e := range c.Env {
		if strings.HasPrefix(e, "GOOS=") {
			key = e + " " + key
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.envs[key] = c.Env
	r, ok := f.replies[key]
	f.mu.Unlock()
	if !ok {
		return "", 0, fmt.Errorf("selFake: no reply for %q", key)
	}
	if c.Stderr != nil {
		io.WriteString(c.Stderr, r.err)
	}
	return r.out, r.code, nil
}

func (f *selFake) host() selHost {
	return selHost{
		r:      &fakeCmdRunner{answer: f.answer, onPath: f.paths},
		cores:  func() int { return f.cores },
		client: f.client,
	}
}

func (f *selFake) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// selRun runs a verb with the given environment and returns its exit code and
// both streams. dir is the working directory of the verb.
func selRun(do func(env, []string) int, dir string, vars map[string]string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	e := env{stdout: &out, stderr: &errb, dir: dir, getenv: func(k string) string { return vars[k] }}
	code := do(e, args)
	return code, out.String(), errb.String()
}

const selMod = "example.com/m"

// selRepo is a directory with a go.mod and a deprecated list naming cmd/gone.
func selRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                 "module " + selMod + "\n\ngo 1.26\n",
		pkgselect.DeprecatedFile: "cmd/gone\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func selImports(pkgs ...string) string {
	var b strings.Builder
	for _, p := range pkgs {
		b.WriteString(selMod + "/" + p + "\n")
	}
	return b.String()
}

func selRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	selListTree = "go list ./cmd/... ./internal/... ./tools/..."
	selListDeps = "go list -f {{.ImportPath}}{{range .Deps}} {{.}}{{end}} ./cmd/... ./internal/... ./tools/..."
	selFiles    = "{{.ImportPath}} {{.GoFiles}} {{.CgoFiles}} {{.TestGoFiles}} {{.XTestGoFiles}}"
)

func TestSelectPackagesVerbPrintsTheWholeTreeWithAll(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{selListTree: {out: selImports("cmd/a", "cmd/gone", "internal/b")}})
	code, out, errb := selRun(func(e env, a []string) int { return selectPackagesVerb(e, a, f.host()) }, selRepo(t), nil, "--all")
	if code != 0 || out != "./cmd/a\n./internal/b\n" || errb != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestSelectPackagesVerbFailsOnAGoListFailureByDefault(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{selListTree: {err: "open /c/go-build/x: no such file or directory\n", code: 1}})
	code, out, errb := selRun(func(e env, a []string) int { return selectPackagesVerb(e, a, f.host()) }, selRepo(t), nil, "--all")
	if code != 1 || out != "" || !strings.HasPrefix(errb, "ERROR select-packages: go list failed; failing the job rather than testing nothing") || !strings.Contains(errb, "no such file or directory") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestSelectPackagesVerbFallsBackToTheTreeWithAWarning(t *testing.T) {
	t.Parallel()
	repo := selRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "a", "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newSelFake(map[string]selReply{
		selListTree: {err: "go: cannot find main module\n"},
		"git ls-files -z -- cmd/*.go internal/*.go tools/*.go": {out: "cmd/a/a.go\x00"},
	})
	code, out, errb := selRun(func(e env, a []string) int { return selectPackagesVerb(e, a, f.host()) }, repo, nil, "--on-go-list-error=whole-tree", "--all")
	if code != 0 || out != "./cmd/a\n" || errb != "WARN select-packages: go list failed (go: cannot find main module); testing the whole tree\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestSelectPackagesVerbRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"an unknown mode", []string{"--on-go-list-error=sometimes", "--all"}, "select-packages: unknown --on-go-list-error=sometimes; want fail or whole-tree"},
		{"no base and no --all", nil, "want --all or exactly one base sha"},
		{"two bases", []string{"a", "b"}, "want --all or exactly one base sha"},
		{"--all and a base", []string{"--all", "a"}, "--all takes no base sha"},
		{"an unknown flag", []string{"--bogus"}, "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSelFake(nil)
			code, out, errb := selRun(func(e env, a []string) int { return selectPackagesVerb(e, a, f.host()) }, selRepo(t), nil, tc.args...)
			if code != 2 || out != "" || !strings.Contains(errb, tc.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 2 and %q", code, out, errb, tc.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("a refused call started %v", f.calls)
			}
		})
	}
}

func TestSelectPackagesVerbHelpIsTheVerbsHelp(t *testing.T) {
	t.Parallel()
	code, out, _ := selRun(func(e env, a []string) int { return selectPackagesVerb(e, a, newSelFake(nil).host()) }, "", nil, "-h")
	if code != 0 || out != registry["select-packages"].help {
		t.Errorf("-h: exit %d, printed %q", code, out)
	}
}

func matrixRepoFake(t *testing.T, extra map[string]selReply) (*selFake, string) {
	t.Helper()
	answers := map[string]selReply{
		selListTree: {out: selImports("cmd/a", "cmd/nova-bus", "cmd/nova-sandbox", "internal/b", "internal/ci", "internal/docs")},
	}
	for k, v := range extra {
		answers[k] = v
	}
	return newSelFake(answers), selRepo(t)
}

// A push tests the whole tree: the heavy package first, the functional list
// over the Linux legs, every package on both OSes.
func TestTestMatrixPushDealsTheWholeTree(t *testing.T) {
	t.Parallel()
	f, repo := matrixRepoFake(t, nil)
	gh := filepath.Join(t.TempDir(), "output")
	code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, f.host()) }, repo,
		map[string]string{"GITHUB_OUTPUT": gh}, "--event", "push", "--target-branch", "dev", "--linux-group", "lin", "--macos-group", "mac")
	if code != 0 || errb != "" {
		t.Fatalf("exit %d, stderr %q\n%s", code, errb, out)
	}
	wantFunctional := `[{"name":"1/4 lin","packages":"./cmd/nova-bus ./internal/docs"},{"name":"2/4 lin","packages":"./cmd/a"},{"name":"3/4 lin","packages":"./internal/b"},{"name":"4/4 lin","packages":"./internal/ci"}]`
	wantPackages := `[{"name":"1/8 lin","packages":"./cmd/nova-bus","os":"linux","arch":"x64","group":"lin"},{"name":"2/8 lin","packages":"./cmd/a","os":"linux","arch":"x64","group":"lin"},{"name":"3/8 lin","packages":"./internal/b","os":"linux","arch":"x64","group":"lin"},{"name":"4/8 lin","packages":"./internal/ci","os":"linux","arch":"x64","group":"lin"},{"name":"5/8 lin","packages":"./internal/docs","os":"linux","arch":"x64","group":"lin"},` +
		`{"name":"1/8 darwin-arm64","packages":"./cmd/nova-bus","os":"macOS","arch":"ARM64","group":"mac"},{"name":"2/8 darwin-arm64","packages":"./cmd/a","os":"macOS","arch":"ARM64","group":"mac"},{"name":"3/8 darwin-arm64","packages":"./cmd/nova-sandbox","os":"macOS","arch":"ARM64","group":"mac"},{"name":"4/8 darwin-arm64","packages":"./internal/b","os":"macOS","arch":"ARM64","group":"mac"},{"name":"5/8 darwin-arm64","packages":"./internal/ci","os":"macOS","arch":"ARM64","group":"mac"},{"name":"6/8 darwin-arm64","packages":"./internal/docs","os":"macOS","arch":"ARM64","group":"mac"}]`
	if got := selRead(t, gh); got != "functional="+wantFunctional+"\npackages="+wantPackages+"\n" {
		t.Errorf("GITHUB_OUTPUT =\n%s\nwant functional and packages:\n%s\n%s", got, wantFunctional, wantPackages)
	}
	if !strings.Contains(out, "functional: "+wantFunctional+"\n") || !strings.HasSuffix(out, wantPackages+"\n") || strings.Contains(out, "touched") {
		t.Errorf("stdout =\n%s", out)
	}
	if f.called("git") {
		t.Error("a whole-tree run read a diff")
	}
}

// A pull request selects against its own base, keeps macOS legs for the code
// that differs there, and says which.
func TestTestMatrixPullRequestSelectsAgainstItsBase(t *testing.T) {
	t.Parallel()
	f, repo := matrixRepoFake(t, map[string]selReply{
		"git cat-file -e basesha^{commit}":  {},
		"git diff --name-only basesha HEAD": {out: "cmd/a/a.go\n"},
		selListDeps:                         {out: selMod + "/cmd/a fmt\n" + selMod + "/cmd/nova-bus fmt\n" + selMod + "/internal/ci fmt\n" + selMod + "/internal/docs fmt\n"},
		"go list -m":                        {out: selMod + "\n"},
		"GOOS=linux go list -f " + selFiles + " ./cmd/... ./internal/...":                            {out: selMod + "/cmd/a [a.go] [] [] []\n" + selMod + "/cmd/nova-bus [b.go] [] [] []\n"},
		"GOOS=darwin go list -f " + selFiles + " ./cmd/... ./internal/...":                           {out: selMod + "/cmd/a [a.go a_darwin.go] [] [] []\n" + selMod + "/cmd/nova-bus [b.go] [] [] []\n"},
		"GOOS=darwin go list -test -f {{.ImportPath}} {{join .Deps \" \"}} ./cmd/... ./internal/...": {out: selMod + "/cmd/a fmt\n" + selMod + "/cmd/nova-bus fmt\n"},
	})
	gh := filepath.Join(t.TempDir(), "output")
	code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, f.host()) }, repo,
		map[string]string{"GITHUB_OUTPUT": gh}, "--event", "pull_request", "--pull-request-base", "basesha", "--merge-group-base", "ignored", "--target-branch", "dev", "--linux-group", "lin", "--macos-group", "mac")
	if code != 0 || errb != "" {
		t.Fatalf("exit %d, stderr %q\n%s", code, errb, out)
	}
	for _, want := range []string{
		"pull_request: 3 package(s) touched: ./cmd/a ./internal/ci ./internal/docs\n",
		"darwin-specific (own files or an import differ under GOOS=darwin): ./cmd/a \n",
		`{"name":"1/4 darwin-arm64","packages":"./cmd/a","os":"macOS","arch":"ARM64","group":"mac"}`,
		`{"name":"1/4 lin","packages":"./cmd/a","os":"linux","arch":"x64","group":"lin"},{"name":"2/4 lin","packages":"./internal/ci","os":"linux","arch":"x64","group":"lin"},{"name":"3/4 lin","packages":"./internal/docs","os":"linux","arch":"x64","group":"lin"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if f.called("git cat-file -e ignored") {
		t.Error("a pull request read the merge group's base")
	}
}

// A change bound for a working branch meets Linux only: every package on the
// Linux legs, the darwin-only packages dropped before the nothing-to-test
// check, the darwin-sensitivity analysis not run.
func TestTestMatrixWithTheDarwinGateOffIsLinuxOnly(t *testing.T) {
	t.Parallel()
	f, repo := matrixRepoFake(t, nil)
	gh := filepath.Join(t.TempDir(), "output")
	code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, f.host()) }, repo,
		map[string]string{"GITHUB_OUTPUT": gh}, "--event", "push", "--target-branch", "sprint/foundation", "--linux-group", "lin", "--macos-group", "mac")
	require.Equal(t, 0, code, "stderr %q\n%s", errb, out)
	assert.Contains(t, out, "darwin shards: false (push -> sprint/foundation; on for main dev)\n")
	assert.Contains(t, out, "darwin shards off: 5 package(s) on the Linux shards: ./cmd/a ./cmd/nova-bus ./internal/b ./internal/ci ./internal/docs\n")
	got := selRead(t, gh)
	assert.NotContains(t, got, "macOS")
	assert.NotContains(t, got, "nova-sandbox")
	assert.False(t, f.called("GOOS="), "the darwin-sensitivity analysis ran with the gate off")
}

// A pull request whose selection is only darwin-only packages falls to the one
// nothing leg while the gate is off, and keeps its darwin leg while it is on.
func TestTestMatrixDarwinOnlyChangeIsNothingUntilItReachesDev(t *testing.T) {
	t.Parallel()
	answers := func() map[string]selReply {
		return map[string]selReply{
			"git cat-file -e basesha^{commit}":  {},
			"git diff --name-only basesha HEAD": {out: "cmd/nova-sandbox/main.go\n"},
			selListTree:                         {out: selImports("cmd/nova-sandbox", "internal/ci", "internal/docs")},
			selListDeps:                         {out: selMod + "/cmd/nova-sandbox fmt\n" + selMod + "/internal/ci fmt\n" + selMod + "/internal/docs fmt\n"},
		}
	}
	run := func(target string) string {
		f := newSelFake(answers())
		code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, f.host()) }, selRepo(t),
			map[string]string{"GITHUB_OUTPUT": filepath.Join(t.TempDir(), "o")}, "--event", "merge_group", "--merge-group-base", "basesha", "--target-branch", target, "--linux-group", "lin", "--macos-group", "mac")
		require.Equal(t, 0, code, "stderr %q\n%s", errb, out)
		return out
	}
	assert.Contains(t, run("refs/heads/sprint/foundation"), "darwin shards off: 2 package(s) on the Linux shards: ./internal/ci ./internal/docs\n")
	assert.Contains(t, run("refs/heads/dev"), `"os":"macOS"`)
}

// A change that touches no Go package still gets the two class-test packages,
// so a docs-only pull request is never "nothing": the nothing leg is for a
// selection that really is empty.
func TestTestMatrixNothingToTestIsOneLegThatExitsZero(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{
		"git cat-file -e basesha^{commit}":  {},
		"git diff --name-only basesha HEAD": {out: "README.md\n"},
		selListTree:                         {out: selImports("cmd/gone")},
		selListDeps:                         {out: selMod + "/cmd/gone fmt\n"},
	})
	gh := filepath.Join(t.TempDir(), "output")
	code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, f.host()) }, selRepo(t),
		map[string]string{"GITHUB_OUTPUT": gh}, "--event", "merge_group", "--merge-group-base", "basesha", "--linux-group", "lin", "--macos-group", "mac")
	if code != 0 || errb != "" {
		t.Fatalf("exit %d, stderr %q\n%s", code, errb, out)
	}
	want := `functional=[{"name":"nothing","packages":""}]` + "\n" + `packages=[{"name":"nothing","packages":"","os":"linux","arch":"x64","group":"lin"}]` + "\n"
	if got := selRead(t, gh); got != want {
		t.Errorf("GITHUB_OUTPUT = %q, want %q", got, want)
	}
	if !strings.Contains(out, "merge_group: 0 package(s) touched: none\n") || !strings.Contains(out, "nothing to test for this change\n") {
		t.Errorf("stdout =\n%s", out)
	}
}

func TestTestMatrixSelectionFailureFailsTheStepOnAPullRequestOnly(t *testing.T) {
	t.Parallel()
	answers := func() map[string]selReply {
		return map[string]selReply{
			"git cat-file -e basesha^{commit}":  {},
			"git diff --name-only basesha HEAD": {out: "cmd/a/a.go\n"},
			selListTree:                         {err: "open /c/go-build/x: no such file or directory\n", code: 1},
		}
	}
	code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, newSelFake(answers()).host()) }, selRepo(t),
		map[string]string{"GITHUB_OUTPUT": filepath.Join(t.TempDir(), "o")}, "--event", "pull_request", "--pull-request-base", "basesha", "--linux-group", "lin", "--macos-group", "mac")
	if code != 1 || !strings.Contains(errb, "ERROR select-packages: go list failed") || !strings.Contains(out, "select-packages failed; see its error above") {
		t.Errorf("pull_request: exit %d, stdout %q, stderr %q; want a failed step", code, out, errb)
	}

	// merge_group falls back to the whole tree read from the tracked files
	repo := selRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "a", "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := answers()
	a["git ls-files -z -- cmd/*.go internal/*.go tools/*.go"] = selReply{out: "cmd/a/a.go\x00"}
	code, out, errb = selRun(func(e env, args []string) int { return testMatrixVerb(e, args, newSelFake(a).host()) }, repo,
		map[string]string{"GITHUB_OUTPUT": filepath.Join(t.TempDir(), "o")}, "--event", "merge_group", "--merge-group-base", "basesha", "--linux-group", "lin", "--macos-group", "mac")
	if code != 0 || !strings.Contains(errb, "WARN select-packages: go list failed (open /c/go-build/x: no such file or directory); testing the whole tree") || !strings.Contains(out, "merge_group: 1 package(s) touched: ./cmd/a\n") {
		t.Errorf("merge_group: exit %d, stdout %q, stderr %q; want the warning and the whole tree", code, out, errb)
	}
}

func TestTestMatrixRefusals(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no event":         {"--linux-group", "l", "--macos-group", "m"},
		"no linux group":   {"--event", "push", "--macos-group", "m"},
		"no macos group":   {"--event", "push", "--linux-group", "l"},
		"an extra operand": {"--event", "push", "--linux-group", "l", "--macos-group", "m", "x"},
	} {
		code, out, errb := selRun(func(e env, a []string) int { return testMatrixVerb(e, a, newSelFake(nil).host()) }, selRepo(t), nil, args...)
		if code != 2 || out != "" || errb == "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want a refusal", name, code, out, errb)
		}
	}
}

func TestDealVerbWritesThisShardsLine(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{"go list ./...": {out: selImports("cmd/a", "cmd/nova-bus", "cmd/gone", "internal/b", "cmd/c", "internal/d")}})
	gh := filepath.Join(t.TempDir(), "env")
	repo := selRepo(t)
	var all []string
	for shard := 1; shard <= 2; shard++ {
		code, out, errb := selRun(func(e env, a []string) int { return dealVerb(e, a, f.host()) }, repo,
			map[string]string{"GITHUB_ENV": gh}, "--shards", "2", "--shard", fmt.Sprint(shard), "--heavy", "cmd/nova-bus")
		if code != 0 || errb != "" || !strings.HasSuffix(out, " \n") {
			t.Fatalf("shard %d: exit %d, stdout %q, stderr %q", shard, code, out, errb)
		}
		all = append(all, strings.Fields(out)...)
	}
	if got := selRead(t, gh); got != "HOSTED_PKGS="+selMod+"/cmd/nova-bus "+selMod+"/internal/b "+selMod+"/internal/d \nHOSTED_PKGS="+selMod+"/cmd/a "+selMod+"/cmd/c \n" {
		t.Errorf("GITHUB_ENV = %q", got)
	}
	if len(all) != 5 {
		t.Errorf("the two shards dealt %v, want the five live packages once each", all)
	}
}

func TestDealVerbRefusals(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no shards":        {"--shard", "1"},
		"shard over":       {"--shards", "2", "--shard", "3"},
		"shard zero":       {"--shards", "2", "--shard", "0"},
		"an extra operand": {"--shards", "2", "--shard", "1", "x"},
	} {
		code, _, errb := selRun(func(e env, a []string) int { return dealVerb(e, a, newSelFake(nil).host()) }, selRepo(t), nil, args...)
		if code != 2 || errb == "" {
			t.Errorf("%s: exit %d, stderr %q; want a refusal", name, code, errb)
		}
	}
	f := newSelFake(map[string]selReply{"go list ./...": {err: "go: no go.mod\n", code: 1}})
	code, _, errb := selRun(func(e env, a []string) int { return dealVerb(e, a, f.host()) }, selRepo(t), map[string]string{"GITHUB_ENV": filepath.Join(t.TempDir(), "e")}, "--shards", "2", "--shard", "1")
	if code != 1 || !strings.Contains(errb, "no go.mod") {
		t.Errorf("a failed go list: exit %d, stderr %q", code, errb)
	}
}

func TestRaceDepsVerbBuildsTheDependenciesUnderRace(t *testing.T) {
	t.Parallel()
	const goListDeps = "go list -deps -test -f {{if not .Standard}}{{if not .Module.Main}}{{.ImportPath}}{{end}}{{end}} "
	f := newSelFake(map[string]selReply{
		"go list ./...":                              {out: selImports("cmd/a", "cmd/gone")},
		goListDeps + selMod + "/cmd/a":               {out: "example.org/z\nexample.org/a\n"},
		"go build -race example.org/a example.org/z": {out: "built\n"},
	})
	code, out, errb := selRun(func(e env, a []string) int { return raceDepsVerb(e, a, f.host()) }, selRepo(t), nil)
	if code != 0 || out != "2 external packages\nbuilt\n" || errb != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
	f = newSelFake(map[string]selReply{
		"go list ./...":                {out: selImports("cmd/a")},
		goListDeps + selMod + "/cmd/a": {out: "example.org/a\n"},
		"go build -race example.org/a": {err: "build failed\n", code: 1},
	})
	code, _, errb = selRun(func(e env, a []string) int { return raceDepsVerb(e, a, f.host()) }, selRepo(t), nil)
	if code != 1 || !strings.Contains(errb, "build failed") || !strings.Contains(errb, "exited 1") {
		t.Errorf("a failed build: exit %d, stderr %q", code, errb)
	}
}

func TestPerfTestsVerbWritesTheRunsAndThePackages(t *testing.T) {
	t.Parallel()
	repo := selRepo(t)
	tmp := t.TempDir()
	dir := filepath.Join(repo, "internal", "perfy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte("//go:build perf\n\npackage perfy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newSelFake(map[string]selReply{
		"go list -tags perf -f {{.ImportPath}} {{.Dir}} ./...":     {out: selMod + "/internal/perfy " + dir + "\n"},
		"go test -list . " + selMod + "/internal/perfy":            {out: "TestA\n"},
		"go test -tags perf -list . " + selMod + "/internal/perfy": {out: "TestA\nTestSlow\n"},
	})
	gh := filepath.Join(t.TempDir(), "env")
	code, out, errb := selRun(func(e env, a []string) int { return perfTestsVerb(e, a, f.host()) }, repo, map[string]string{"RUNNER_TEMP": tmp, "GITHUB_ENV": gh})
	if code != 0 || errb != "" || out != selMod+"/internal/perfy: TestSlow\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
	if got := selRead(t, filepath.Join(tmp, "perf-runs")); got != selMod+"/internal/perfy ^(TestSlow)$\n" {
		t.Errorf("perf-runs = %q", got)
	}
	if got := selRead(t, gh); got != "PERF_PKGS="+selMod+"/internal/perfy \n" {
		t.Errorf("GITHUB_ENV = %q", got)
	}
}

// With no perf-tagged test in the live tree the job would assert nothing, so
// it is red.
func TestPerfTestsVerbIsRedWhenTheTreeHasNone(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{"go list -tags perf -f {{.ImportPath}} {{.Dir}} ./...": {out: selMod + "/cmd/a " + t.TempDir() + "\n"}})
	code, out, _ := selRun(func(e env, a []string) int { return perfTestsVerb(e, a, f.host()) }, selRepo(t), map[string]string{"RUNNER_TEMP": t.TempDir(), "GITHUB_ENV": filepath.Join(t.TempDir(), "e")})
	if code != 1 || out != "no live package holds a perf-tagged test: this job would assert nothing\n" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
}

func TestRunnerShareIsTheCoresOverTheRunnersBetweenOneAndTwo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cores   int
		runners string
		want    int
		says    string
	}{
		{64, "", 2, "64 cores on this machine, 8 runners per machine (NOVA_RUNNERS_PER_MACHINE), this leg takes 2 (at most 2)"},
		{16, "8", 2, "16 cores on this machine, 8 runners per machine"},
		{12, "8", 1, "12 cores on this machine, 8 runners per machine (NOVA_RUNNERS_PER_MACHINE), this leg takes 1 (at most 2)"},
		{4, "8", 1, "this leg takes 1"},
		{1, "1", 1, "this leg takes 1"},
		{64, "1", 2, "64 cores on this machine, 1 runners per machine"},
		{16, "4", 2, "16 cores on this machine, 4 runners per machine"},
		{16, "0", 2, "16 cores on this machine, 8 runners per machine"},
		{16, "many", 2, "16 cores on this machine, 8 runners per machine"},
		{16, "-3", 2, "16 cores on this machine, 8 runners per machine"},
		{24, "12", 2, "24 cores on this machine, 12 runners per machine"},
	}
	for _, tc := range cases {
		f := newSelFake(nil)
		f.cores = tc.cores
		gh := filepath.Join(t.TempDir(), "env")
		code, out, errb := selRun(func(e env, a []string) int { return runnerShareVerb(e, a, f.host()) }, "",
			map[string]string{"GITHUB_ENV": gh, "NOVA_RUNNERS_PER_MACHINE": tc.runners})
		if code != 0 || errb != "" || !strings.Contains(out, tc.says) {
			t.Errorf("%d cores, runners %q: exit %d, stdout %q, stderr %q; want %q", tc.cores, tc.runners, code, out, errb, tc.says)
		}
		if got := selRead(t, gh); got != fmt.Sprintf("GOMAXPROCS=%d\n", tc.want) {
			t.Errorf("%d cores, runners %q: GITHUB_ENV = %q, want GOMAXPROCS=%d", tc.cores, tc.runners, got, tc.want)
		}
	}
}

func TestRunnerShareNeedsGithubEnv(t *testing.T) {
	t.Parallel()
	code, _, errb := selRun(func(e env, a []string) int { return runnerShareVerb(e, a, newSelFake(nil).host()) }, "", nil)
	if code != 1 || !strings.Contains(errb, "GITHUB_ENV is not set") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
}

// The shim is run, not read: exit 86 and the reason on stderr.
func TestUnitTierShimRefusesRedisServer(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shim is a /bin/sh file on the Linux and macOS runners")
	}
	tmp := t.TempDir()
	gh := filepath.Join(t.TempDir(), "path")
	code, out, errb := selRun(unitTierShimVerb, "", map[string]string{"RUNNER_TEMP": tmp, "GITHUB_PATH": gh})
	shim := filepath.Join(tmp, "unit-tier-bin", "redis-server")
	if code != 0 || errb != "" || out != "redis-server on this leg is "+shim+" (exit 86)\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
	if got := selRead(t, gh); got != filepath.Dir(shim)+"\n" {
		t.Errorf("GITHUB_PATH = %q, want the shim's directory", got)
	}
	res, err := exec.Command(shim, "--port", "0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 || string(res) != pkgselect.UnitShimMessage+"\n" {
		t.Errorf("the shim: %v, output %q; want exit 86 and %q", err, res, pkgselect.UnitShimMessage)
	}
	if fi, err := os.Stat(shim); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the shim is not executable: %v %v", fi, err)
	}
}

func TestUnitTierShimNeedsRunnerTemp(t *testing.T) {
	t.Parallel()
	if code, _, errb := selRun(unitTierShimVerb, "", nil); code != 1 || !strings.Contains(errb, "RUNNER_TEMP is not set") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
}

type selRoundTrip func(*http.Request) (*http.Response, error)

func (f selRoundTrip) Do(r *http.Request) (*http.Response, error) { return f(r) }

func selJSON(status int, body string) selHTTP {
	return selRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

// Any trouble with the API reads as "not proved", never as a skip.
func TestProvedByMergeGroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		client selHTTP
		runs   int
	}{
		{"a successful run exists", selJSON(200, `{"total_count": 1, "workflow_runs": []}`), 1},
		{"several", selJSON(200, `{"total_count":3}`), 3},
		{"none", selJSON(200, `{"total_count": 0}`), 0},
		{"a rate limit answers with a message", selJSON(403, `{"message":"API rate limit exceeded"}`), 0},
		{"a server error with a count in it", selJSON(500, `{"total_count": 9}`), 0},
		{"a body that is not JSON", selJSON(200, `<html>`), 0},
		{"no count", selJSON(200, `{}`), 0},
		{"a transport error", selRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSelFake(nil)
			f.client = tc.client
			gh := filepath.Join(t.TempDir(), "output")
			code, out, errb := selRun(func(e env, a []string) int { return provedVerb(e, a, f.host()) }, "",
				map[string]string{"GH_TOKEN": "tok", "GITHUB_OUTPUT": gh}, "--repo", "owner/name", "--sha", "abc123")
			proved := tc.runs > 0
			if code != 0 || errb != "" || out != fmt.Sprintf("PROVED sha=abc123 merge_group_runs=%d proved=%t\n", tc.runs, proved) {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
			}
			if got := selRead(t, gh); got != fmt.Sprintf("proved=%t\n", proved) {
				t.Errorf("GITHUB_OUTPUT = %q", got)
			}
		})
	}
}

func TestProvedByMergeGroupAsksForASuccessfulRunOfThisSha(t *testing.T) {
	t.Parallel()
	var got *http.Request
	f := newSelFake(nil)
	f.client = selRoundTrip(func(r *http.Request) (*http.Response, error) {
		got = r
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"total_count":1}`))}, nil
	})
	selRun(func(e env, a []string) int { return provedVerb(e, a, f.host()) }, "",
		map[string]string{"GH_TOKEN": "tok", "GITHUB_OUTPUT": filepath.Join(t.TempDir(), "o")}, "--repo", "owner/name", "--sha", "abc123")
	if got == nil {
		t.Fatal("no request was made")
	}
	if got.Method != http.MethodGet || got.URL.String() != "https://api.github.com/repos/owner/name/actions/runs?event=merge_group&head_sha=abc123&per_page=1&status=success" {
		t.Errorf("request = %s %s", got.Method, got.URL)
	}
	if got.Header.Get("Authorization") != "Bearer tok" || got.Header.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("headers = %v", got.Header)
	}
	if _, ok := got.Context().Deadline(); !ok {
		t.Error("the ask has no deadline: it could hang the step")
	}
}

func TestProvedByMergeGroupRefusals(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{"no repo": {"--sha", "a"}, "no sha": {"--repo", "o/n"}} {
		if code, _, errb := selRun(func(e env, a []string) int { return provedVerb(e, a, newSelFake(nil).host()) }, "", nil, args...); code != 2 || errb == "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, errb)
		}
	}
}

func TestUnitTestRunsMakeTestOnceThePathHoldsTheShim(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	shim := filepath.Join(tmp, "unit-tier-bin", "redis-server")
	cases := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"a pull request or merge group", nil, "make test PKGS=./cmd/a ./cmd/b GOTEST_TIMEOUT=110s GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w"},
		{"a whole-tree push", map[string]string{"WHOLE_TREE_SLOWTESTS": "--budget 60 --sleeps ledger.txt"}, "make test PKGS=./cmd/a ./cmd/b GOTEST_TIMEOUT=110s GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w SLOWTESTS_FLAGS=--budget 60 --sleeps ledger.txt"},
		{"the nightly leg", map[string]string{"NIGHTLY_ENFORCE": "1"}, "make test PKGS=./cmd/a ./cmd/b GOTEST_TIMEOUT=110s GOTEST_COUNT_FLAG=-count=1 GOTEST_LDFLAGS=-ldflags=-w SLOWTESTS_ENFORCE=1"},
		{"a non-nightly leg says 0", map[string]string{"NIGHTLY_ENFORCE": "0"}, "make test PKGS=./cmd/a ./cmd/b GOTEST_TIMEOUT=110s GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSelFake(map[string]selReply{tc.want: {out: "ok\n"}})
			f.paths["redis-server"] = shim
			vars := map[string]string{"RUNNER_TEMP": tmp}
			for k, v := range tc.vars {
				vars[k] = v
			}
			code, out, errb := selRun(func(e env, a []string) int { return unitTestVerb(e, a, f.host()) }, "", vars, "--packages", "./cmd/a ./cmd/b")
			if code != 0 || out != "ok\n" || errb != "" {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
			}
		})
	}
}

func TestUnitTestCarriesMakesExitStatus(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	f := newSelFake(map[string]selReply{"make test PKGS=./cmd/a GOTEST_TIMEOUT=110s GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w": {err: "CI-SLEEPS\n", code: 2}})
	f.paths["redis-server"] = filepath.Join(tmp, "unit-tier-bin", "redis-server")
	code, _, errb := selRun(func(e env, a []string) int { return unitTestVerb(e, a, f.host()) }, "", map[string]string{"RUNNER_TEMP": tmp}, "--packages", "./cmd/a")
	if code != 2 || errb != "CI-SLEEPS\n" {
		t.Errorf("exit %d, stderr %q; want make's 2 through", code, errb)
	}
}

// A redis-server that is not the refusing shim, or none, stops the leg before
// any test runs.
func TestUnitTestRefusesARealRedisServerOnPath(t *testing.T) {
	t.Parallel()
	for name, path := range map[string]string{"a real one": "/usr/bin/redis-server", "none": ""} {
		f := newSelFake(nil)
		if path != "" {
			f.paths["redis-server"] = path
		}
		code, out, _ := selRun(func(e env, a []string) int { return unitTestVerb(e, a, f.host()) }, "", map[string]string{"RUNNER_TEMP": t.TempDir()}, "--packages", "./cmd/a")
		if code != 1 || out != "unit tier: redis-server on PATH is "+path+", not the refusing shim\n" {
			t.Errorf("%s: exit %d, stdout %q", name, code, out)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: make ran: %v", name, f.calls)
		}
	}
}

func TestUnitTestNeedsPackages(t *testing.T) {
	t.Parallel()
	if code, _, errb := selRun(func(e env, a []string) int { return unitTestVerb(e, a, newSelFake(nil).host()) }, "", nil); code != 2 || !strings.Contains(errb, "--packages is required") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
}

const (
	ancestryShallow   = "git rev-parse --is-shallow-repository"
	ancestryParents   = "git cat-file -p HEAD"
	ancestryFetchDev  = "git fetch --no-tags --filter=blob:none --unshallow origin +dev:refs/remotes/origin/dev"
	ancestryFetchPlan = "git fetch --no-tags --filter=blob:none origin +sprint/foundation:refs/remotes/origin/sprint/foundation"
	ancestryFetchUnsh = "git fetch --no-tags --filter=blob:none --unshallow origin +sprint/foundation:refs/remotes/origin/sprint/foundation"
	ancestryLsRemote  = "git ls-remote --exit-code --heads origin sprint/foundation"
	ancestryAbsentErr = "fatal: couldn't find remote ref sprint/foundation\n"
)

// A shallow checkout is completed; a complete one takes a plain fetch (--unshallow
// on a complete repository is fatal).
func TestFetchAncestryUnshallowsOnlyAShallowCheckout(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{ancestryShallow: {out: "true\n"}, ancestryFetchDev: {out: "fetched\n"}})
	code, out, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", nil, "dev")
	if code != 0 || out != "fetched\n" || errb != "" {
		t.Errorf("shallow: exit %d, stdout %q, stderr %q", code, out, errb)
	}
	f = newSelFake(map[string]selReply{
		ancestryShallow: {out: "false\n"},
		"git fetch --no-tags --filter=blob:none origin +dev:refs/remotes/origin/dev": {},
	})
	if code, _, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", nil, "dev"); code != 0 || errb != "" {
		t.Errorf("complete: exit %d, stderr %q", code, errb)
	}
}

// A one-parent commit on a run that is not a pull request has no promotion to
// read, so nothing is fetched; a pull request always fetches.
func TestFetchAncestryPromotionSkipsAOneParentCommit(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{ancestryParents: {out: "tree abc123\nparent def456\n\nsubject\n"}})
	code, out, _ := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", map[string]string{"GITHUB_EVENT_NAME": "push"}, "--promotion", "sprint/foundation")
	if code != 0 || out != "a one-parent commit: no promotion to read\n" || f.called("git fetch") {
		t.Errorf("one parent on a push: exit %d, stdout %q, calls %v", code, out, f.calls)
	}
	f = newSelFake(map[string]selReply{ancestryParents: {out: "tree abc123\nparent def456\nparent fed789\n\nsubject\n"}, ancestryLsRemote: {out: "abc123\trefs/heads/sprint/foundation\n"}, ancestryShallow: {out: "false\n"}, ancestryFetchPlan: {}})
	if code, out, _ := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", map[string]string{"GITHUB_EVENT_NAME": "merge_group"}, "--promotion", "sprint/foundation"); code != 0 || out != "" || !f.called("git fetch") {
		t.Errorf("a merge commit: exit %d, stdout %q, calls %v", code, out, f.calls)
	}
	f = newSelFake(map[string]selReply{ancestryLsRemote: {out: "abc123\trefs/heads/sprint/foundation\n"}, ancestryShallow: {out: "false\n"}, ancestryFetchPlan: {}})
	if code, _, _ := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", map[string]string{"GITHUB_EVENT_NAME": "pull_request"}, "--promotion", "sprint/foundation"); code != 0 || f.called("git cat-file") || !f.called("git fetch") {
		t.Errorf("a pull request: exit %d, calls %v; want a fetch and no parent count", code, f.calls)
	}
}

// Stored headers preserve a shallow merge's parents and exclude its message.
func TestFetchAncestryReadsStoredParentHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		commit string
		fetch  bool
	}{
		{"root", "tree abc123\n\nsubject\n", false},
		{"one parent", "tree abc123\nparent def456\n\nsubject\n", false},
		{"shallow merge", "tree abc123\nparent def456\nparent fed789\n\nsubject\n", true},
		{"parent in message", "tree abc123\nparent def456\n\nparent fed789\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSelFake(map[string]selReply{
				ancestryParents:   {out: tc.commit},
				ancestryLsRemote:  {out: "abc123\trefs/heads/sprint/foundation\n"},
				ancestryShallow:   {out: "true\n"},
				ancestryFetchUnsh: {},
			})
			code, _, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", map[string]string{"GITHUB_EVENT_NAME": "push"}, "--promotion", "sprint/foundation")
			require.Zero(t, code, errb)
			assert.Equal(t, tc.fetch, f.called("git fetch"))
		})
	}
}

// The promotion branch is optional: dev is the integration branch, and
// sprint/foundation exists only while a promotion is in flight. A --promotion
// fetch of a branch origin does not have says so and exits 0 (nothing to
// read); the classtests rule then excuses nothing, which is the safe side.
// Any other ls-remote failure, and a plain (non-promotion) fetch of a missing
// branch, stay red.
func TestFetchAncestryPromotionSaysSoWhenTheBranchIsAbsent(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{
		ancestryParents:   {out: "tree abc123\nparent def456\nparent fed789\n\nsubject\n"},
		ancestryLsRemote:  {code: 2},
		ancestryShallow:   {out: "true\n"},
		ancestryFetchUnsh: {err: ancestryAbsentErr, code: 128},
	})
	code, out, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", map[string]string{"GITHUB_EVENT_NAME": "push"}, "--promotion", "sprint/foundation")
	if code != 0 || out != "origin has no branch sprint/foundation: no promotion to read\n" || errb != "" || f.called("git fetch") {
		t.Errorf("absent branch: exit %d, stdout %q, stderr %q, calls %v; want 0, the saying, and no fetch", code, out, errb, f.calls)
	}
	// ls-remote failing for another reason (the network) is not "absent".
	f = newSelFake(map[string]selReply{ancestryParents: {out: "tree abc123\nparent def456\nparent fed789\n\nsubject\n"}, ancestryLsRemote: {err: "fatal: unable to access\n", code: 128}})
	if code, _, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", map[string]string{"GITHUB_EVENT_NAME": "push"}, "--promotion", "sprint/foundation"); code != 1 || !strings.Contains(errb, "unable to access") {
		t.Errorf("ls-remote failure: exit %d, stderr %q; want 1", code, errb)
	}
	// Without --promotion a missing branch is a failure, as before (dev's fetch on main).
	f = newSelFake(map[string]selReply{ancestryShallow: {out: "true\n"}, ancestryFetchDev: {err: "fatal: couldn't find remote ref dev\n", code: 128}})
	if code, _, _ := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", nil, "dev"); code != 1 {
		t.Errorf("plain fetch of a missing branch: exit %d; want 1", code)
	}
}

func TestFetchAncestryFailures(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{ancestryShallow: {out: "true\n"}, ancestryFetchDev: {err: "fatal: unable to access\n", code: 128}})
	code, _, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", nil, "dev")
	if code != 1 || !strings.Contains(errb, "unable to access") || !strings.Contains(errb, "exited 128") {
		t.Errorf("a failed fetch: exit %d, stderr %q", code, errb)
	}
	f = newSelFake(map[string]selReply{ancestryShallow: {err: "fatal: not a git repository\n", code: 128}})
	if code, _, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, f.host()) }, "", nil, "dev"); code != 1 || !strings.Contains(errb, "not a git repository") {
		t.Errorf("not a repository: exit %d, stderr %q", code, errb)
	}
	for _, args := range [][]string{nil, {"a", "b"}, {" "}} {
		if code, _, errb := selRun(func(e env, a []string) int { return fetchAncestryVerb(e, a, newSelFake(nil).host()) }, "", nil, args...); code != 2 || errb == "" {
			t.Errorf("args %q: exit %d, stderr %q; want a refusal", args, code, errb)
		}
	}
}

func TestGofmtListsWhatIsNotFormatted(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{"gofmt -l .": {out: "cmd/a/a.go\ninternal/c/c.go\n"}})
	code, out, errb := selRun(func(e env, a []string) int { return gofmtVerb(e, a, f.host()) }, "", nil)
	if code != 1 || out != "not gofmt-clean:\ncmd/a/a.go\ninternal/c/c.go\n" || errb != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestGofmtIsSilentWhenTheTreeIsClean(t *testing.T) {
	t.Parallel()
	for name, listing := range map[string]string{"nothing": ""} {
		f := newSelFake(map[string]selReply{"gofmt -l .": {out: listing}})
		if code, out, errb := selRun(func(e env, a []string) int { return gofmtVerb(e, a, f.host()) }, "", nil); code != 0 || out != "" || errb != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errb)
		}
	}
}

// gofmt's own exit status is not the verb's: its output is. A file that does
// not parse is shown on stderr and the rest are still listed.
func TestGofmtShowsWhatGofmtSaysAboutAFileThatDoesNotParse(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{"gofmt -l .": {out: "a.go\n", err: "b.go:1:1: expected 'package'\n", code: 2}})
	code, out, errb := selRun(func(e env, a []string) int { return gofmtVerb(e, a, f.host()) }, "", nil)
	if code != 1 || out != "not gofmt-clean:\na.go\n" || errb != "b.go:1:1: expected 'package'\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
	code, _, errb = selRun(func(e env, a []string) int { return gofmtVerb(e, a, newSelFake(nil).host()) }, "", nil)
	if code != 2 || !strings.Contains(errb, "cannot run gofmt") {
		t.Errorf("no gofmt: exit %d, stderr %q", code, errb)
	}
}

func TestSandboxProbe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		reply  selReply
		code   int
		want   string
		fixHOM bool
	}{
		{"the sandboxed network answers", selReply{out: "200"}, 0, "PROBE OK runner=bench-1 slot=2 sandboxed-net=200\n", true},
		{"the network is refused", selReply{out: "000"}, 1, "PROBE FAIL runner=bench-1 slot=2 sandboxed-net=000\n", true},
		{"a status that is not 200", selReply{out: "403"}, 1, "PROBE FAIL runner=bench-1 slot=2 sandboxed-net=403\n", true},
		{"curl fails inside the sandbox", selReply{out: "000", code: 6}, 1, "PROBE FAIL runner=bench-1 slot=2 sandboxed-net=000 (exit 6)\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			repo := t.TempDir()
			np := filepath.Join(tmp, "np")
			probe := "./nova-sandbox --read " + repo + " --write " + np + " --cwd " + np + " -- curl -s -o /dev/null -w %{http_code} " + sandboxProbeURL
			f := newSelFake(map[string]selReply{"go build ./cmd/nova-sandbox": {}, probe: tc.reply})
			code, out, errb := selRun(func(e env, a []string) int { return sandboxProbeVerb(e, a, f.host()) }, repo,
				map[string]string{"RUNNER_TEMP": tmp, "RUNNER_NAME": "bench-1"}, "--slot", "2")
			if code != tc.code || out != tc.want || errb != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d %q", code, out, errb, tc.code, tc.want)
			}
			if env := f.envs[probe]; len(env) != 1 || env[0] != "HOME="+filepath.Join(np, "home") {
				t.Errorf("the probe's environment = %v, want a private HOME under RUNNER_TEMP", env)
			}
			if fi, err := os.Stat(filepath.Join(np, "home")); err != nil || !fi.IsDir() {
				t.Errorf("the probe's home was not made: %v", err)
			}
		})
	}
}

func TestSandboxProbeFailsWhenTheBuildFails(t *testing.T) {
	t.Parallel()
	f := newSelFake(map[string]selReply{"go build ./cmd/nova-sandbox": {err: "build error\n", code: 1}})
	code, out, errb := selRun(func(e env, a []string) int { return sandboxProbeVerb(e, a, f.host()) }, t.TempDir(), map[string]string{"RUNNER_TEMP": t.TempDir()}, "--slot", "1")
	if code != 1 || out != "" || !strings.Contains(errb, "build error") || !strings.Contains(errb, "go build ./cmd/nova-sandbox failed") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errb)
	}
	if f.called("./nova-sandbox") {
		t.Error("the probe ran after a failed build")
	}
}

func TestEverySelectionVerbIsRegisteredWithItsHelp(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"select-packages", "test-matrix", "deal", "race-deps", "perf-tests", "runner-share", "unit-tier-shim", "proved-by-merge-group", "unit-test", "fetch-ancestry", "gofmt", "sandbox-probe"} {
		v, ok := registry[n]
		if !ok || !strings.Contains(v.help, "example:") || !strings.Contains(v.help, "Exit 0") {
			t.Errorf("verb %s: registered %v, help needs an example and its exit codes", n, ok)
		}
	}
}
