//go:build functional

package ci

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kisielk/errcheck/errcheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
)

const (
	errcheckLedgerPath = "testdata/errcheck"
	errcheckRemedy     = "the call's error is not checked; return it, print it as one line with its remedy, or say why it is safe with `// ignored: <reason>` on this line or the one above; the ledger only shrinks"
	// errcheckExcludes are the CLI's output convention: a print to an
	// io.Writer is not a checked error (docs/SPEC-CI.md, `errcheck`).
	errcheckExcludes = "fmt.Fprint\nfmt.Fprintf\nfmt.Fprintln\n"
)

// errcheckExcludeSymbols is errcheckExcludes as the -exclude file's lines, the
// symbols the subprocess read from the exclude file.
func errcheckExcludeSymbols() []string {
	var syms []string
	for _, s := range strings.Split(strings.TrimSpace(errcheckExcludes), "\n") {
		if s != "" {
			syms = append(syms, s)
		}
	}
	return syms
}

// uncheckedErrorSites runs errcheck over dir in process, loading and checking
// ./... with the errcheck library (github.com/kisielk/errcheck/errcheck,
// already a module dependency) rather than spawning the `tool` binary, and
// returns every unchecked error under `<package>:unchecked`, except a line that
// carries `// ignored: <reason>` on it or on the line above, the discarded
// rule's reason (reasonedAt).
func uncheckedErrorSites(t *testing.T, ctx context.Context, dir string) map[string][]string {
	t.Helper()
	checker := errcheck.Checker{
		Exclusions: errcheck.Exclusions{
			Symbols:          append(append([]string{}, errcheck.DefaultExcludedSymbols...), errcheckExcludeSymbols()...),
			BlankAssignments: true,
			TypeAssertions:   true,
		},
	}
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests:   true,
		Dir:     dir,
		Env:     append(append(goenv.Clean(os.Environ()), lintTarget...), "GOFLAGS=-mod=readonly"),
	}
	lintGoLock.Lock()
	pkgs, err := packages.Load(cfg, "./...")
	lintGoLock.Unlock()
	require.NoError(t, err)
	var result errcheck.Result
	for _, pkg := range pkgs {
		require.Emptyf(t, pkg.Errors, "loading %s: %v", pkg.ID, pkg.Errors)
		result.Append(checker.CheckPackage(pkg))
	}
	result = result.Unique()
	sources := map[string][]string{}
	sites := map[string][]string{}
	for _, ue := range result.UncheckedErrors {
		file, line := ue.Pos.Filename, ue.Pos.Line
		if sources[file] == nil {
			src, err := os.ReadFile(file)
			require.NoError(t, err)
			sources[file] = strings.Split(string(src), "\n")
		}
		if reasonedAt(sources[file], line) {
			continue
		}
		pkg, err := packageOf(dir, file)
		require.NoError(t, err)
		key := pkg + ":unchecked"
		sites[key] = append(sites[key], pkg+"/"+filepath.Base(file)+":"+strconv.Itoa(line)+": "+ue.Line)
	}
	return sites
}

// TestUncheckedErrors holds errcheck's unchecked errors over the tree to the
// shrink-only `errcheck` package ledger, one count per package
// (docs/SPEC-CI.md, `errcheck`).
func TestUncheckedErrors(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), lintDeadline)
	defer cancel()

	root := repoRoot(t)
	ledger := newPackageSiteLedger(t, errcheckLedgerPath)
	ledger.sites = uncheckedErrorSites(t, ctx, root)
	reportLedger(t, ledger, errcheckRemedy)
}

// TestUncheckedErrorsReadsItsShapes pins the measure over a planted module: a
// dropped Close is counted under its package, while a print to an io.Writer and
// a call reasoned with `// ignored:` on its line or the line above are not.
func TestUncheckedErrorsReadsItsShapes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), lintDeadline)
	defer cancel()

	dir := plantModule(t, map[string]string{
		"p/p.go": `package p

import (
	"fmt"
	"io"
	"os"
)

func F(w io.Writer, f *os.File) {
	f.Close()
	fmt.Fprintf(w, "x\n")
	fmt.Fprintln(w, "x")
	f.Sync() // ignored: a planted reason on the line
	// ignored: a planted reason on the line above
	os.Remove("x")
}
`,
	})
	sites := uncheckedErrorSites(t, ctx, dir)
	require.Len(t, sites["p:unchecked"], 1, "%v", sites)
	assert.Contains(t, sites["p:unchecked"][0], "f.Close()")
	assert.Len(t, sites, 1)
}
