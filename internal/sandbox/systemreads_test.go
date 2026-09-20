//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// nova-tools #893, PR 948 re-cut: linuxReadRoots is the ONE system-reads policy. On
// Linux the sandbox always reads the system roots the resolver and TLS need, enforced
// by addRules and never switched off, because a harness that cannot resolve a name
// inside the sandbox is a sandbox bug, not a network one.
func TestLinuxReadRootsIncludeTheResolverDirectory(t *testing.T) {
	for _, want := range []string{"/etc", "/usr", "/lib", "/lib64", "/run/systemd/resolve"} {
		found := false
		for _, r := range linuxReadRoots {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("linuxReadRoots %v does not list %s; the resolver and TLS need it", linuxReadRoots, want)
		}
	}
}

// nova-tools #1737: on WSL2 the distro's /etc/resolv.conf is a symlink to
// /mnt/wsl/resolv.conf, which sits outside every static read root. Rule 5's roots are the
// directories the kernel will see, so a symlink target outside them leaves glibc with no
// nameserver inside the wall: every name lookup fails with "Could not resolve host" while
// TCP by IP still works. The read set the backend applies must follow the resolver's own
// config through its symlinks and grant the directory it resolves to.
func TestLinuxWallGrantsTheResolvedResolverConfigDirectory(t *testing.T) {
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
		t.Fatal(err)
	}

	saved := resolvConfPath
	resolvConfPath = link
	defer func() { resolvConfPath = saved }()

	roots := linuxRoots()
	for _, r := range roots {
		if r == targetDir {
			return
		}
	}
	t.Fatalf("the resolver config %s resolves to %s, whose directory %s is in no read root the wall applies (%v), so a name lookup inside the wall fails with \"Could not resolve host\" while TCP by IP still works", link, target, targetDir, roots)
}

// The system reads are not a caller switch and there is no second table: a field on
// Input or Policy would be the duplicate policy PR 948 removed.
func TestSystemReadsAreNotAFieldOnInputOrPolicy(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(Input{}), reflect.TypeOf(Policy{})} {
		for _, name := range []string{"SystemReads", "NoSystemReads"} {
			if f, ok := typ.FieldByName(name); ok {
				t.Fatalf("%s still carries %s: linuxReadRoots is the one enforced policy", typ, f.Name)
			}
		}
	}
}
