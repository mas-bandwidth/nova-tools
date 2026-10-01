package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestQuackReviewRetriesTheSameOperation(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	command := "quack --streams a,b --count 1 --repo https://example.com/quack.git --op quack-pass-one"
	ta.ok(command)
	before := ta.applies()
	code, out, errs := ta.do(command)
	assert.Equal(t, 0, code, "a retry must return the recorded result: %s%s", out, errs)
	assert.Equal(t, before, ta.applies())
}

func TestQuackReviewRefusesWholePassWhenGeneratedIDIsTooLong(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	// The stream id itself is legal; the generated card id exceeds 128 bytes.
	stream := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	code, out, errs := ta.do("quack --streams a," + stream + " --count 1 --repo https://example.com/quack.git")
	assert.NotEqual(t, 0, code, out+errs)
	assert.Equal(t, before, ta.applies(), "a refused pass must add no cards: %s%s", out, errs)
}
