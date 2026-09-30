package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// The store tier of IT17 (tick_functional_test.go) waits on gate G0: until
// Layer 2's fragment lua/table_set_log.lua is in the library, the composed
// tset source cannot be assembled, and those tests skip, by the same strict
// single-cause classifier Layer 1's tests use (internal/tset,
// TestMissingTSetLogFragmentClassifier): any other failure to assemble the
// source is a failure, never a skip.

const missingTSetLogReason = "Layer 2's fragment not landed"

// missingTSetLogFragment says an error is the log fragment's absence and
// nothing else: one cause chain, ending in the fragment's PathError of
// fs.ErrNotExist. A joined error, another fragment, an empty or malformed
// fragment, or a load error is not.
func missingTSetLogFragment(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if pathErr, ok := err.(*fs.PathError); ok {
			return pathErr.Path == "lua/table_set_log.lua" && pathErr.Err == fs.ErrNotExist
		}
	}
	return false
}

// requireTSetLogFragment skips a store-tier test while the log fragment is
// absent, and fails it on any other failure to assemble the composed source.
func requireTSetLogFragment(t *testing.T) {
	t.Helper()
	_, err := fn.TSetSource(fn.TSetComposed)
	if err == nil {
		return
	}
	if missingTSetLogFragment(err) {
		t.Skip(missingTSetLogReason)
	}
	t.Fatalf("assemble composed tset source: %v", err)
}

// TestMissingTSetLogFragmentClassifierMachine is Layer 1's classifier table,
// held for this package's copy: only the log fragment's own absence skips.
func TestMissingTSetLogFragmentClassifierMachine(t *testing.T) {
	t.Parallel()
	log := func(err error) error { return &fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: err} }
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"present", nil, false},
		{"bare absence without a path", fs.ErrNotExist, false},
		{"wrapped absence of the log", fmt.Errorf("read lua/table_set_log.lua: %w", log(fs.ErrNotExist)), true},
		{"the log's absence joined with another failure", errors.Join(log(fs.ErrNotExist), errors.New("another failure")), false},
		{"another failure joined with the log's absence", errors.Join(errors.New("another failure"), log(fs.ErrNotExist)), false},
		{"the log's path with joined causes", log(errors.Join(fs.ErrNotExist, fs.ErrPermission)), false},
		{"another fragment absent", &fs.PathError{Op: "open", Path: "lua/table_set_read.lua", Err: fs.ErrNotExist}, false},
		{"permission", log(fs.ErrPermission), false},
		{"empty", errors.New("empty function file lua/table_set_log.lua"), false},
		{"malformed", errors.New("ERR Error compiling function: syntax error"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := missingTSetLogFragment(tc.err); got != tc.want {
				t.Errorf("missingTSetLogFragment(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
