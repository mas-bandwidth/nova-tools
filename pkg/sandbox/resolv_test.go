//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// nova-tools #1737: on WSL2 the distro's /etc/resolv.conf is a symlink to
// /mnt/wsl/resolv.conf, which sits outside every static linux read root. Rule 5's roots
// are the directories the kernel will see, so a symlink target outside them leaves glibc
// with no nameserver inside the wall: every name lookup fails with "Could not resolve
// host" while TCP by IP still works.
//
// The path unit is tested here, not the Landlock apply, under the linux tag its subject
// carries: the function is built only where the linux wall is built, and linuxRoots
// applies this directory there.
func TestWSL2ResolverConfigSymlinkDirectoryIsGranted(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	targetDir := filepath.Join(base, "mnt", "wsl")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))
	target := filepath.Join(targetDir, "resolv.conf")
	require.NoError(t, os.WriteFile(target, []byte("nameserver 10.255.255.254\n"), 0o644))
	link := filepath.Join(base, "resolv.conf")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("skipped: cannot create a symlink here (%v); the WSL2 fixture is a symlink", err)
	}

	want, err := filepath.EvalSymlinks(targetDir)
	require.NoError(t, err)
	dir := resolverConfigDirectory(link)
	require.Equal(t, want, dir, "the resolver config %s resolves to %s, whose directory is %s, but resolverConfigDirectory returned %q; a name lookup inside the wall fails with \"Could not resolve host\" while TCP by IP still works", link, target, want, dir)
	require.NotEqual(t, filepath.Dir(link), dir, "granted the symlink's own directory %s, which is the /etc case the static table already has; the WSL2 target sits outside it", dir)
}
