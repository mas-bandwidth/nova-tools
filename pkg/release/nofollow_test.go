package release

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// plantSymlink puts a regular file outside parent and a symlink named name
// inside parent that points at it. The outside file is not a child of parent,
// so a write that follows the link is the only way the body can change.
func plantSymlink(t *testing.T, parent, name, body string) (link, outside string) {
	t.Helper()
	outsideDir := t.TempDir()
	outside = filepath.Join(outsideDir, "target")
	require.NoError(t, os.WriteFile(outside, []byte(body), 0o644))
	require.NoError(t, os.MkdirAll(parent, 0o755))
	link = filepath.Join(parent, name)
	require.NoError(t, os.Symlink(outside, link))
	return link, outside
}

func assertSymlinkRefused(t *testing.T, op, link, outside, before string, err error) {
	t.Helper()
	require.Error(t, err, "a symlink destination was written")
	msg := err.Error()
	for _, want := range []string{op, "destination is a symlink", link, "pass the real file, or remove the link, and retry"} {
		require.Contains(t, msg, want, "error %q does not contain %q", msg, want)
	}
	got, rerr := os.ReadFile(outside)
	require.NoError(t, rerr, "reading outside file: %v", rerr)
	require.Equal(t, before, string(got), "outside file changed from %q to %q", before, got)
	target, rerr := os.Readlink(link)
	require.NoError(t, rerr, "destination is no longer a symlink: %v", rerr)
	require.Equal(t, outside, target, "symlink target = %q, want %q", target, outside)
}

func TestWritePathsFileSymlinkDestination(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	const before = "ORIGINAL-PATHS"
	link, outside := plantSymlink(t, parent, "paths.txt", before)
	err := WritePathsFile(link, "v0...v1", []string{"cmd/nova-update"})
	assertSymlinkRefused(t, "write paths", link, outside, before, err)
}

func TestPrependSectionSymlinkDestination(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	const before = "# nova-tools changelog\n\n## v0 — 2026-09-28\n\n- #1 witness\n"
	link, outside := plantSymlink(t, parent, "CHANGELOG.md", before)
	err := prependSection(link, "## v1 — 2026-09-28\n\n- #2 new\n")
	assertSymlinkRefused(t, "write changelog", link, outside, before, err)
}

func TestInstallFileSymlinkDestination(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	const before = "ORIGINAL-INSTALL"
	// installFile writes the temporary beside dst, then renames. The link
	// has to be that temporary: a link at dst itself is replaced by rename
	// and would not show that the write followed.
	link, outside := plantSymlink(t, parent, ".nova-tool.new", before)
	src := filepath.Join(t.TempDir(), "src")
	require.NoError(t, os.WriteFile(src, []byte("INSTALLED-BODY"), 0o755))
	dst := filepath.Join(parent, "nova-tool")
	err := installFile(src, dst, os.Rename)
	assertSymlinkRefused(t, "install", link, outside, before, err)
	_, statErr := os.Lstat(dst)
	require.Truef(t, os.IsNotExist(statErr), "install renamed onto dst after a refused write: %v", statErr)
}

func TestWriteSumsSymlinkDestination(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	const before = "ORIGINAL-SUMS"
	link, outside := plantSymlink(t, parent, SumsFile, before)
	require.NoError(t, os.WriteFile(filepath.Join(parent, "nova-witness"), []byte("bits"), 0o644))
	_, err := writeSums(parent)
	assertSymlinkRefused(t, "write sums", link, outside, before, err)
}

func TestReadTarSymlinkDestination(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	const before = "ORIGINAL-READTAR"
	link, outside := plantSymlink(t, parent, "got.dat", before)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("TAR-BODY")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "got.dat", Mode: 0o644, Size: int64(len(body))}))
	_, err := tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	err = readTar(&buf, parent)
	assertSymlinkRefused(t, "unpack", link, outside, before, err)
}

func TestNoFollowTrailingSlashSymlinkIsRefused(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	t.Run("directory", func(t *testing.T) {
		outside := t.TempDir()
		secret := filepath.Join(outside, "secret")
		require.NoError(t, os.WriteFile(secret, []byte("SECRET"), 0o644))
		link := filepath.Join(parent, "dirlink")
		require.NoError(t, os.Symlink(outside, link))
		slash := link + string(os.PathSeparator)
		err := writeNoFollow("write", slash, []byte("NOPE"), 0o644)
		require.Error(t, err, "a trailing-slash symlink destination was written")
		msg := err.Error()
		for _, want := range []string{"write", "destination is a symlink", slash, "pass the real file, or remove the link, and retry"} {
			require.Contains(t, msg, want, "error %q does not contain %q", msg, want)
		}
		secretBody, rerr := os.ReadFile(secret)
		require.NoError(t, rerr, "outside file = %q err=%v", secretBody, rerr)
		require.Equal(t, "SECRET", string(secretBody), "outside file = %q err=%v", secretBody, rerr)
		got, rerr := os.Readlink(link)
		require.NoError(t, rerr, "directory link was replaced: %v", rerr)
		require.Equal(t, outside, got, "directory link target = %q, want %q", got, outside)
		entries, rerr := os.ReadDir(outside)
		require.NoError(t, rerr, rerr)
		require.Len(t, entries, 1, "outside directory changed: %v", entries)
		require.Equal(t, "secret", entries[0].Name(), "outside directory changed: %v", entries)
	})

	t.Run("file", func(t *testing.T) {
		const before = "SECRET"
		link, outside := plantSymlink(t, parent, "filelink", before)
		slash := link + string(os.PathSeparator)
		err := writeNoFollow("write", slash, []byte("NOPE"), 0o644)
		require.Error(t, err, "a trailing-slash symlink destination was written")
		msg := err.Error()
		for _, want := range []string{"write", "destination is a symlink", slash, "pass the real file, or remove the link, and retry"} {
			require.Contains(t, msg, want, "error %q does not contain %q", msg, want)
		}
		got, rerr := os.ReadFile(outside)
		require.NoError(t, rerr, "outside file = %q err=%v", got, rerr)
		require.Equal(t, before, string(got), "outside file = %q err=%v", got, rerr)
		target, rerr := os.Readlink(link)
		require.NoError(t, rerr, "link = %q err=%v", target, rerr)
		require.Equal(t, outside, target, "link = %q err=%v", target, rerr)
	})
}

func TestNoFollowCreatesAndReplacesARegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	require.NoError(t, writeNoFollow("write", path, []byte("one"), 0o644))
	require.NoError(t, writeNoFollow("write", path, []byte("two"), 0o644))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "two", string(got), "got %q", got)
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	require.Truef(t, fi.Mode().IsRegular(), "created file is %v, want a regular file", fi.Mode())
}
