package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// nova-tools #1737: on WSL2 the distro's /etc/resolv.conf is a symlink to
// /mnt/wsl/resolv.conf, which sits outside every static linux read root. Rule 5's roots
// are the directories the kernel will see, so a symlink target outside them leaves glibc
// with no nameserver inside the wall: every name lookup fails with "Could not resolve
// host" while TCP by IP still works.
//
// The path unit is tested here, not the Landlock apply, so the same WSL2 fixture runs on
// Darwin (winpath_test.go's shape: the path logic of a platform this build does not run
// on is proved where the studio sits). linuxRoots applies this directory on linux.
func TestWSL2ResolverConfigSymlinkDirectoryIsGranted(t *testing.T) {
	base := t.TempDir()
	targetDir := filepath.Join(base, "mnt", "wsl")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "resolv.conf")
	if err := os.WriteFile(target, []byte("nameserver 10.255.255.254\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "resolv.conf")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("skipped: cannot create a symlink here (%v); the WSL2 fixture is a symlink", err)
	}

	want, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := resolverConfigDirectory(link)
	if dir != want {
		t.Fatalf("the resolver config %s resolves to %s, whose directory is %s, but resolverConfigDirectory returned %q; a name lookup inside the wall fails with \"Could not resolve host\" while TCP by IP still works", link, target, want, dir)
	}
	if dir == filepath.Dir(link) {
		t.Fatalf("granted the symlink's own directory %s, which is the /etc case the static table already has; the WSL2 target sits outside it", dir)
	}
}
