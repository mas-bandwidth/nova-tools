package ci

import (
	"go/parser"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceCoverDiskSourceSeams is the unit tier's reach for diskSourceSeams:
// it drives the three seams it wires -- filepath.WalkDir, os.ReadFile and a
// fresh-FileSet parse -- over a fixture tree the test builds in its own
// t.TempDir. Each row names one seam's answer from the disk, and one refusal
// against a name or bytes the disk will not give. The functional tier's
// TestTheSourceSeamsAnswerAsTheDiskDoes compares these seams with the shared
// tree at repository scale; this test pins the seams' own wiring without that
// sweep: no sleep, no subprocess, no store.
func TestSourceCoverDiskSourceSeams(t *testing.T) {
	t.Parallel()

	const goSrc = "package p\n\nfunc F() int { return 1 }\n"
	root := t.TempDir()
	dir := filepath.Join(root, "sub")
	require.NoError(t, os.Mkdir(dir, 0o755))
	file := filepath.Join(dir, "f.go")
	require.NoError(t, os.WriteFile(file, []byte(goSrc), 0o644))
	missing := filepath.Join(root, "gone")

	tests := []struct {
		name  string
		probe func(t *testing.T, s SourceSeams)
	}{
		{"wires every seam non-nil", func(t *testing.T, s SourceSeams) {
			require.NotNil(t, s.WalkDir, "diskSourceSeams wired no WalkDir")
			require.NotNil(t, s.ReadFile, "diskSourceSeams wired no ReadFile")
			require.NotNil(t, s.ParseFile, "diskSourceSeams wired no ParseFile")
		}},
		{"walk lists the fixture tree", func(t *testing.T, s SourceSeams) {
			var seen []string
			require.NoError(t, s.walk(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, relErr := filepath.Rel(root, p)
				if relErr != nil {
					return relErr
				}
				seen = append(seen, filepath.ToSlash(rel))
				return nil
			}))
			assert.Equal(t, []string{".", "sub", "sub/f.go"}, seen, "the walk did not list the fixture tree once each")
		}},
		{"walk refuses a root not on the disk", func(t *testing.T, s SourceSeams) {
			// WalkDir hands the root's error to fn; propagating it is the refusal.
			err := s.walk(missing, func(_ string, _ fs.DirEntry, err error) error { return err })
			assert.ErrorIs(t, err, fs.ErrNotExist, "walking a root that is not on the disk answered %v", err)
		}},
		{"read answers the fixture bytes", func(t *testing.T, s SourceSeams) {
			got, err := s.readFile(file)
			require.NoError(t, err)
			assert.Equal(t, goSrc, string(got), "the read of the fixture file answered different bytes")
		}},
		{"read refuses a file not on the disk", func(t *testing.T, s SourceSeams) {
			_, err := s.readFile(missing)
			assert.ErrorIs(t, err, fs.ErrNotExist, "reading a file that is not on the disk answered %v", err)
		}},
		{"parse answers a fresh FileSet and the tree", func(t *testing.T, s SourceSeams) {
			src, err := s.readFile(file)
			require.NoError(t, err)
			fset, astFile, err := s.parseFile(file, src, parser.ParseComments)
			require.NoError(t, err)
			require.NotNil(t, fset)
			require.NotNil(t, astFile)
			assert.Equal(t, "p", astFile.Name.Name, "the parse answered a file whose package is not p")
			assert.NotNil(t, fset.File(astFile.Pos()), "the parsed file's position does not resolve in the FileSet it returned")
		}},
		{"parse refuses bytes that are not Go", func(t *testing.T, s SourceSeams) {
			_, astFile, err := s.parseFile("bad.go", []byte("not Go at all"), 0)
			assert.Error(t, err, "parsing bytes that are not Go answered no error (file %v)", astFile)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.probe(t, diskSourceSeams())
		})
	}
}
