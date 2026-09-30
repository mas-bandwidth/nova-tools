package pkgselect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake is a Runner answered from a table keyed by the command line (with
// "GOOS=x " in front when the call sets GOOS). No test here starts git or go.
type fake struct {
	mu      sync.Mutex
	answers map[string]Result
	calls   []string
}

// newFake answers the base check as "the base commit is here" unless the
// table says otherwise, so a test that is not about the fetch never meets one.
func newFake(answers map[string]Result) *fake {
	if answers == nil {
		answers = map[string]Result{}
	}
	if _, ok := answers[catFileCmd]; !ok {
		answers[catFileCmd] = Result{}
	}
	return &fake{answers: answers}
}

func (f *fake) run(dir string, env []string, argv ...string) (Result, error) {
	key := strings.Join(argv, " ")
	for _, e := range env {
		if strings.HasPrefix(e, "GOOS=") {
			key = e + " " + key
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.mu.Unlock()
	if r, ok := f.answers[key]; ok {
		return r, nil
	}
	return Result{}, fmt.Errorf("fake: no answer for %q", key)
}

func (f *fake) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

const (
	mod         = "example.com/m"
	listTree    = "go list ./cmd/... ./internal/... ./tools/..."
	listDeps    = "go list -f {{.ImportPath}}{{range .Deps}} {{.}}{{end}} ./cmd/... ./internal/... ./tools/..."
	diffCmd     = "git diff --name-only base HEAD"
	catFileCmd  = "git cat-file -e base^{commit}"
	shallowCmd  = "git rev-parse --is-shallow-repository"
	fetchCmd    = "git fetch -q origin base"
	fetchDeep   = "git fetch -q --depth=1 origin base"
	lsFilesCmd  = "git ls-files -z -- cmd/*.go internal/*.go tools/*.go"
	cacheErr    = "open /home/u/.cache/go-build/5e/5e1f-d: no such file or directory\n"
	wholeTreeIs = "./cmd/foo\n./internal/bar\n./internal/ci\n./internal/docs\n"
)

// tree makes the fixture repository: four packages, one behind a build tag, a
// testdata package and a deprecated one, and go.mod.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                       "module " + mod + "\n\ngo 1.26\n",
		"deprecated/PACKAGES":          "# the fixture's list\ncmd/gone\nkeep cmd/gone/kept\n",
		"cmd/foo/foo.go":               "package main\n",
		"cmd/gone/gone.go":             "package main\n",
		"internal/bar/bar.go":          "package bar\n",
		"internal/ci/ci.go":            "package ci\n",
		"internal/docs/docs.go":        "package docs\n",
		"internal/tagged/tagged.go":    "//go:build swarmtest\n\npackage tagged\n",
		"internal/bar/testdata/x/x.go": "package x\n",
		"internal/_skip/skip.go":       "package skip\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return root
}

func tracked() string {
	return strings.Join([]string{
		"cmd/foo/foo.go", "cmd/gone/gone.go", "internal/bar/bar.go", "internal/ci/ci.go", "internal/docs/docs.go",
		"internal/tagged/tagged.go", "internal/bar/testdata/x/x.go", "internal/_skip/skip.go",
	}, "\x00") + "\x00"
}

func imports(pkgs ...string) string {
	var b strings.Builder
	for _, p := range pkgs {
		b.WriteString(mod + "/" + p + "\n")
	}
	return b.String()
}

// depsListing is `go list -f ImportPath+Deps`: cmd/foo imports internal/bar,
// which imports nothing of ours; internal/ci and internal/docs import nothing.
const depsListing = mod + "/cmd/foo " + mod + "/internal/bar fmt\n" +
	mod + "/internal/bar fmt\n" +
	mod + "/internal/ci fmt\n" +
	mod + "/internal/docs fmt\n"

func TestDeprecatedPackagesAreDroppedAndKeepLinesKept(t *testing.T) {
	t.Parallel()
	d := ParseDeprecated("# list\ninternal/nsprint  # the prefix\nkeep internal/nsprint/store\n\nkeep\tinternal/nsprint/verbflag\ncmd/old\n", mod)
	in := []string{
		"./internal/nsprint/ws",
		mod + "/internal/nsprint/land/stream",
		"./internal/nsprint",
		"./internal/nsprint/store",
		mod + "/internal/nsprint/verbflag",
		"./internal/nsprintx",
		"./cmd/old",
		"./cmd/older",
		"cmd/old/sub",
		mod + "/cmd/nova-bus",
	}
	want := []string{
		"./internal/nsprint/store",
		mod + "/internal/nsprint/verbflag",
		"./internal/nsprintx",
		"./cmd/older",
		mod + "/cmd/nova-bus",
	}
	assert.Equal(t, want, d.Live(in), "a path drops that package and everything under it, a keep line keeps one, a name that only starts the same is another package")
}

func TestNoDeprecatedListKeepsEverything(t *testing.T) {
	t.Parallel()
	var d *Deprecated
	in := []string{"./a", "", mod + "/b"}
	assert.Equal(t, in, d.Live(in), "a nil list dropped something")
	got, err := LoadDeprecated(t.TempDir())
	assert.NoError(t, err)
	assert.Nil(t, got, "LoadDeprecated of a root with no list")
}

func TestLoadDeprecatedReadsTheModuleFromGoMod(t *testing.T) {
	t.Parallel()
	d, err := LoadDeprecated(tree(t))
	require.NoError(t, err)
	assert.False(t, d.LivePackage(mod+"/cmd/gone"), "the fixture list is not read as written")
	assert.True(t, d.LivePackage(mod+"/cmd/gone/kept"))
	assert.False(t, d.LivePackage("./cmd/gone/other"))
	assert.True(t, d.LivePackage("./cmd/goner"))
}

func TestSelectAllListsTheLiveTreeAsDotPaths(t *testing.T) {
	t.Parallel()
	root := tree(t)
	f := newFake(map[string]Result{
		listTree: {Stdout: imports("cmd/foo", "cmd/gone", "internal/bar", "internal/ci", "internal/docs")},
	})
	out, err := Select(f.run, Options{Root: root, All: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"./cmd/foo", "./internal/bar", "./internal/ci", "./internal/docs"}, out.Packages, "Select --all")
	assert.Empty(t, out.Warning)
	assert.False(t, f.called("git fetch"), "--all fetched a base")
}

// A change to cmd/foo selects it and its dependents, then internal/ci and
// internal/docs on every run, in go list order, and nothing deprecated.
func TestSelectChangeIsTheTouchedPackagesTheirDependentsAndTheClassTestPackages(t *testing.T) {
	t.Parallel()
	root := tree(t)
	cases := []struct {
		name, diff string
		want       []string
	}{
		{"a leaf edit selects its dependent", "internal/bar/bar.go\nREADME.md\n", []string{"./cmd/foo", "./internal/bar", "./internal/ci", "./internal/docs"}},
		{"a cmd edit selects it alone with the two class-test packages", "cmd/foo/foo.go\n", []string{"./cmd/foo", "./internal/ci", "./internal/docs"}},
		{"a docs-only change selects the two class-test packages", "docs/CLI.md\n.github/workflows/ci.yml\n", []string{"./internal/ci", "./internal/docs"}},
		{"an edit under deprecated is not selected", "deprecated/cmd/old/main.go\n", []string{"./internal/ci", "./internal/docs"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(map[string]Result{
				diffCmd:  {Stdout: tc.diff},
				listTree: {Stdout: imports("cmd/foo", "internal/bar", "internal/ci", "internal/docs")},
				listDeps: {Stdout: depsListing},
			})
			out, err := Select(f.run, Options{Root: root, Base: "base"})
			require.NoError(t, err)
			assert.Equal(t, tc.want, out.Packages)
			assert.Empty(t, out.Warning)
		})
	}
}

// The base is fetched only when it is missing, and --depth=1 only into a clone
// that is already shallow: a fetch with --depth shallows the clone it runs in,
// and `nova-ci local` runs here in a developer's own clone.
func TestSelectFetchesTheBaseOnlyWhenMissingAndNeverShallowsAFullClone(t *testing.T) {
	t.Parallel()
	root := tree(t)
	cases := []struct {
		name      string
		answers   map[string]Result
		wantFetch string // the fetch command expected, "" for none
	}{
		{"base present: no fetch", map[string]Result{catFileCmd: {}}, ""},
		{"base absent in a full clone: fetch without --depth", map[string]Result{catFileCmd: {Code: 128}, shallowCmd: {Stdout: "false\n"}, fetchCmd: {}}, fetchCmd},
		{"base absent in a shallow clone: fetch --depth=1", map[string]Result{catFileCmd: {Code: 128}, shallowCmd: {Stdout: "true\n"}, fetchDeep: {}}, fetchDeep},
		{"a failed fetch is not an error, the diff says", map[string]Result{catFileCmd: {Code: 128}, shallowCmd: {Stdout: "false\n"}, fetchCmd: {Code: 128}}, fetchCmd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.answers[diffCmd] = Result{Stdout: "docs/CLI.md\n"}
			tc.answers[listTree] = Result{Stdout: imports("cmd/foo", "internal/bar", "internal/ci", "internal/docs")}
			tc.answers[listDeps] = Result{Stdout: depsListing}
			f := newFake(tc.answers)
			_, err := Select(f.run, Options{Root: root, Base: "base"})
			require.NoError(t, err)
			if tc.wantFetch == "" {
				assert.False(t, f.called("git fetch"), "the base was here and was fetched")
				return
			}
			assert.True(t, f.called(tc.wantFetch), "want %q in %v", tc.wantFetch, f.calls)
			if tc.wantFetch == fetchCmd {
				assert.False(t, f.called("git fetch -q --depth"), "a full clone was shallowed")
			}
		})
	}
}

func TestSelectGoModChangePutsTheWholeTreeInScope(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"go.mod\n", "go.sum\n"} {
		f := newFake(map[string]Result{
			diffCmd:  {Stdout: "README.md\n" + changed},
			listTree: {Stdout: imports("cmd/foo", "internal/bar", "internal/ci", "internal/docs")},
		})
		out, err := Select(f.run, Options{Root: tree(t), Base: "base"})
		require.NoError(t, err)
		assert.Equal(t, wholeTreeIs, strings.Join(out.Packages, "\n")+"\n", "%s changed: want the whole tree", strings.TrimSpace(changed))
		assert.False(t, f.called("go list -f"), "%s changed: the dependents were listed though the whole tree is in scope", strings.TrimSpace(changed))
	}
}

// NEVER SILENTLY NOTHING: the failures of go list, and a Go diff that selects
// zero packages, fail (the default) or fall back to the whole tree, with the
// error or the warning printed. The table is the shapes the #4370 race took.
func TestSelectNeverSilentlySelectsNothing(t *testing.T) {
	t.Parallel()
	root := tree(t)
	const warn = "WARN select-packages: go list failed (open /home/u/.cache/go-build/5e/5e1f-d: no such file or directory); testing the whole tree"
	lists := map[string]map[string]Result{
		"cache":     {listTree: {Stderr: cacheErr, Code: 1}},
		"cannot":    {listTree: {Stderr: "go: cannot find main module\n"}},
		"silent":    {listTree: {Code: 3}},
		"empty":     {listTree: {}},
		"unrelated": {listTree: {Stdout: imports("cmd/other")}, listDeps: {Stdout: mod + "/cmd/other fmt\n"}},
	}
	cases := []struct {
		name, fake string
		all        bool
		wholeTree  bool
		wantErr    string
		wantWarn   string
	}{
		{"a cache race fails by default", "cache", false, false, "ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):\n" + cacheErr, ""},
		{"a zero exit with cannot on stderr fails", "cannot", false, false, "cannot find main module", ""},
		{"an exit with nothing on stderr says so", "silent", false, false, "go list exited 3 with nothing on stderr", ""},
		{"a zero exit listing nothing fails", "empty", false, false, "go list exited 0 and listed no packages", ""},
		{"a Go diff selecting zero packages fails", "unrelated", false, false, "select-packages: the diff touches Go files (cmd/foo/foo.go) but selected zero packages", ""},
		{"--all fails on the cache race too", "cache", true, false, "no such file or directory", ""},
		{"merge_group falls back to the whole tree", "cache", false, true, "", warn},
		{"--all falls back to the whole tree", "cache", true, true, "", warn},
		{"a Go diff selecting zero packages falls back", "unrelated", false, true, "", "WARN select-packages: go list failed (select-packages: the diff touches Go files (cmd/foo/foo.go) but selected zero packages); testing the whole tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			answers := map[string]Result{
				diffCmd:    {Stdout: "cmd/foo/foo.go\n"},
				lsFilesCmd: {Stdout: tracked()},
			}
			for k, v := range lists[tc.fake] {
				answers[k] = v
			}
			out, err := Select(newFake(answers).run, Options{Root: root, All: tc.all, Base: "base", WholeTreeOnError: tc.wholeTree})
			if tc.wantErr != "" {
				var le *ListError
				require.ErrorAs(t, err, &le, "want a ListError containing %q", tc.wantErr)
				require.Contains(t, le.Text, tc.wantErr)
				require.Empty(t, out.Packages)
				assert.True(t, strings.HasSuffix(le.Text, "\n"), "the error text %q does not end in a newline", le.Text)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, wholeTreeIs, strings.Join(out.Packages, "\n")+"\n", "want the whole tree")
			assert.Equal(t, tc.wantWarn, out.Warning)
		})
	}
}

// The whole tree read from the tracked files drops a build-tagged package, a
// testdata directory, a _ or . directory and everything deprecated.
func TestWholeTreeFromTheTrackedFiles(t *testing.T) {
	t.Parallel()
	s := &selector{run: newFake(map[string]Result{lsFilesCmd: {Stdout: tracked()}}).run, o: Options{Root: tree(t)}}
	dep, err := LoadDeprecated(s.o.Root)
	require.NoError(t, err)
	s.dep = dep
	got, err := s.treeFromFiles()
	require.NoError(t, err)
	assert.Equal(t, []string{"./cmd/foo", "./internal/bar", "./internal/ci", "./internal/docs"}, got, "tree from files")
}

func TestSelectRefusesWithNoBase(t *testing.T) {
	t.Parallel()
	_, err := Select(newFake(nil).run, Options{Root: tree(t)})
	require.Error(t, err, "Select with no base and no --all is a refusal")
	assert.Contains(t, err.Error(), "no base commit")
}

func TestSelectReportsAGitDiffFailure(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{diffCmd: {Stderr: "fatal: bad object base\n", Code: 128}})
	_, err := Select(f.run, Options{Root: tree(t), Base: "base"})
	var le *ListError
	require.ErrorAs(t, err, &le, "a failed git diff")
	assert.Contains(t, le.Text, "exited 128")
	assert.Contains(t, le.Text, "bad object base")
}

func TestDealPutsHeavyFirstOnePerShardThenRoundRobin(t *testing.T) {
	t.Parallel()
	var pkgs []string
	for i := 1; i <= 10; i++ {
		pkgs = append(pkgs, fmt.Sprintf("%s/cmd/p%02d", mod, i))
	}
	pkgs = append(pkgs[:3], append([]string{mod + "/cmd/nova-bus"}, pkgs[3:]...)...)
	pkgs = append(pkgs, mod+"/internal/heavy2")
	heavy := []string{"cmd/nova-bus", "internal/heavy2"}
	seen := map[string]int{}
	home := map[string]int{}
	for i := 1; i <= 4; i++ {
		got, err := Deal(pkgs, heavy, 4, i)
		require.NoError(t, err)
		for _, p := range got {
			seen[p]++
			home[p] = i
		}
	}
	for _, p := range pkgs {
		assert.Equal(t, 1, seen[p], "%s dealt %d times", p, seen[p])
	}
	assert.Equal(t, 1, home[mod+"/cmd/nova-bus"], "the heavy packages are on shards 1 and 2")
	assert.Equal(t, 2, home[mod+"/internal/heavy2"])
	// the first other package takes the shard after the heavy ones, then round robin
	assert.Equal(t, []int{3, 4, 1, 2}, []int{home[mod+"/cmd/p01"], home[mod+"/cmd/p02"], home[mod+"/cmd/p03"], home[mod+"/cmd/p04"]}, "round robin after the heavy: p01..p04")
}

func TestDealMatchesOnAPathSuffixNotASubstring(t *testing.T) {
	t.Parallel()
	got, err := Deal([]string{mod + "/cmd/nova-bus-extra", mod + "/cmd/nova-bus"}, []string{"cmd/nova-bus"}, 2, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{mod + "/cmd/nova-bus"}, got, "shard 1 is only the exact heavy package")
}

func TestDealRefusesAShardThatIsNotOne(t *testing.T) {
	t.Parallel()
	for _, c := range [][2]int{{0, 0}, {4, 0}, {4, 5}, {-1, 1}} {
		_, err := Deal([]string{"a"}, nil, c[0], c[1])
		assert.Error(t, err, "Deal(shards %d, shard %d) did not refuse", c[0], c[1])
	}
}

func TestOrderHeavyFirstAndFunctional(t *testing.T) {
	t.Parallel()
	all := []string{"./cmd/a", "./cmd/nova-sandbox", "./cmd/nova-bus", "./cmd/b", "./internal/sandbox", "./cmd/c", "./cmd/d", "./cmd/e"}
	ordered := OrderHeavyFirst(all)
	assert.Equal(t, "./cmd/nova-bus", ordered[0], "the heavy package first")
	assert.Len(t, ordered, len(all))
	assert.Equal(t, "./cmd/a", ordered[1], "the rest in order")
	g := Groups{Linux: "lin", Mac: "mac"}
	got := MarshalLegs(Functional(ordered, g))
	want := `[{"name":"1/4 lin","packages":"./cmd/nova-bus ./cmd/d"},{"name":"2/4 lin","packages":"./cmd/a ./cmd/e"},{"name":"3/4 lin","packages":"./cmd/b"},{"name":"4/4 lin","packages":"./cmd/c"}]`
	assert.Equal(t, want, got, "Functional: the darwin-only packages have no Linux leg")
	assert.Equal(t, `[{"name":"nothing","packages":""}]`, MarshalLegs(Functional(nil, g)), "Functional of nothing")
	assert.Equal(t, `[{"name":"nothing","packages":"","os":"linux","arch":"x64","group":"lin"}]`, MarshalLegs([]Leg{NothingLeg(g)}), "the nothing leg")
}

func TestShardsFollowTheEvent(t *testing.T) {
	t.Parallel()
	for event, want := range map[string]Shards{
		"pull_request":      {Linux: 4, Mac: 4},
		"merge_group":       {Linux: 8, Mac: 4},
		"push":              {Linux: 8, Mac: 8},
		"schedule":          {Linux: 8, Mac: 8},
		"workflow_dispatch": {Linux: 8, Mac: 8},
	} {
		assert.Equal(t, want, ShardsFor(event), "ShardsFor(%s)", event)
	}
}

func legNames(legs []Leg) []string {
	var out []string
	for _, l := range legs {
		out = append(out, l.Name+"="+l.Packages)
	}
	return out
}

func TestFanoutByEvent(t *testing.T) {
	t.Parallel()
	g := Groups{Linux: "lin", Mac: "mac"}
	pkgs := []string{"./cmd/a", "./cmd/b", "./cmd/nova-sandbox", "./cmd/c", "./internal/sandbox"}
	sens := DarwinSensitive{Pkgs: map[string]bool{"./cmd/b": true}}
	cases := []struct {
		event string
		want  []string
	}{
		// the nightly run: Linux only, the darwin-only packages have no leg
		{"schedule", []string{"1/8 lin=./cmd/a", "2/8 lin=./cmd/b", "3/8 lin=./cmd/c"}},
		// the merge group: every package on Linux but the darwin-only ones, which go to macOS
		{"merge_group", []string{"1/8 lin=./cmd/a", "2/8 lin=./cmd/b", "3/8 lin=./cmd/c", "1/4 darwin-arm64=./cmd/nova-sandbox", "2/4 darwin-arm64=./internal/sandbox"}},
		// a pull request: macOS only for what differs there, and the darwin-only packages
		{"pull_request", []string{"1/4 lin=./cmd/a", "2/4 lin=./cmd/b", "3/4 lin=./cmd/c", "1/4 darwin-arm64=./cmd/b", "2/4 darwin-arm64=./cmd/nova-sandbox", "3/4 darwin-arm64=./internal/sandbox"}},
		// a push to dev: every package on both OSes
		{"push", []string{"1/8 lin=./cmd/a", "2/8 lin=./cmd/b", "3/8 lin=./cmd/c", "1/8 darwin-arm64=./cmd/a", "2/8 darwin-arm64=./cmd/b", "3/8 darwin-arm64=./cmd/nova-sandbox", "4/8 darwin-arm64=./cmd/c", "5/8 darwin-arm64=./internal/sandbox"}},
	}
	for _, tc := range cases {
		got := legNames(Fanout(tc.event, pkgs, sens, g, true))
		sort.Strings(got)
		want := append([]string{}, tc.want...)
		sort.Strings(want)
		assert.Equal(t, want, got, tc.event)
	}
	// a pull request with every package sensitive keeps the old shape
	all := DarwinSensitive{All: true}
	legs := Fanout("pull_request", []string{"./cmd/a", "./cmd/b"}, all, g, true)
	got := MarshalLegs(legs)
	assert.Contains(t, got, `"os":"macOS","arch":"ARM64","group":"mac"`)
	assert.Contains(t, got, `"os":"linux","arch":"x64","group":"lin"`)
}

const (
	filesLinux  = mod + "/cmd/a [a.go] [] [] []\n" + mod + "/cmd/b [b.go] [] [] []\n" + mod + "/internal/c [c.go] [] [] []\n"
	filesDarwin = mod + "/cmd/a [a.go] [] [] []\n" + mod + "/cmd/b [b.go] [] [] []\n" + mod + "/internal/c [c.go c_darwin.go] [] [] []\n"
	depsDarwin  = mod + "/cmd/a [" + mod + "/cmd/a.test] " + mod + "/internal/c fmt\n" +
		mod + "/cmd/a.test " + mod + "/cmd/a\n" +
		mod + "/cmd/b fmt\n" +
		mod + "/internal/c fmt\n"
)

func darwinFake(files map[string]string) *fake {
	ans := map[string]Result{
		"go list -m": {Stdout: mod + "\n"},
		"GOOS=linux go list -f " + darwinFiles + " ./cmd/... ./internal/...":                         {Stdout: files["linux"]},
		"GOOS=darwin go list -f " + darwinFiles + " ./cmd/... ./internal/...":                        {Stdout: files["darwin"]},
		"GOOS=darwin go list -test -f {{.ImportPath}} {{join .Deps \" \"}} ./cmd/... ./internal/...": {Stdout: files["deps"]},
	}
	return newFake(ans)
}

// A package is macOS-sensitive when its own files differ under GOOS=darwin or a
// module package it (or its tests) imports does.
func TestDetectDarwinSensitive(t *testing.T) {
	t.Parallel()
	f := darwinFake(map[string]string{"linux": filesLinux, "darwin": filesDarwin, "deps": depsDarwin})
	sens, ok, err := DetectDarwinSensitive(f.run, t.TempDir())
	require.NoError(t, err)
	require.True(t, ok, "DetectDarwinSensitive = %v", sens)
	assert.Equal(t, "./cmd/a ./internal/c ", sens.Sorted(), "want ./cmd/a (imports the differing ./internal/c) and ./internal/c itself")
	assert.True(t, sens.Needs("./cmd/a"))
	assert.False(t, sens.Needs("./cmd/b"))
}

func TestDetectDarwinSensitiveWithNothingDifferent(t *testing.T) {
	t.Parallel()
	f := darwinFake(map[string]string{"linux": filesLinux, "darwin": filesLinux, "deps": depsDarwin})
	sens, ok, err := DetectDarwinSensitive(f.run, t.TempDir())
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.False(t, sens.All)
	assert.Empty(t, sens.Sorted(), "identical listings: an empty set")
}

// If go list fails, every touched package keeps its macOS leg.
func TestDetectDarwinSensitiveFallsBackToAll(t *testing.T) {
	t.Parallel()
	for _, broken := range []string{"GOOS=linux go list -f " + darwinFiles, "GOOS=darwin go list -f " + darwinFiles, "GOOS=darwin go list -test"} {
		f := darwinFake(map[string]string{"linux": filesLinux, "darwin": filesDarwin, "deps": depsDarwin})
		for k := range f.answers {
			if strings.HasPrefix(k, broken) {
				f.answers[k] = Result{Code: 1, Stderr: "boom"}
			}
		}
		sens, ok, err := DetectDarwinSensitive(f.run, t.TempDir())
		assert.NoError(t, err, broken)
		assert.False(t, ok, broken)
		assert.True(t, sens.All, broken)
		assert.True(t, sens.Needs("./anything"), broken)
	}
}

func TestRaceDepsAreTheExternalPackagesOfTheLiveTreeSortedOnce(t *testing.T) {
	t.Parallel()
	root := tree(t)
	const goListDeps = "go list -deps -test -f {{if not .Standard}}{{if not .Module.Main}}{{.ImportPath}}{{end}}{{end}} "
	f := newFake(map[string]Result{
		"go list ./...": {Stdout: imports("cmd/foo", "cmd/gone", "internal/bar")},
		goListDeps + mod + "/cmd/foo " + mod + "/internal/bar": {Stdout: "\nexample.org/z\n\nexample.org/a\nexample.org/z\n"},
	})
	got, err := RaceDeps(f.run, root)
	require.NoError(t, err)
	assert.Equal(t, []string{"example.org/a", "example.org/z"}, got, "the deprecated package is not asked about")
}

func TestRaceDepsRefuseAFailedList(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{"go list ./...": {Code: 1, Stderr: "go: no go.mod\n"}})
	_, err := RaceDeps(f.run, tree(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no go.mod")
	f = newFake(map[string]Result{"go list ./...": {Stdout: imports("cmd/gone")}})
	_, err := RaceDeps(f.run, tree(t))
	require.Error(t, err, "a tree of only deprecated packages is a refusal")
	assert.Contains(t, err.Error(), "no live package")
}

func TestPerfRunsFindTheTestsOnlyTheTagAdds(t *testing.T) {
	t.Parallel()
	root := tree(t)
	write := func(rel, body string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(p, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(p, "x_test.go"), []byte(body), 0o644))
		return p
	}
	perfDir := write("internal/perfy", "//go:build perf\n\npackage perfy\n")
	emptyDir := write("internal/hollow", "//go:build !windows && perf\n\npackage hollow\n")
	plainDir := write("internal/plain", "package plain\n")
	goneDir := write("cmd/gone/perf", "//go:build perf\n\npackage gone\n")
	f := newFake(map[string]Result{
		"go list -tags perf -f {{.ImportPath}} {{.Dir}} ./...": {Stdout: strings.Join([]string{
			mod + "/internal/perfy " + perfDir, mod + "/internal/hollow " + emptyDir, mod + "/internal/plain " + plainDir, mod + "/cmd/gone/perf " + goneDir,
		}, "\n") + "\n"},
		"go test -list . " + mod + "/internal/perfy":             {Stdout: "TestOne\nok  \tx\t0.1s\n"},
		"go test -tags perf -list . " + mod + "/internal/perfy":  {Stdout: "TestOne\nTestSlow\nBenchmarkFast\nok  \tx\t0.1s\n"},
		"go test -list . " + mod + "/internal/hollow":            {Stdout: "TestA\n"},
		"go test -tags perf -list . " + mod + "/internal/hollow": {Stdout: "TestA\n"},
	})
	runs, notes, err := PerfRuns(f.run, root)
	require.NoError(t, err)
	assert.Equal(t, []PerfRun{{Package: mod + "/internal/perfy", Run: "^(BenchmarkFast|TestSlow)$"}}, runs)
	wantNotes := []string{mod + "/internal/perfy: BenchmarkFast TestSlow", mod + "/internal/hollow carries a perf constraint and no test behind it"}
	sort.Strings(wantNotes)
	sort.Strings(notes)
	assert.Equal(t, wantNotes, notes)
	assert.False(t, f.called("go test -list . "+mod+"/internal/plain") || f.called("go test -list . "+mod+"/cmd/gone/perf"), "a package with no perf constraint, or a deprecated one, was asked for its tests")
}

func TestLiveTreeRefusesAFailedList(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{"go list ./...": {Code: 1, Stderr: "boom\n"}})
	_, err := LiveTree(f.run, tree(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestRunnerShareIsCoresOverRunnersBetweenOneAndTwo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cores             int
		says              string
		wantRunners, want int
	}{
		{64, "", 8, 2}, {16, "8", 8, 2}, {12, "8", 8, 1}, {4, "8", 8, 1}, {1, "1", 1, 1}, {64, "1", 1, 2},
		{6, "3", 3, 2}, {6, "4", 4, 1}, {16, "0", 8, 2}, {16, "many", 8, 2}, {16, "-3", 8, 2}, {0, "4", 4, 1},
	} {
		runners, share := RunnerShare(tc.cores, tc.says)
		assert.Equal(t, tc.wantRunners, runners, "RunnerShare(%d, %q) runners", tc.cores, tc.says)
		assert.Equal(t, tc.want, share, "RunnerShare(%d, %q) share", tc.cores, tc.says)
	}
}

func TestUnitMakeArgsByRun(t *testing.T) {
	t.Parallel()
	const common = "make test PKGS=./cmd/a GOTEST_TIMEOUT=110s"
	for name, tc := range map[string]struct {
		whole   string
		nightly bool
		want    string
	}{
		"the default leg keeps Go's test cache and links without DWARF": {"", false, common + " GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w"},
		"a whole-tree run keeps the cache and brings its budgets":       {"--budget 60", false, common + " GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w SLOWTESTS_FLAGS=--budget 60"},
		"the nightly leg measures uncached and enforces":                {"", true, common + " GOTEST_COUNT_FLAG=-count=1 GOTEST_LDFLAGS=-ldflags=-w SLOWTESTS_ENFORCE=1"},
		"a whole-tree run outranks the nightly flag":                    {"--budget 60", true, common + " GOTEST_COUNT_FLAG= GOTEST_LDFLAGS=-ldflags=-w SLOWTESTS_FLAGS=--budget 60"},
	} {
		assert.Equal(t, tc.want, strings.Join(UnitMakeArgs("./cmd/a", tc.whole, tc.nightly), " "), name)
	}
}

func TestWriteUnitShimRefusesWithExit86(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	shim, err := WriteUnitShim(tmp)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmp, UnitShimDir, "redis-server"), shim)
	b, err := os.ReadFile(shim)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho \""+UnitShimMessage+"\" >&2\nexit 86\n", string(b), "shim body")
	fi, err := os.Stat(shim)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), "shim mode")
}

// The darwin legs are dealt on schedule and workflow_dispatch and where the
// target branch is dev or main; the target is read bare (a pull request's
// base_ref, a push's ref name) or as a ref (a merge group's base_ref).
func TestDarwinOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		event, target string
		want          bool
	}{
		{"schedule", "", true},
		{"workflow_dispatch", "", true},
		{"workflow_dispatch", "sprint/foundation", true},
		{"pull_request", "dev", true},
		{"pull_request", "main", true},
		{"pull_request", "sprint/foundation", false},
		{"merge_group", "refs/heads/dev", true},
		{"merge_group", "refs/heads/main", true},
		{"merge_group", "refs/heads/sprint/foundation", false},
		{"push", "dev", true},
		{"push", "main", true},
		{"push", "sprint/foundation", false},
		{"push", "", false},
		{"pull_request", "devel", false},
		{"push", "refs/heads/dev", true},
		{"merge_group", "dev", true},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, DarwinOn(tc.event, tc.target), "%s -> %q", tc.event, tc.target)
	}
}

// With the darwin legs off every package rides the Linux shards and the
// darwin-only packages have no leg, on every event.
func TestFanoutWithTheDarwinLegsOffIsLinuxOnly(t *testing.T) {
	t.Parallel()
	g := Groups{Linux: "lin", Mac: "mac"}
	pkgs := []string{"./cmd/a", "./cmd/nova-sandbox", "./internal/sandbox", "./cmd/b"}
	for _, event := range []string{"pull_request", "merge_group", "push"} {
		legs := Fanout(event, pkgs, DarwinSensitive{All: true}, g, false)
		require.NotEmpty(t, legs, event)
		var dealt []string
		for _, l := range legs {
			assert.Equal(t, "linux", l.OS, "%s: leg %s", event, l.Name)
			dealt = append(dealt, strings.Fields(l.Packages)...)
		}
		sort.Strings(dealt)
		assert.Equal(t, []string{"./cmd/a", "./cmd/b"}, dealt, event)
	}
	assert.Equal(t, []string{"./cmd/a", "./cmd/b"}, DropDarwinOnly(pkgs))
}
