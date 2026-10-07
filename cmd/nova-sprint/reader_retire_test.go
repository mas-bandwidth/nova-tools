package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// reader retire --dry-run says which readers it would retire and writes nothing: the
// reader keeps its state, so a verb that writes has the --dry-run the onboarding standard asks.
func TestReaderRetireDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.readerState("reader-a")
	assert.Contains(t, ta.ok("reader retire --dry-run reader-a"), "READER-RETIRE DRY-RUN readers=reader-a; nothing was changed")
	assert.Equal(t, before, ta.readerState("reader-a"), "a dry run retires no one")
	code, _, errs := ta.do("reader retire --dry-run reader-x")
	assert.Equal(t, 1, code, "a dry run still refuses a name with no row")
	assert.Contains(t, errs, "no reader reader-x")
	ta.clean()
}
