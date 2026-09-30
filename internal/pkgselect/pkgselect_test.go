package pkgselect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fake is a Runner answered from a table keyed by the command line (with
// "GOOS=x " in front when the call sets GOOS). No test here starts git or go.
type fake struct {
	mu      sync.Mutex
	answers map[string]Result
	calls   []string
}

func newFake(answers map[string]Result) *fake { return &fake{answers: answers} }

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
	fetchCmd    = "git fetch -q --depth=1 origin base"
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
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if got := d.Live(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Live = %v\nwant %v (a path drops that package and everything under it, a keep line keeps one, a name that only starts the same is another package)", got, want)
	}
}

func TestNoDeprecatedListKeepsEverything(t *testing.T) {
	t.Parallel()
	var d *Deprecated
	in := []string{"./a", "", mod + "/b"}
	if got := d.Live(in); !reflect.DeepEqual(got, in) {
		t.Errorf("a nil list dropped something: %v", got)
	}
	got, err := LoadDeprecated(t.TempDir())
	if err != nil || got != nil {
		t.Errorf("LoadDeprecated of a root with no list = %v, %v; want nil, nil", got, err)
	}
}

func TestLoadDeprecatedReadsTheModuleFromGoMod(t *testing.T) {
	t.Parallel()
	d, err := LoadDeprecated(tree(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.LivePackage(mod+"/cmd/gone") || !d.LivePackage(mod+"/cmd/gone/kept") || d.LivePackage("./cmd/gone/other") || !d.LivePackage("./cmd/goner") {
		t.Errorf("the fixture list is not read as written: %+v", d)
	}
}

func TestSelectAllListsTheLiveTreeAsDotPaths(t *testing.T) {
	t.Parallel()
	root := tree(t)
	f := newFake(map[string]Result{
		listTree: {Stdout: imports("cmd/foo", "cmd/gone", "internal/bar", "internal/ci", "internal/docs")},
	})
	out, err := Select(f.run, Options{Root: root, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"./cmd/foo", "./internal/bar", "./internal/ci", "./internal/docs"}; !reflect.DeepEqual(out.Packages, want) || out.Warning != "" {
		t.Errorf("Select --all = %v %q, want %v", out.Packages, out.Warning, want)
	}
	if f.called("git fetch") {
		t.Error("--all fetched a base")
	}
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
				fetchCmd: {Code: 128},
				diffCmd:  {Stdout: tc.diff},
				listTree: {Stdout: imports("cmd/foo", "internal/bar", "internal/ci", "internal/docs")},
				listDeps: {Stdout: depsListing},
			})
			out, err := Select(f.run, Options{Root: root, Base: "base"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Packages, tc.want) || out.Warning != "" {
				t.Errorf("selected %v %q, want %v", out.Packages, out.Warning, tc.want)
			}
			if !f.called("git fetch -q --depth=1 origin base") {
				t.Error("the base was not fetched (a shallow clone may lack it)")
			}
		})
	}
}

func TestSelectGoModChangePutsTheWholeTreeInScope(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"go.mod\n", "go.sum\n"} {
		f := newFake(map[string]Result{
			fetchCmd: {},
			diffCmd:  {Stdout: "README.md\n" + changed},
			listTree: {Stdout: imports("cmd/foo", "internal/bar", "internal/ci", "internal/docs")},
		})
		out, err := Select(f.run, Options{Root: tree(t), Base: "base"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(out.Packages, "\n")+"\n" != wholeTreeIs {
			t.Errorf("%s changed: selected %v, want the whole tree", strings.TrimSpace(changed), out.Packages)
		}
		if f.called("go list -f") {
			t.Errorf("%s changed: the dependents were listed though the whole tree is in scope", strings.TrimSpace(changed))
		}
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
				fetchCmd:   {},
				diffCmd:    {Stdout: "cmd/foo/foo.go\n"},
				lsFilesCmd: {Stdout: tracked()},
			}
			for k, v := range lists[tc.fake] {
				answers[k] = v
			}
			out, err := Select(newFake(answers).run, Options{Root: root, All: tc.all, Base: "base", WholeTreeOnError: tc.wholeTree})
			if tc.wantErr != "" {
				var le *ListError
				if !errors.As(err, &le) || !strings.Contains(le.Text, tc.wantErr) || len(out.Packages) != 0 {
					t.Fatalf("got %v %v, want a ListError containing %q and no packages", out, err, tc.wantErr)
				}
				if !strings.HasSuffix(le.Text, "\n") {
					t.Errorf("the error text %q does not end in a newline", le.Text)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(out.Packages, "\n") + "\n"; got != wholeTreeIs || out.Warning != tc.wantWarn {
				t.Errorf("got %q and warning %q, want the whole tree and %q", got, out.Warning, tc.wantWarn)
			}
		})
	}
}

// The whole tree read from the tracked files drops a build-tagged package, a
// testdata directory, a _ or . directory and everything deprecated.
func TestWholeTreeFromTheTrackedFiles(t *testing.T) {
	t.Parallel()
	s := &selector{run: newFake(map[string]Result{lsFilesCmd: {Stdout: tracked()}}).run, o: Options{Root: tree(t)}}
	dep, err := LoadDeprecated(s.o.Root)
	if err != nil {
		t.Fatal(err)
	}
	s.dep = dep
	got, err := s.treeFromFiles()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"./cmd/foo", "./internal/bar", "./internal/ci", "./internal/docs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tree from files = %v, want %v", got, want)
	}
}

func TestSelectRefusesWithNoBase(t *testing.T) {
	t.Parallel()
	if _, err := Select(newFake(nil).run, Options{Root: tree(t)}); err == nil || !strings.Contains(err.Error(), "no base commit") {
		t.Errorf("Select with no base and no --all: %v, want a refusal", err)
	}
}

func TestSelectReportsAGitDiffFailure(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{fetchCmd: {}, diffCmd: {Stderr: "fatal: bad object base\n", Code: 128}})
	_, err := Select(f.run, Options{Root: tree(t), Base: "base"})
	var le *ListError
	if !errors.As(err, &le) || !strings.Contains(le.Text, "exited 128") || !strings.Contains(le.Text, "bad object base") {
		t.Errorf("a failed git diff = %v, want its status and message", err)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range got {
			seen[p]++
			home[p] = i
		}
	}
	for _, p := range pkgs {
		if seen[p] != 1 {
			t.Errorf("%s dealt %d times", p, seen[p])
		}
	}
	if home[mod+"/cmd/nova-bus"] != 1 || home[mod+"/internal/heavy2"] != 2 {
		t.Errorf("the heavy packages are on shards %d and %d, want 1 and 2", home[mod+"/cmd/nova-bus"], home[mod+"/internal/heavy2"])
	}
	// the first other package takes the shard after the heavy ones, then round robin
	if home[mod+"/cmd/p01"] != 3 || home[mod+"/cmd/p02"] != 4 || home[mod+"/cmd/p03"] != 1 || home[mod+"/cmd/p04"] != 2 {
		t.Errorf("round robin after the heavy: p01..p04 on %d %d %d %d, want 3 4 1 2", home[mod+"/cmd/p01"], home[mod+"/cmd/p02"], home[mod+"/cmd/p03"], home[mod+"/cmd/p04"])
	}
}

func TestDealMatchesOnAPathSuffixNotASubstring(t *testing.T) {
	t.Parallel()
	got, err := Deal([]string{mod + "/cmd/nova-bus-extra", mod + "/cmd/nova-bus"}, []string{"cmd/nova-bus"}, 2, 1)
	if err != nil || !reflect.DeepEqual(got, []string{mod + "/cmd/nova-bus"}) {
		t.Errorf("shard 1 = %v, %v; want only the exact heavy package", got, err)
	}
}

func TestDealRefusesAShardThatIsNotOne(t *testing.T) {
	t.Parallel()
	for _, c := range [][2]int{{0, 0}, {4, 0}, {4, 5}, {-1, 1}} {
		if _, err := Deal([]string{"a"}, nil, c[0], c[1]); err == nil {
			t.Errorf("Deal(shards %d, shard %d) did not refuse", c[0], c[1])
		}
	}
}

func TestOrderHeavyFirstAndFunctional(t *testing.T) {
	t.Parallel()
	all := []string{"./cmd/a", "./cmd/nova-sandbox", "./cmd/nova-bus", "./cmd/b", "./internal/sandbox", "./cmd/c", "./cmd/d", "./cmd/e"}
	ordered := OrderHeavyFirst(all)
	if ordered[0] != "./cmd/nova-bus" || len(ordered) != len(all) || ordered[1] != "./cmd/a" {
		t.Errorf("OrderHeavyFirst = %v, want the heavy package first and the rest in order", ordered)
	}
	g := Groups{Linux: "lin", Mac: "mac"}
	got := MarshalLegs(Functional(ordered, g))
	want := `[{"name":"1/4 lin","packages":"./cmd/nova-bus ./cmd/d"},{"name":"2/4 lin","packages":"./cmd/a ./cmd/e"},{"name":"3/4 lin","packages":"./cmd/b"},{"name":"4/4 lin","packages":"./cmd/c"}]`
	if got != want {
		t.Errorf("Functional = %s\nwant %s (the darwin-only packages have no Linux leg)", got, want)
	}
	if got := MarshalLegs(Functional(nil, g)); got != `[{"name":"nothing","packages":""}]` {
		t.Errorf("Functional of nothing = %s", got)
	}
	if got := MarshalLegs([]Leg{NothingLeg(g)}); got != `[{"name":"nothing","packages":"","os":"linux","arch":"x64","group":"lin"}]` {
		t.Errorf("the nothing leg = %s", got)
	}
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
		if got := ShardsFor(event); got != want {
			t.Errorf("ShardsFor(%s) = %+v, want %+v", event, got, want)
		}
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
		got := legNames(Fanout(tc.event, pkgs, sens, g))
		sort.Strings(got)
		want := append([]string{}, tc.want...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", tc.event, got, want)
		}
	}
	// a pull request with every package sensitive keeps the old shape
	all := DarwinSensitive{All: true}
	legs := Fanout("pull_request", []string{"./cmd/a", "./cmd/b"}, all, g)
	if got := MarshalLegs(legs); !strings.Contains(got, `"os":"macOS","arch":"ARM64","group":"mac"`) || !strings.Contains(got, `"os":"linux","arch":"x64","group":"lin"`) {
		t.Errorf("legs = %s, want both groups with their labels", got)
	}
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
	if err != nil || !ok {
		t.Fatalf("DetectDarwinSensitive = %v, %v, %v", sens, ok, err)
	}
	if got := sens.Sorted(); got != "./cmd/a ./internal/c " {
		t.Errorf("sensitive = %q, want ./cmd/a (imports the differing ./internal/c) and ./internal/c itself", got)
	}
	if !sens.Needs("./cmd/a") || sens.Needs("./cmd/b") {
		t.Errorf("Needs: a=%v b=%v, want true and false", sens.Needs("./cmd/a"), sens.Needs("./cmd/b"))
	}
}

func TestDetectDarwinSensitiveWithNothingDifferent(t *testing.T) {
	t.Parallel()
	f := darwinFake(map[string]string{"linux": filesLinux, "darwin": filesLinux, "deps": depsDarwin})
	sens, ok, err := DetectDarwinSensitive(f.run, t.TempDir())
	if err != nil || !ok || sens.All || sens.Sorted() != "" {
		t.Errorf("identical listings: %+v %v %v, want an empty set", sens, ok, err)
	}
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
		if err != nil || ok || !sens.All || !sens.Needs("./anything") {
			t.Errorf("%s failing: %+v %v %v, want All", broken, sens, ok, err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"example.org/a", "example.org/z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RaceDeps = %v, want %v (the deprecated package is not asked about)", got, want)
	}
}

func TestRaceDepsRefuseAFailedList(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{"go list ./...": {Code: 1, Stderr: "go: no go.mod\n"}})
	if _, err := RaceDeps(f.run, tree(t)); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("a failed go list = %v, want its message", err)
	}
	f = newFake(map[string]Result{"go list ./...": {Stdout: imports("cmd/gone")}})
	if _, err := RaceDeps(f.run, tree(t)); err == nil || !strings.Contains(err.Error(), "no live package") {
		t.Errorf("a tree of only deprecated packages = %v, want a refusal", err)
	}
}

func TestPerfRunsFindTheTestsOnlyTheTagAdds(t *testing.T) {
	t.Parallel()
	root := tree(t)
	write := func(rel, body string) string {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "x_test.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	if want := []PerfRun{{Package: mod + "/internal/perfy", Run: "^(BenchmarkFast|TestSlow)$"}}; !reflect.DeepEqual(runs, want) {
		t.Errorf("runs = %v, want %v", runs, want)
	}
	wantNotes := []string{mod + "/internal/perfy: BenchmarkFast TestSlow", mod + "/internal/hollow carries a perf constraint and no test behind it"}
	sort.Strings(wantNotes)
	sort.Strings(notes)
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Errorf("notes = %q, want %q", notes, wantNotes)
	}
	if f.called("go test -list . "+mod+"/internal/plain") || f.called("go test -list . "+mod+"/cmd/gone/perf") {
		t.Error("a package with no perf constraint, or a deprecated one, was asked for its tests")
	}
}

func TestLiveTreeRefusesAFailedList(t *testing.T) {
	t.Parallel()
	f := newFake(map[string]Result{"go list ./...": {Code: 1, Stderr: "boom\n"}})
	if _, err := LiveTree(f.run, tree(t)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("LiveTree = %v, want the go list error", err)
	}
}
