//go:build functional

package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// The Go best-practice linters as class tests (docs/SPEC-CI.md, `staticcheck`
// and `errcheck`): each tool is a `tool` line in go.mod, built here with the
// tree's own toolchain, run over ./..., and its findings counted per package
// against a shrink-only package ledger. Both run in the functional tier behind
// `//go:build functional`: a whole-tree analysis is far over the unit tier's
// one-second test budget (docs/STANDARD.md section 9, rule 5).
const (
	staticcheckLedgerPath = "testdata/staticcheck"
	staticcheckPkg        = "honnef.co/go/tools/cmd/staticcheck"
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

// wholeTreeLintMu serializes the three whole-tree linter runs. Each tool is a
// CPU-bound child that itself uses every core the leg gave the test binary;
// started together by t.Parallel on the leg's two cores they starve one
// another (errcheck reached 90 s and hit its deadline on a loaded bench) while
// each finishes sooner alone. Taking a turn gives each the leg's whole budget,
// holds every finding, assertion, check, timeout and bound as it was, and adds
// no work (docs/SPEC-CI.md, `staticcheck`, `errcheck`, `deadcode`).
var wholeTreeLintMu sync.Mutex

// serializeLint runs f with wholeTreeLintMu held, releasing it even when f
// fails the test: require's FailNow unwinds deferred calls.
func serializeLint(f func()) {
	wholeTreeLintMu.Lock()
	defer wholeTreeLintMu.Unlock()
	f()
}

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

// buildModuleTool builds pkg, a `tool` line of go.mod, at its pinned version
// with the tree's own toolchain into a temporary directory: a linter built by
// another Go cannot read this toolchain's export data.
func buildModuleTool(t *testing.T, ctx context.Context, root, pkg string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, pkg)
	cmd.Dir = root
	cmd.Env = goenv.Clean(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build %s: %s", pkg, out)
	return bin
}

// runLinter runs bin over ./... in dir as lintTarget and returns its stdout.
// Exit 1 is a run with findings; any other failure, or exit 1 with nothing on
// stdout, is the tool's own error.
func runLinter(ctx context.Context, bin, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, append(args, "./...")...)
	cmd.Dir = dir
	cmd.Env = append(append(goenv.Clean(os.Environ()), lintTarget...), env...)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err == nil || (errors.As(err, &exit) && exit.ExitCode() == 1 && stdout.Len() > 0) {
		return stdout.Bytes(), nil
	}
	return nil, fmt.Errorf("%s ./... in %s: %w\nstderr: %s", filepath.Base(bin), dir, err, stderr.String())
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

// staticcheckFinding is one line of `staticcheck -f json`.
type staticcheckFinding struct {
	Code     string `json:"code"`
	Location struct {
		File string `json:"file"`
		Line int    `json:"line"`
	} `json:"location"`
	Message string `json:"message"`
}

// staticcheckCache is the analysis cache every staticcheck run of this package
// shares, staticcheck's own default (<user cache dir>/staticcheck) where the
// run may write it. A cache made fresh per run re-analysed the standard library
// and every dependency from source on each run, 50-90 s of the functional
// internal/ci package at GOMAXPROCS=2 (docs/SPEC-CI.md, `staticcheck`). The
// cache is content-addressed and salted with the binary's build ID, so a
// changed package, a changed dependency or another staticcheck version misses
// it and is analysed again: sharing it changes what is re-done, never what is
// found. A sandboxed run's write root is the job, not the home, and creating
// the user cache dir there is refused; the cache then lives under the
// process's own temporary root, which the caller keeps inside the job.
func staticcheckCache(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("STATICCHECK_CACHE"); dir != "" {
		return dir
	}
	if dir, err := os.UserCacheDir(); err == nil {
		cache := filepath.Join(dir, "staticcheck")
		if err := os.MkdirAll(cache, 0o755); err == nil {
			return cache
		}
	}
	return filepath.Join(os.TempDir(), "staticcheck")
}

// staticcheckSites runs staticcheck (its default checks, U1000 `unused`
// among them) over dir and returns every finding under `<package>:<check>`.
func staticcheckSites(t *testing.T, ctx context.Context, bin, dir string) map[string][]string {
	t.Helper()
	out, err := runLinter(ctx, bin, dir, []string{"STATICCHECK_CACHE=" + staticcheckCache(t)}, "-f", "json")
	require.NoError(t, err)
	sites := map[string][]string{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var f staticcheckFinding
		require.NoError(t, dec.Decode(&f))
		pkg, err := packageOf(dir, f.Location.File)
		require.NoError(t, err)
		key := pkg + ":" + f.Code
		sites[key] = append(sites[key], fmt.Sprintf("%s/%s:%d: %s", pkg, filepath.Base(f.Location.File), f.Location.Line, f.Message))
	}
	return sites
}

// TestStaticcheckFindings holds staticcheck's findings over the tree to the
// shrink-only `staticcheck` package ledger, one count per package and check
// (docs/SPEC-CI.md, `staticcheck`).
func TestStaticcheckFindings(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	ledger := newPackageSiteLedger(t, staticcheckLedgerPath)
	serializeLint(func() {
		ctx, cancel := context.WithTimeout(t.Context(), lintDeadline)
		defer cancel()
		ledger.sites = staticcheckSites(t, ctx, buildModuleTool(t, ctx, root, staticcheckPkg), root)
	})
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
	sites := staticcheckSites(t, ctx, buildModuleTool(t, ctx, repoRoot(t), staticcheckPkg), dir)
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
