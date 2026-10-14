package repotree

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepotreeFilesReadsOnce(t *testing.T) {
	t.Parallel()

	root := testkit.Tree(t, t.TempDir(), map[string]string{
		"a/b/c.go":        "package c\n",
		"a/testdata/d.go": "package d\n",
		"a/note.txt":      "hello\n",
		"a/bad.go":        "package bad\nfunc {\n",
		"a/nested/e.md":   "# x\n",
	})
	cases := []struct {
		name    string
		q       Query
		want    []string
		wantErr string
		once    bool
		bad     string
	}{
		{
			name: "go files skip testdata and non-go",
			q:    Query{Under: []string{"a"}, Ext: ".go", SkipDirs: []string{"testdata"}},
			want: []string{"a/b/c.go", "a/bad.go"},
			bad:  "a/bad.go",
		},
		{
			name: "empty ext keeps the non-go file",
			q:    Query{Under: []string{"a"}, SkipDirs: []string{"testdata"}},
			want: []string{"a/b/c.go", "a/bad.go", "a/nested/e.md", "a/note.txt"},
		},
		{
			name: "no skip lists testdata",
			q:    Query{Under: []string{"a"}, Ext: ".go"},
			want: []string{"a/b/c.go", "a/bad.go", "a/testdata/d.go"},
		},
		{
			name:    "missing under fails the walk",
			q:       Query{Under: []string{"missing"}, Ext: ".go"},
			wantErr: "walk",
		},
		{
			name: "eight goroutines read once",
			q:    Query{Under: []string{"a"}, Ext: ".md", SkipDirs: []string{"testdata"}},
			want: []string{"a/nested/e.md"},
			once: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.wantErr != "" {
				_, err := entryFor(root, tc.q)
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			if tc.once {
				assertOneRead(t, root, tc.q, tc.want)
				return
			}
			got := filesAt(t, root, tc.q)
			assert.Equal(t, tc.want, rels(got))
			if tc.bad == "" {
				return
			}
			parsed := parsedAt(t, root, tc.q)
			var bad *ParsedFile
			for i := range parsed {
				if parsed[i].Rel == tc.bad {
					bad = &parsed[i]
				}
				if parsed[i].Rel == "a/b/c.go" {
					assert.NoError(t, parsed[i].Err)
					assert.NotNil(t, parsed[i].AST)
					assert.NotNil(t, parsed[i].FSet)
				}
			}
			require.NotNil(t, bad)
			assert.Error(t, bad.Err)
		})
	}
}

func assertOneRead(t *testing.T, root string, q Query, want []string) {
	t.Helper()
	const n = 8
	var wg sync.WaitGroup
	got := make([][]File, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			e, err := entryFor(root, q)
			errs[i] = err
			if err == nil {
				got[i] = append([]File(nil), e.files...)
			}
		}()
	}
	wg.Wait()
	for i := range n {
		require.NoError(t, errs[i])
		assert.Equal(t, want, rels(got[i]))
	}
	require.NotEmpty(t, got[0])
	assert.Equal(t, got[0][0].Src, got[n-1][0].Src)
	e, err := entryFor(root, q)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.loads.Load())
	assert.Equal(t, int64(len(want)), e.fileReads.Load())
	parsed := parsedAt(t, root, q)
	assert.Empty(t, parsed)
	assert.Equal(t, int64(len(want)), e.fileReads.Load())
}

func rels(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Rel
	}
	return out
}

func TestRootIsTheCheckout(t *testing.T) {
	t.Parallel()
	root := Root(t)
	_, err := entryFor(root, Query{Under: []string{"internal/testkit/repotree"}, Ext: ".go"})
	require.NoError(t, err)
	files := filesAt(t, root, Query{Under: []string{"internal/testkit/repotree"}, Ext: ".go", SkipDirs: []string{"testdata"}})
	var names []string
	for _, f := range files {
		names = append(names, f.Rel)
	}
	assert.Contains(t, names, "internal/testkit/repotree/repotree.go")
}

func ExampleFiles() {
	q := Query{Under: []string{"cmd", "internal", "pkg"}, Ext: ".go", SkipDirs: []string{"testdata"}}
	fmt.Printf("%s %s\n", q.Ext, strings.Join(q.SkipDirs, ","))
	// Output:
	// .go testdata
}
