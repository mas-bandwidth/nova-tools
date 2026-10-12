//go:build functional

package ci

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"

	"honnef.co/go/tools/config"
	"honnef.co/go/tools/lintcmd/cache"
	"honnef.co/go/tools/lintcmd/runner"
	"honnef.co/go/tools/simple"
	"honnef.co/go/tools/staticcheck"
	"honnef.co/go/tools/stylecheck"
	"honnef.co/go/tools/unused"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
)

// The Go best-practice linters as class tests (docs/SPEC-CI.md, `staticcheck`
// and `errcheck`): each is run over ./... in process with its library
// (honnef.co/go/tools, github.com/kisielk/errcheck), and its findings counted
// per package against a shrink-only package ledger. Both run in the functional
// tier behind `//go:build functional`: a whole-tree analysis is far over the
// unit tier's one-second test budget (docs/STANDARD.md section 9, rule 5).
const (
	staticcheckLedgerPath = "testdata/staticcheck"
	deadcodePkg           = "golang.org/x/tools/cmd/deadcode"
	// lintUpdateCommand is the one run that lowers both ledgers: the rules are
	// functional-tier only, so `NOVA_CI_UPDATE=1 make test PKGS=./internal/ci`
	// never reaches them.
	lintUpdateCommand = "go test -tags functional -count=1 -timeout 110s -run '^(TestStaticcheckFindings|TestUncheckedErrors)$' ./internal/ci/"
	staticcheckRemedy = "fix the finding (staticcheck -explain <check> says how); the ledger only shrinks"
	// lintDeadline bounds one tool's run over the whole tree, under make lint's
	// -timeout and the two-minute job cap.
	lintDeadline = 100 * time.Second
)

// lintTarget is the one platform both linters read the tree as, whatever the
// member's own GOOS: the files a GOOS selects change what the tools find, so a
// ledger measured on one platform is stale on another (docs/SPEC-CI.md,
// `staticcheck`, its narrowings).
var lintTarget = []string{"GOOS=linux", "CGO_ENABLED=0"}

// packageLedgerOptions are the options of the two linter ledgers: counted,
// ceiling-only, and keyed `<package directory>:<kind>`.
var packageLedgerOptions = allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true}

// newPackageSiteLedger loads a package-keyed counted ledger into the shared
// site ledger, so its findings read as the never-silent rules' do.
func newPackageSiteLedger(t *testing.T, path string) *siteLedger {
	t.Helper()
	allow, err := allowlist.LoadPackages(path, packageLedgerOptions)
	require.NoError(t, err)
	requireReasons(t, allow)
	return &siteLedger{path: path, allow: allow, sites: map[string][]string{}}
}

// reportLedger fails t with each violation and the command that lowers the
// ledger after a fix.
func reportLedger(t *testing.T, ledger *siteLedger, remedy string) {
	t.Helper()
	for _, v := range ledger.violations(t, remedy) {
		t.Errorf("%s; after a fix, run: %s=1 %s", v, allowlist.UpdateEnv, lintUpdateCommand)
	}
}

// lintTools is the one build per package run of the module linter tool that
// still runs as a subprocess (deadcode), shared by every test that runs one:
// the functional internal/ci package links the tool once instead of once per
// test, the same sharing cmd/'s walks got (builtTools,
// onboarding_functional_test.go; internal/ci ran past its 100 s bound at
// GOMAXPROCS=2). The tool is still built by the tree's own toolchain: a linter
// built by another Go cannot read this toolchain's export data. TestMain
// removes the directory (classtests_class_test.go).
var lintTools struct {
	once sync.Once
	dir  string
	bin  map[string]string
	out  []byte
	err  error
}

// lintGoLock serializes the `go` subprocesses the whole-tree linters run
// against one another: the deadcode tool build here and the in-process
// packages.Load in staticcheck and errcheck all ask the go command to resolve
// the same module graph, and two of them resolving it at once races a go.mod
// read against a rewrite ("no required module provides package ...", seen
// intermittently at GOMAXPROCS=2). The lock is held only around the subprocess
// itself, never around an analyzer's CPU work.
var lintGoLock sync.Mutex

func init() {
	cleanupShared = append(cleanupShared, func() {
		if lintTools.dir != "" {
			_ = os.RemoveAll(lintTools.dir) // ignored: a temporary directory that may already be gone
		}
	})
}

// buildModuleTool returns the path of pkg, a `tool` line of go.mod, building
// each still-subprocess tool ONCE per package run into a shared directory the
// first caller creates; the other callers read the one build. A failed build
// fails every caller with its output. The build runs in the repo root, never in
// a caller's own directory: the tool is a `tool` line of this module's go.mod,
// and a caller that passes a planted fixture module (dead_code_class_test.go)
// would resolve the import path there and fail ("no required module provides
// package", seen intermittently at GOMAXPROCS=2 when the fixture's test won the
// once race).
func buildModuleTool(t *testing.T, _ context.Context, pkg string) string {
	t.Helper()
	lintTools.once.Do(func() {
		dir, err := os.MkdirTemp("", "ci-lint-tools-")
		if err != nil {
			lintTools.err = err
			return
		}
		lintTools.dir = dir
		lintTools.bin = map[string]string{}
		for _, p := range []string{deadcodePkg} {
			bin := filepath.Join(dir, filepath.Base(p))
			cmd := exec.Command("go", "build", "-o", bin, p)
			cmd.Dir = repoRoot(t)
			cmd.Env = goenv.Clean(os.Environ())
			lintGoLock.Lock()
			out, err := cmd.CombinedOutput()
			lintGoLock.Unlock()
			if err != nil {
				lintTools.out, lintTools.err = out, err
				return
			}
			lintTools.bin[p] = bin
		}
	})
	require.NoErrorf(t, lintTools.err, "go build %s: %v\n%s", pkg, lintTools.err, lintTools.out)
	return lintTools.bin[pkg]
}

// packageOf names the package directory of file relative to root, "." for the
// root itself; both are resolved through symlinks first.
func packageOf(root, file string) (string, error) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	f, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(r, filepath.Dir(f))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside %s", file, root)
	}
	return filepath.ToSlash(rel), nil
}

// staticcheckAnalyzers is staticcheck's default check set (docs/SPEC-CI.md,
// `staticcheck`): the SA, S and ST analyzers plus U1000 `unused`, the same set
// the `staticcheck` binary runs with no -checks flag. The NonDefault analyzers
// (ST1000 package comments, ST1003, ST1016, ST1020-ST1023, SA9003) are dropped,
// exactly as the binary's default excludes them. They are run in process over
// one packages.Load instead of as a subprocess that re-loads the tree, so the
// load is paid once.
func staticcheckAnalyzers() []*analysis.Analyzer {
	var as []*analysis.Analyzer
	for _, a := range simple.Analyzers {
		if !a.Doc.NonDefault {
			as = append(as, a.Analyzer)
		}
	}
	for _, a := range staticcheck.Analyzers {
		if !a.Doc.NonDefault {
			as = append(as, a.Analyzer)
		}
	}
	for _, a := range stylecheck.Analyzers {
		if !a.Doc.NonDefault {
			as = append(as, a.Analyzer)
		}
	}
	as = append(as, unused.Analyzer.Analyzer)
	return as
}

// staticcheckSites runs staticcheck's default checks over dir in process,
// loading and checking ./... with honnef.co/go/tools (already a module
// dependency) rather than spawning the `tool` binary, and returns every finding
// under `<package>:<check>`. U1000 is a whole-program result carried on the
// runner's unused set, not a per-package diagnostic, so it is emitted from the
// unused objects that no package marks used.
func staticcheckSites(t *testing.T, ctx context.Context, dir string) map[string][]string {
	t.Helper()
	c, err := cache.Default()
	require.NoError(t, err)
	r, err := runner.New(config.Config{Checks: []string{"all"}}, c)
	require.NoError(t, err)
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.LoadAllSyntax | packages.NeedModule,
		Dir:     dir,
		Env:     append(append(goenv.Clean(os.Environ()), lintTarget...), "GOFLAGS=-mod=readonly"),
		Tests:   true,
	}
	lintGoLock.Lock()
	results, err := r.Run(cfg, staticcheckAnalyzers(), []string{"./..."})
	lintGoLock.Unlock()
	require.NoError(t, err)

	sites := map[string][]string{}
	seen := map[string]bool{}
	add := func(file, code, message string, line, col int) {
		// One finding per (file, line, column, check, message): the same source
		// file sits in both a package and its test variant and each reports the
		// finding once, so staticcheck's printer collapses them; this does the same.
		dk := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s", file, line, col, code, message)
		if seen[dk] {
			return
		}
		seen[dk] = true
		pkg, err := packageOf(dir, file)
		require.NoError(t, err)
		key := pkg + ":" + code
		sites[key] = append(sites[key], fmt.Sprintf("%s/%s:%d: %s", pkg, filepath.Base(file), line, message))
	}

	type unusedKey struct{ pkgPath, base, name string; line int }
	type unusedPair struct{ key unusedKey; obj unused.Object }
	used := map[unusedKey]bool{}
	var unuseds []unusedPair
	for _, res := range results {
		if res.Failed {
			for _, e := range res.Errors {
				require.NoError(t, e, "staticcheck failed on %s", res.Package)
			}
			continue
		}
		if res.Skipped || !res.Initial {
			continue
		}
		resd, err := res.Load()
		require.NoError(t, err)
		for _, diag := range resd.Diagnostics {
			add(diag.Position.Filename, diag.Category, diag.Message, diag.Position.Line, diag.Position.Column)
		}
		for _, obj := range resd.Unused.Used {
			used[unusedKey{res.Package.PkgPath, filepath.Base(obj.Position.Filename), obj.Name, obj.Position.Line}] = true
		}
		for _, obj := range resd.Unused.Unused {
			key := unusedKey{res.Package.PkgPath, filepath.Base(obj.Position.Filename), obj.Name, obj.Position.Line}
			unuseds = append(unuseds, unusedPair{key, obj})
			if _, ok := used[key]; !ok {
				used[key] = false
			}
		}
	}
	for _, uo := range unuseds {
		if used[uo.key] {
			continue
		}
		add(uo.obj.DisplayPosition.Filename, "U1000", fmt.Sprintf("%s %s is unused", uo.obj.Kind, uo.obj.Name), uo.obj.DisplayPosition.Line, uo.obj.DisplayPosition.Column)
	}
	return sites
}

// TestStaticcheckFindings holds staticcheck's findings over the tree to the
// shrink-only `staticcheck` package ledger, one count per package and check
// (docs/SPEC-CI.md, `staticcheck`).
func TestStaticcheckFindings(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), lintDeadline)
	defer cancel()

	root := repoRoot(t)
	ledger := newPackageSiteLedger(t, staticcheckLedgerPath)
	ledger.sites = staticcheckSites(t, ctx, root)
	reportLedger(t, ledger, staticcheckRemedy)
}

// TestStaticcheckFindingsReadsItsChecks pins the measure over a planted module:
// an unused function is U1000 and a capitalized error string ST1005, each
// counted under its package, and a clean file adds nothing.
func TestStaticcheckFindingsReadsItsChecks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), lintDeadline)
	defer cancel()

	dir := plantModule(t, map[string]string{
		"p/p.go": "package p\n\nimport \"errors\"\n\nfunc unused() {}\n\nvar ErrBad = errors.New(\"Bad thing\")\n",
		"q/q.go": "package q\n\n// Q is clean.\nfunc Q() int { return 1 }\n",
		"r/r.go": "package r\n\nfunc gone() {}\n\nfunc alsoGone() {}\n",
	})
	sites := staticcheckSites(t, ctx, dir)
	counts := map[string]int{}
	for k, v := range sites {
		counts[k] = len(v)
	}
	assert.Equal(t, map[string]int{"p:U1000": 1, "p:ST1005": 1, "r:U1000": 2}, counts)
}

// plantModule writes files into a fresh module in a temporary directory.
func plantModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.test/planted\n\ngo 1.26\n"
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	}
	return dir
}
