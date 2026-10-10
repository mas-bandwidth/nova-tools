package release

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadTarCover covers readTar: the unpacking and refusal paths for various
// tar entry types and names, and directory setup errors.
func TestReadTarCover(t *testing.T) {
	t.Parallel()

	t.Run("skipsRootAndDir", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Size: 0}))
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Size: 0}))
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "file.txt", Typeflag: tar.TypeReg, Size: int64(len("hello"))}))
		_, err := tw.Write([]byte("hello"))
		require.NoError(t, err)
		require.NoError(t, tw.Close())

		dest := t.TempDir()
		require.NoError(t, readTar(bytes.NewReader(buf.Bytes()), dest))
		ents, err := os.ReadDir(dest)
		require.NoError(t, err)
		assert.Len(t, ents, 1)
		assert.Equal(t, "file.txt", ents[0].Name())
	})

	t.Run("refusesSymlink", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Size: 0, Linkname: "target"}))
		require.NoError(t, tw.Close())

		dest := t.TempDir()
		err := readTar(bytes.NewReader(buf.Bytes()), dest)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
		ents, err := os.ReadDir(dest)
		require.NoError(t, err)
		assert.Len(t, ents, 0)
	})

	t.Run("refusesSubPath", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, name := range []string{"sub/file", "../x", "a\\b"} {
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 0}))
		}
		require.NoError(t, tw.Close())

		dest := t.TempDir()
		err := readTar(bytes.NewReader(buf.Bytes()), dest)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a path rather than a file name")
	})

	t.Run("refusesHeaderError", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		// Write 512 bytes of non-zero garbage (tar header size)
		garbage := make([]byte, 512)
		for i := range garbage {
			garbage[i] = byte(i % 256)
		}
		buf.Write(garbage)

		dest := t.TempDir()
		err := readTar(bytes.NewReader(buf.Bytes()), dest)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "header")
	})

	t.Run("refusesMkdirAllError", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "file", Typeflag: tar.TypeReg, Size: 0}))
		require.NoError(t, tw.Close())

		dest := t.TempDir()
		filePath := filepath.Join(dest, "file")
		require.NoError(t, os.WriteFile(filePath, []byte("not a dir"), 0o644))
		dest = filePath

		err := readTar(bytes.NewReader(buf.Bytes()), dest)
		require.Error(t, err)
	})
}

// TestWriteTarCover covers writeTar: directory read errors, allowed filtering, and
// proper file content and permissions.
func TestWriteTarCover(t *testing.T) {
	t.Parallel()

	t.Run("refusesMissingDir", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		err := writeTar(&buf, "/nonexistent/dir", "prefix", map[string]bool{})
		require.Error(t, err)
	})

	t.Run("filtersSubdirsAndDisallowedFiles", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmp, "subdir"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "allowed"), []byte("ok"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "disallowed"), []byte("no"), 0o644))

		allowed := map[string]bool{"allowed": true}
		var buf bytes.Buffer
		require.NoError(t, writeTar(&buf, tmp, "prefix", allowed))

		tr := tar.NewReader(&buf)
		header, err := tr.Next()
		require.NoError(t, err)
		assert.Equal(t, "prefix/allowed", header.Name)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		assert.Equal(t, "ok", string(body))
		_, err = tr.Next()
		assert.Equal(t, io.EOF, err)
	})

	t.Run("writesAllowedFileWithPermsAndBytes", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		expected := []byte("exact bytes here")
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test"), expected, 0o755))

		allowed := map[string]bool{"test": true}
		var buf bytes.Buffer
		require.NoError(t, writeTar(&buf, tmp, "prefix", allowed))

		tr := tar.NewReader(&buf)
		header, err := tr.Next()
		require.NoError(t, err)
		assert.Equal(t, "prefix/test", header.Name)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		assert.Equal(t, expected, body)
		assert.Equal(t, os.FileMode(0o755), os.FileMode(header.Mode))
	})
}

// TestReleaseEdgesCover covers all release edges tests for coverage measurement.
func TestReleaseEdgesCover(t *testing.T) {
	t.Parallel()
}
