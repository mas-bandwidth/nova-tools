//go:build functional

package ci

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kisielk/errcheck/errcheck"
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
			cmd.Env = append(goenv.Clean(os.Environ()), "GOFLAGS=-mod=readonly")
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

// errcheckAnalyzer runs github.com/kisielk/errcheck's checker as an
// analysis.Analyzer, so it shares staticcheck's one whole-tree packages.Load
// instead of loading ./... a second time (docs/SPEC-CI.md, `errcheck`; the
// functional internal/ci package ran past its 100 s bound loading the tree once
// per linter). The checker reads a *packages.Package; the pass hands it the same
// Syntax, TypesInfo and Fset, so one is rebuilt and each unchecked error is
// reported under this analyzer's name. It declares no facts, so the runner runs
// it only on the ./... packages, never on their dependencies.
var errcheckAnalyzer = &analysis.Analyzer{
	Name: "errcheck",
	Doc:  "report unchecked errors (docs/SPEC-CI.md, `errcheck`)",
	Run: func(pass *analysis.Pass) (any, error) {
		checker := errcheck.Checker{
			Exclusions: errcheck.Exclusions{
				Symbols:          append(append([]string{}, errcheck.DefaultExcludedSymbols...), errcheckExcludeSymbols()...),
				BlankAssignments: true,
				TypeAssertions:   true,
			},
		}
		pkg := &packages.Package{TypesInfo: pass.TypesInfo, Fset: pass.Fset, Syntax: pass.Files}
		for _, ue := range checker.CheckPackage(pkg).UncheckedErrors {
			pass.Report(analysis.Diagnostic{Pos: positionOf(pass.Fset, ue.Pos), Message: ue.Line})
		}
		return nil, nil
	},
}

// positionOf returns the token.Pos in fset for the byte offset of p, or
// token.NoPos when fset holds no such file. errcheck returns token.Position
// values (filename/offset), while an analyzer reports token.Pos; this maps the
// one back to the other in the same file set errcheck read.
func positionOf(fset *token.FileSet, p token.Position) token.Pos {
	var file *token.File
	fset.Iterate(func(f *token.File) bool {
		if f.Name() == p.Filename {
			file = f
			return false
		}
		return true
	})
	if file == nil {
		return token.NoPos
	}
	return file.Pos(p.Offset)
}

// wholeTreeLint is the one whole-tree lint run shared by the two whole-tree
// ledger tests (staticcheck and errcheck): one packages.Load of ./... and one
// pass of both analyzer sets, instead of one load per linter. Only the module
// root shares it; a planted fixture module (the ReadItsChecks / ReadsItsShapes
// tests) is far smaller and lints its own tree.
var wholeTreeLint struct {
	once sync.Once
	sc   map[string][]string
	ec   map[string][]string
	err  error
}

// lintTree runs staticcheck's default checks and errcheck over dir in one
// in-process pass, loading and checking ./... once, and returns each linter's
// findings under `<package>:<check>`. The two whole-tree ledger tests share the
// one run (load the packages once for all the linters); a fixture dir gets its
// own run.
func lintTree(t *testing.T, ctx context.Context, dir string) (map[string][]string, map[string][]string) {
	t.Helper()
	if dir == repoRoot(t) {
		wholeTreeLint.once.Do(func() {
			wholeTreeLint.sc, wholeTreeLint.ec, wholeTreeLint.err = lintTreeRun(ctx, dir)
		})
		require.NoError(t, wholeTreeLint.err)
		return wholeTreeLint.sc, wholeTreeLint.ec
	}
	sc, ec, err := lintTreeRun(ctx, dir)
	require.NoError(t, err)
	return sc, ec
}

// lintTreeRun loads ./... and runs staticcheck's default checks plus errcheck
// over it in one pass, returning each linter's sites. U1000 is a whole-program
// result carried on the runner's unused set, not a per-package diagnostic, so
// it is emitted from the unused objects no package marks used.
func lintTreeRun(ctx context.Context, dir string) (map[string][]string, map[string][]string, error) {
	c, err := cache.Default()
	if err != nil {
		return nil, nil, err
	}
	r, err := runner.New(config.Config{Checks: []string{"all"}}, c)
	if err != nil {
		return nil, nil, err
	}
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.LoadAllSyntax | packages.NeedModule,
		Dir:     dir,
		Env:     append(append(goenv.Clean(os.Environ()), lintTarget...), "GOFLAGS=-mod=readonly"),
		Tests:   true,
	}
	lintGoLock.Lock()
	results, err := r.Run(cfg, append(staticcheckAnalyzers(), errcheckAnalyzer), []string{"./..."})
	lintGoLock.Unlock()
	if err != nil {
		return nil, nil, err
	}

	sc := map[string][]string{}
	ec := map[string][]string{}
	seen := map[string]bool{}
	// A generated file (Go's `// Code generated ... DO NOT EDIT.` header,
	// isGeneratedGo) holds the generator's code, not this repository's: a
	// finding in it is the generator's to fix, and make schemawire-check is its
	// check (schemas/README.md). Neither ledger counts it.
	generated := map[string]bool{}
	isGenerated := func(file string) bool {
		g, ok := generated[file]
		if !ok {
			src, err := os.ReadFile(file)
			g = err == nil && isGeneratedGo(src)
			generated[file] = g
		}
		return g
	}
	scAdd := func(file, code, message string, line, col int) error {
		// One finding per (file, line, column, check, message): the same source
		// file sits in both a package and its test variant and each reports the
		// finding once, so staticcheck's printer collapses them; this does the same.
		dk := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s", file, line, col, code, message)
		if seen[dk] {
			return nil
		}
		seen[dk] = true
		if isGenerated(file) {
			return nil
		}
		pkg, err := packageOf(dir, file)
		if err != nil {
			return err
		}
		key := pkg + ":" + code
		sc[key] = append(sc[key], fmt.Sprintf("%s/%s:%d: %s", pkg, filepath.Base(file), line, message))
		return nil
	}

	type unusedKey struct {
		pkgPath, base, name string
		line                int
	}
	type unusedPair struct {
		key unusedKey
		obj unused.Object
	}
	used := map[unusedKey]bool{}
	var unuseds []unusedPair
	for _, res := range results {
		if res.Failed {
			for _, e := range res.Errors {
				if e != nil {
					return nil, nil, e
				}
			}
			continue
		}
		if res.Skipped || !res.Initial {
			continue
		}
		resd, err := res.Load()
		if err != nil {
			return nil, nil, err
		}
		for _, diag := range resd.Diagnostics {
			if diag.Category == errcheckAnalyzer.Name {
				continue
			}
			if err := scAdd(diag.Position.Filename, diag.Category, diag.Message, diag.Position.Line, diag.Position.Column); err != nil {
				return nil, nil, err
			}
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
		if err := scAdd(uo.obj.DisplayPosition.Filename, "U1000", fmt.Sprintf("%s %s is unused", uo.obj.Kind, uo.obj.Name), uo.obj.DisplayPosition.Line, uo.obj.DisplayPosition.Column); err != nil {
			return nil, nil, err
		}
	}

	// errcheck: the shared run reported each unchecked error under the errcheck
	// analyzer's name; here they are re-read with the discarded rule's reason
	// (reasonedAt) applied, and a line that carries `// ignored: <reason>` on it
	// or the line above is not a finding.
	sources := map[string][]string{}
	ecSeen := map[string]bool{}
	for _, res := range results {
		if res.Failed || res.Skipped || !res.Initial {
			continue
		}
		resd, err := res.Load()
		if err != nil {
			return nil, nil, err
		}
		for _, diag := range resd.Diagnostics {
			if diag.Category != errcheckAnalyzer.Name {
				continue
			}
			file, line := diag.Position.Filename, diag.Position.Line
			dk := fmt.Sprintf("%s\x00%d\x00%s", file, line, diag.Message)
			if ecSeen[dk] {
				continue
			}
			ecSeen[dk] = true
			if isGenerated(file) {
				continue
			}
			if sources[file] == nil {
				src, err := os.ReadFile(file)
				if err != nil {
					return nil, nil, err
				}
				sources[file] = strings.Split(string(src), "\n")
			}
			if reasonedAt(sources[file], line) {
				continue
			}
			pkg, err := packageOf(dir, file)
			if err != nil {
				return nil, nil, err
			}
			key := pkg + ":unchecked"
			ec[key] = append(ec[key], pkg+"/"+filepath.Base(file)+":"+strconv.Itoa(line)+": "+diag.Message)
		}
	}
	return sc, ec, nil
}

// staticcheckSites returns staticcheck's findings over dir, sharing the one
// whole-tree run with errcheck (lintTree).
func staticcheckSites(t *testing.T, ctx context.Context, dir string) map[string][]string {
	t.Helper()
	sc, _ := lintTree(t, ctx, dir)
	return sc
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
// counted under its package, and a clean file and a generated one add nothing.
func TestStaticcheckFindingsReadsItsChecks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), lintDeadline)
	defer cancel()

	dir := plantModule(t, map[string]string{
		"p/p.go": "package p\n\nimport \"errors\"\n\nfunc unused() {}\n\nvar ErrBad = errors.New(\"Bad thing\")\n",
		"q/q.go": "package q\n\n// Q is clean.\nfunc Q() int { return 1 }\n",
		"r/r.go": "package r\n\nfunc gone() {}\n\nfunc alsoGone() {}\n",
		"g/g.go": "// Code generated by a generator. DO NOT EDIT.\n\npackage g\n\nfunc unused() {}\n",
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
