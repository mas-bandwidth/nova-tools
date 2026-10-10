package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
)

// deletes= on SANDBOX OK is the --write roots in the order given, each a oneline field,
// joined by "," with a "," inside a path escaped so the list splits back into its roots;
// a wall that grants no delete prints "-" (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root").
func TestTheDeletesFieldNamesEveryWriteRoot(t *testing.T) {
	t.Parallel()
	p := &sandbox.Policy{Writes: []string{"/slot/jobs/card", "/slot/data", "/slot/a,b", "/slot/c d"}}
	assert.Equal(t, `/slot/jobs/card,/slot/data,/slot/a\x2cb,/slot/c\x20d`, deletesField(p))
	assert.Equal(t, "-", deletesField(&sandbox.Policy{}))
	assert.Equal(t, "-", deletesField(nil))
}
