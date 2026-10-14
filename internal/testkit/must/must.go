// Package must holds the one-line asserts for the multi-line check shapes that
// repeat across many tests and that testify does not already answer in one
// call: a text that must carry every one of several substrings (ContainsAll),
// and a file that must read back as a given body (FileReads). Each function
// takes testing.TB first, calls t.Helper and fails through testify's assert,
// so one bad row does not hide the rest of a table.
//
// A table row reads:
//
//	must.ContainsAll(t, stderr, "REPORT REFUSED", "line 2: unknown kind gadget")
//	must.FileReads(t, path, "name\tkind\n")
package must

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ContainsAll fails the test through assert unless text carries every one of
// the want substrings, naming each one it lacks, so one missing substring does
// not hide the rest.
func ContainsAll(t testing.TB, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		assert.Contains(t, text, w)
	}
}

// FileReads fails the test through assert unless the file at path reads back
// as exactly the want body; a path that does not read is a failure, not a
// skip.
func FileReads(t testing.TB, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		assert.Failf(t, "must.FileReads", "reading %s: %v", path, err)
		return
	}
	assert.Equal(t, want, string(got))
}
