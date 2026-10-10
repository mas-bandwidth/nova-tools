//go:build functional

package ci

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	errcheckLedgerPath = "testdata/errcheck"
	errcheckPkg        = "github.com/kisielk/errcheck"
	errcheckRemedy     = "the call's error is not checked; return it, print it as one line with its remedy, or say why it is safe with `// ignored: <reason>` on this line or the one above; the ledger only shrinks"
	// errcheckExcludes are the CLI's output convention: a print to an
	// io.Writer is not a checked error (docs/SPEC-CI.md, `errcheck`).
	errcheckExcludes = "fmt.Fprint\nfmt.Fprintf\nfmt.Fprintln\n"
)

// errcheckLine is one line of `errcheck -abspath`: file:line:col:<tab>call.
var errcheckLine = regexp.MustCompile(`^(.+?):(\d+):\d+:\t(.*)$`)

// uncheckedErrorSites runs errcheck over dir and returns every unchecked error
// under `<package>:unchecked`, except a line that carries `// ignored: <reason>`
// on it or on the line above, the discarded rule's reason (reasonedAt).
func uncheckedErrorSites(t *testing.T, ctx context.Context, bin, dir string) map[string][]string {
	t.Helper()
	excludes := filepath.Join(t.TempDir(), "excludes.txt")
	require.NoError(t, os.WriteFile(excludes, []byte(errcheckExcludes), 0o644))
	out, err := runLinter(ctx, bin, dir, nil, "-abspath", "-exclude", excludes)
	require.NoError(t, err)
	sources := map[string][]string{}
	sites := map[string][]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := errcheckLine.FindStringSubmatch(sc.Text())
		require.NotNil(t, m, "errcheck printed a line this test cannot read: %q", sc.Text())
		file, call := m[1], m[3]
		line, err := strconv.Atoi(m[2])
		require.NoError(t, err)
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
		sites[key] = append(sites[key], pkg+"/"+filepath.Base(file)+":"+m[2]+": "+call)
	}
	require.NoError(t, sc.Err())
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
	serializeLint(func() {
		ledger.sites = uncheckedErrorSites(t, ctx, buildModuleTool(t, ctx, root, errcheckPkg), root)
	})
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
	sites := uncheckedErrorSites(t, ctx, buildModuleTool(t, ctx, repoRoot(t), errcheckPkg), dir)
	require.Len(t, sites["p:unchecked"], 1, "%v", sites)
	assert.Contains(t, sites["p:unchecked"][0], "f.Close()")
	assert.Len(t, sites, 1)
}
