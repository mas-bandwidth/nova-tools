package release

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releaseEdgeTarEntry is one entry an in-memory release archive carries: the
// header and, for a regular file, its body.
type releaseEdgeTarEntry struct {
	header *tar.Header
	body   []byte
}

// releaseEdgeTar builds an in-memory tar stream from entries. It is the unit
// tier's stand-in for a fetched release, so readTar's skips and refusals are
// reached with no network and no child.
func releaseEdgeTar(t *testing.T, entries ...releaseEdgeTarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := *e.header
		if len(e.body) > 0 {
			h.Size = int64(len(e.body))
		}
		require.NoError(t, tw.WriteHeader(&h))
		if len(e.body) > 0 {
			_, err := tw.Write(e.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// TestReleaseEdgesCoverReadTar covers every skip and refusal tier of readTar
// (edges.go:528): the root and a directory entry are skipped and only the
// regular file is unpacked; a non-regular entry, a name carrying a path, an
// unreadable header and a destination that cannot be made are each refused
// before anything is written.
func TestReleaseEdgesCoverReadTar(t *testing.T) {
	t.Parallel()

	garbage := make([]byte, 512)
	for i := range garbage {
		garbage[i] = byte(i%255 + 1)
	}

	dest := func(t *testing.T, root string) string {
		t.Helper()
		return filepath.Join(root, "dest")
	}

	cases := []struct {
		name      string
		archive   func(t *testing.T) []byte
		dest      func(t *testing.T, root string) string
		wantErrIs error
		wantErr   string
		wantFiles []string
	}{
		{
			name: "unpacksTheFileAndSkipsTheRootAndDirectory",
			archive: func(t *testing.T) []byte {
				return releaseEdgeTar(t,
					releaseEdgeTarEntry{header: &tar.Header{Name: "./", Typeflag: tar.TypeDir}},
					releaseEdgeTarEntry{header: &tar.Header{Name: "dir/", Typeflag: tar.TypeDir}},
					releaseEdgeTarEntry{header: &tar.Header{Name: "file.txt", Typeflag: tar.TypeReg}, body: []byte("hello")},
				)
			},
			dest:      dest,
			wantFiles: []string{"file.txt"},
		},
		{
			name: "refusesAnEntryThatIsNotARegularFile",
			archive: func(t *testing.T) []byte {
				return releaseEdgeTar(t,
					releaseEdgeTarEntry{header: &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"}},
				)
			},
			dest:      dest,
			wantErr:   "not a regular file",
			wantFiles: []string{},
		},
		{
			name: "refusesASeparatorInTheName",
			archive: func(t *testing.T) []byte {
				return releaseEdgeTar(t,
					releaseEdgeTarEntry{header: &tar.Header{Name: "sub/file", Typeflag: tar.TypeReg}},
				)
			},
			dest:      dest,
			wantErr:   "a path rather than a file name",
			wantFiles: []string{},
		},
		{
			name: "refusesADotDotInTheName",
			archive: func(t *testing.T) []byte {
				return releaseEdgeTar(t,
					releaseEdgeTarEntry{header: &tar.Header{Name: "../x", Typeflag: tar.TypeReg}},
				)
			},
			dest:      dest,
			wantErr:   "a path rather than a file name",
			wantFiles: []string{},
		},
		{
			name: "refusesABackslashInTheName",
			archive: func(t *testing.T) []byte {
				return releaseEdgeTar(t,
					releaseEdgeTarEntry{header: &tar.Header{Name: "a\\b", Typeflag: tar.TypeReg}},
				)
			},
			dest:      dest,
			wantErr:   "a path rather than a file name",
			wantFiles: []string{},
		},
		{
			name:      "refusesAHeaderItCannotRead",
			archive:   func(t *testing.T) []byte { return garbage },
			dest:      dest,
			wantErrIs: tar.ErrHeader,
			wantFiles: []string{},
		},
		{
			name: "refusesADestinationUnderARegularFile",
			archive: func(t *testing.T) []byte {
				return releaseEdgeTar(t,
					releaseEdgeTarEntry{header: &tar.Header{Name: "file", Typeflag: tar.TypeReg}},
				)
			},
			dest: func(t *testing.T, root string) string {
				t.Helper()
				blocker := filepath.Join(root, "blocker")
				require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o644))
				return filepath.Join(blocker, "dest")
			},
			wantErr: "not a directory",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := tc.dest(t, t.TempDir())
			err := readTar(bytes.NewReader(tc.archive(t)), target)
			switch {
			case tc.wantErrIs != nil:
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErrIs)
			case tc.wantErr != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			default:
				require.NoError(t, err)
			}
			if tc.wantFiles != nil {
				ents, err := os.ReadDir(target)
				require.NoError(t, err)
				names := make([]string, 0, len(ents))
				for _, e := range ents {
					names = append(names, e.Name())
				}
				assert.ElementsMatch(t, tc.wantFiles, names)
			}
		})
	}
}

// TestReleaseEdgesCoverWriteTar covers writeTar's directory read refusal, its
// skip of a subdirectory and of a name outside allowed, and the exact name,
// permission bits and bytes of an allowed file read back with archive/tar
// (edges.go:570).
func TestReleaseEdgesCoverWriteTar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		build     func(t *testing.T, root string) (string, map[string]bool)
		prefix    string
		wantErrIs error
		verify    func(t *testing.T, tr *tar.Reader)
	}{
		{
			name: "refusesADirectoryThatIsNotThere",
			build: func(t *testing.T, root string) (string, map[string]bool) {
				t.Helper()
				return filepath.Join(root, "absent"), nil
			},
			prefix:    "prefix",
			wantErrIs: fs.ErrNotExist,
		},
		{
			name: "skipsASubdirectoryAndADisallowedFile",
			build: func(t *testing.T, root string) (string, map[string]bool) {
				t.Helper()
				dir := filepath.Join(root, "release")
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "allowed"), []byte("ok"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "disallowed"), []byte("no"), 0o644))
				return dir, map[string]bool{"allowed": true, "subdir": true}
			},
			prefix: "prefix",
			verify: func(t *testing.T, tr *tar.Reader) {
				t.Helper()
				header, err := tr.Next()
				require.NoError(t, err)
				assert.Equal(t, "prefix/allowed", header.Name)
				body, err := io.ReadAll(tr)
				require.NoError(t, err)
				assert.Equal(t, "ok", string(body))
				_, err = tr.Next()
				assert.ErrorIs(t, err, io.EOF)
			},
		},
		{
			name: "writesAnAllowedFileWithItsModeAndExactBytes",
			build: func(t *testing.T, root string) (string, map[string]bool) {
				t.Helper()
				dir := filepath.Join(root, "release")
				require.NoError(t, os.MkdirAll(dir, 0o755))
				file := filepath.Join(dir, "tool")
				require.NoError(t, os.WriteFile(file, []byte("exact bytes"), 0o600))
				require.NoError(t, os.Chmod(file, 0o755))
				return dir, map[string]bool{"tool": true}
			},
			prefix: "prefix",
			verify: func(t *testing.T, tr *tar.Reader) {
				t.Helper()
				header, err := tr.Next()
				require.NoError(t, err)
				assert.Equal(t, "prefix/tool", header.Name)
				assert.Equal(t, int64(0o755), header.Mode)
				body, err := io.ReadAll(tr)
				require.NoError(t, err)
				assert.Equal(t, "exact bytes", string(body))
				_, err = tr.Next()
				assert.ErrorIs(t, err, io.EOF)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, allowed := tc.build(t, t.TempDir())
			var buf bytes.Buffer
			err := writeTar(&buf, dir, tc.prefix, allowed)
			if tc.wantErrIs != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErrIs)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, tc.verify)
			tc.verify(t, tar.NewReader(&buf))
		})
	}
}
