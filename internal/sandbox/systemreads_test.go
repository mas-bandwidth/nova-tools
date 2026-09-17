//go:build linux

package sandbox

import (
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
