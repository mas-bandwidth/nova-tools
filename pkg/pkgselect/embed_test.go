package pkgselect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A package takes a non-Go file in as its own data with a //go:embed directive,
// and no import edge says so: a change to the embedded file must select the
// embedding package, the same way a change to a doc selects the package whose
// test reads it (keyedPackages). A //go:embed pattern is package-relative and
// cannot reach outside the package directory, so only a changed file under the
// directive's directory matches. Red witnesses: a file the directives do not
// name, and a line that only looks like a directive inside a comment.
func TestSelectChangeMapsAnEmbeddedFileToTheEmbeddingPackage(t *testing.T) {
	t.Parallel()
	root := tree(t)
	for name, body := range map[string]string{
		"internal/hold/hold.go":    "package hold\n\nimport \"embed\"\n\n//go:embed data/app.js\nvar app string\n\n//go:embed assets/*\nvar assets embed.FS\n\n//go:embed all:tpl\nvar tpl embed.FS\n",
		"internal/hold/noembed.go": "package hold\n\n// //go:embed trap/evil.js is a comment, not a directive\nvar _ = 0\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	cases := []struct {
		name, diff string
		want       []string
	}{
		{"an embedded file named by its path selects the embedding package", "internal/hold/data/app.js\n", []string{"./internal/ci", "./internal/docs", "./internal/hold"}},
		{"a file matched by an embedded glob selects the embedding package", "internal/hold/assets/logo.svg\n", []string{"./internal/ci", "./internal/docs", "./internal/hold"}},
		{"a file under an embedded directory selects the embedding package", "internal/hold/tpl/deep/x.tmpl\n", []string{"./internal/ci", "./internal/docs", "./internal/hold"}},
		{"a file the directives do not name selects nothing", "internal/hold/data/other.js\n", []string{"./internal/ci", "./internal/docs"}},
		{"a directive inside a comment is not a directive", "internal/hold/trap/evil.js\n", []string{"./internal/ci", "./internal/docs"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(map[string]Result{
				diffCmd:  {Stdout: tc.diff},
				listTree: {Stdout: imports("cmd/foo", "internal/bar", "internal/ci", "internal/docs", "internal/hold")},
				listDeps: {Stdout: depsListing},
			})
			out, err := Select(f.run, Options{Root: root, Base: "base"})
			require.NoError(t, err)
			assert.Equal(t, tc.want, out.Packages)
		})
	}
}
