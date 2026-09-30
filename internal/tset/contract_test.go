package tset

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

const missingTSetLogReason = "Layer 2's fragment not landed"

// The source assembler wraps an embedded-FS PathError. An absent different
// fragment, an empty fragment, and a load error must remain test failures.
func missingTSetLogFragment(err error) bool {
	// Follow only a single cause chain. A joined error can contain an
	// unrelated failure and must never defer the test.
	for ; err != nil; err = errors.Unwrap(err) {
		if pathErr, ok := err.(*fs.PathError); ok {
			return pathErr.Path == "lua/table_set_log.lua" && pathErr.Err == fs.ErrNotExist
		}
	}
	return false
}

func requireTSetLogFragment(t testing.TB) {
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

func TestMissingTSetLogFragmentClassifier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"present successful source", nil, false},
		{"bare missing error without path", fs.ErrNotExist, false},
		{"wrapped real log absence", fmt.Errorf("read lua/table_set_log.lua: %w",
			&fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrNotExist}), true},
		{"missing log joined with unrelated failure", errors.Join(
			&fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrNotExist},
			errors.New("unrelated failure")), false},
		{"unrelated failure joined with missing log", errors.Join(errors.New("unrelated failure"),
			&fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrNotExist}), false},
		{"log path with joined causes", &fs.PathError{Op: "open", Path: "lua/table_set_log.lua",
			Err: errors.Join(fs.ErrNotExist, fs.ErrPermission)}, false},
		{"another missing fragment", fmt.Errorf("read lua/table_set_read.lua: %w",
			&fs.PathError{Op: "open", Path: "lua/table_set_read.lua", Err: fs.ErrNotExist}), false},
		{"permission error", &fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrPermission}, false},
		{"unrelated absence alongside log permission", errors.Join(
			&fs.PathError{Op: "open", Path: "lua/table_set_log.lua", Err: fs.ErrPermission},
			&fs.PathError{Op: "open", Path: "lua/table_set_read.lua", Err: fs.ErrNotExist}), false},
		{"present but empty", errors.New("empty function file lua/table_set_log.lua"), false},
		{"present but malformed", errors.New("ERR Error compiling function: syntax error"), false},
		{"present but load rejected", errors.New("ERR Error registering function: duplicate name"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := missingTSetLogFragment(tc.err); got != tc.want {
				t.Errorf("missingTSetLogFragment(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

// These two all-layer aggregate gates remain owed under S18 in
// design/WORK-QUEUE.md. Existing named L1 witnesses do not cover their L2
// and upper-layer rows; a passing alias would misstate the contract.

func TestEveryRefusalPreservesWholeState(t *testing.T) {
	t.Parallel()
	t.Skip("owed: TestEveryRefusalPreservesWholeState; see design/WORK-QUEUE.md S18")
}

func TestAllBoundariesAndExpandedBytes(t *testing.T) {
	t.Parallel()
	t.Skip("owed: TestAllBoundariesAndExpandedBytes; see design/WORK-QUEUE.md S18")
}
