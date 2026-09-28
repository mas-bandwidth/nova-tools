package sandbox

import (
	"reflect"
	"testing"
)

// TestSystemReadsAreNotAFieldOnInputOrPolicy pins c1cbe386 (#948): linuxReadRoots
// is the one system-reads policy, not a field on Input or Policy. The test that
// landed with that commit lives in systemreads_test.go behind //go:build linux,
// so reverting policy.go on darwin stayed green. This file has no build tag.
func TestSystemReadsAreNotAFieldOnInputOrPolicy(t *testing.T) {
	t.Parallel()

	for _, typ := range []reflect.Type{reflect.TypeOf(Input{}), reflect.TypeOf(Policy{})} {
		for _, name := range []string{"SystemReads", "NoSystemReads"} {
			if f, ok := typ.FieldByName(name); ok {
				t.Fatalf("%s still carries %s: linuxReadRoots is the one enforced policy", typ, f.Name)
			}
		}
	}
}
