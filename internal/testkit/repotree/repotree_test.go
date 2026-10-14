package repotree

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepotreeRootIsTheCheckout(t *testing.T) {
	t.Parallel()
	root := Root(t)
	assert.FileExists(t, filepath.Join(root, "go.mod"))
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "module github.com/mas-bandwidth/nova-tools")
}

func TestRepotreeFilesReadsOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"cmd/a.go":                 "package a\n",
		"cmd/b/c.go":               "package b\n",
		"cmd/testdata/hidden.go":   "package hidden\n",
		"cmd/b/testdata/nested.go": "package nested\n",
		"cmd/note.txt":             "not go\n",
		"cmd/.git/keep.go":         "package gitkeep\n",
		"internal/bad.go":          "package bad\nfunc (\n",
	})
	rows := []struct {
		name       string
		q          Query
		want       []string
		body       string
		parse      bool
		concurrent bool
	}{
		{
			name: "nested-go-skips-testdata-and-git",
			q:    Query{Root: root, Under: []string{"cmd"}, Ext: ".go", SkipDirs: []string{"testdata"}},
			want: []string{"cmd/a.go", "cmd/b/c.go"},
			body: "package a\n",
		},
		{
			name: "non-go",
			q:    Query{Root: root, Under: []string{"cmd"}, Ext: ".txt", SkipDirs: []string{"testdata"}},
			want: []string{"cmd/note.txt"},
			body: "not go\n",
		},
		{
			name:  "does-not-parse",
			q:     Query{Root: root, Under: []string{"internal"}, Ext: ".go"},
			want:  []string{"internal/bad.go"},
			parse: true,
		},
		{
			name:       "eight-goroutines",
			q:          Query{Root: root, Under: []string{"cmd", "internal"}, Ext: ".go", SkipDirs: []string{"testdata", "absent"}},
			want:       []string{"cmd/a.go", "cmd/b/c.go", "internal/bad.go"},
			concurrent: true,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if row.concurrent {
				assertOneRead(t, row.q, row.want)
				return
			}
			files := Files(t, row.q)
			assert.Equal(t, row.want, rels(files))
			if row.body != "" {
				assert.Equal(t, row.body, string(files[0].Src))
			}
			assert.Equal(t, 1, reads(row.q), "the query was walked more than once")
			if row.name == "nested-go-skips-testdata-and-git" {
				first := Parsed(t, row.q)
				second := Parsed(t, row.q)
				assert.NotNil(t, first[0].AST)
				assert.NoError(t, first[0].Err)
				assert.Equal(t, "a", first[0].AST.Name.Name)
				assert.True(t, first[0].AST == second[0].AST, "a Go file was parsed twice")
				assert.Equal(t, 1, reads(row.q), "parsing read the tree again")
			}
			if !row.parse {
				return
			}
			first := Parsed(t, row.q)
			second := Parsed(t, row.q)
			require.Len(t, first, 1)
			assert.Equal(t, first[0].Rel, second[0].Rel)
			assert.Error(t, first[0].Err, "a file that does not parse was accepted")
			assert.Equal(t, "package bad\nfunc (\n", string(first[0].Src))
			assert.True(t, first[0].AST == second[0].AST, "the file was parsed twice")
			assert.Equal(t, 1, reads(row.q), "parsing read the tree again")
		})
	}
}

func assertOneRead(t *testing.T, q Query, want []string) {
	t.Helper()
	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	got := make([][]File, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			e, err := entryFor(q)
			errs[i] = err
			if e != nil {
				got[i] = append([]File(nil), e.files...)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range n {
		assert.NoError(t, errs[i])
		assert.Equal(t, want, rels(got[i]))
	}
	if len(got[0]) == 0 || len(got[0][0].Src) == 0 {
		assert.Fail(t, "the shared read returned no bytes")
		return
	}
	base := &got[0][0].Src[0]
	for i := 1; i < n; i++ {
		assert.True(t, &got[i][0].Src[0] == base, "goroutine %d saw a second read", i)
	}
	assert.Equal(t, 1, reads(q), "eight callers walked the query more than once")
	again := Files(t, q)
	assert.Equal(t, want, rels(again))
	assert.True(t, &again[0].Src[0] == base, "Files copied the bytes")
	assert.Equal(t, 1, reads(q))
}

func writeFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
}

func rels(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Rel
	}
	return out
}

type exampleTB struct{ testing.TB }

func (exampleTB) Helper() {}

func ExampleFiles() {
	dir, err := os.MkdirTemp("", "repotree-example")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	path := filepath.Join(dir, "cmd", "a.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte("package a\n"), 0o644); err != nil {
		panic(err)
	}
	files := Files(exampleTB{}, Query{Root: dir, Under: []string{"cmd"}, Ext: ".go", SkipDirs: []string{"testdata"}})
	fmt.Println(files[0].Rel)
	fmt.Print(string(files[0].Src))
	// Output:
	// cmd/a.go
	// package a
}
