package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A reader's queue says whether its name is a row of the readers table (reader):
// the queue of a name with no row writes no beat, and the reader loop says so
// (pkg/member; nova-tools#5096 item 23). reader add makes the row; the beat
// never does.
func TestAReadersQueueSaysWhetherItIsARowOfTheReadersTable(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	var q struct {
		Reader *bool `json:"reader"`
	}
	ta.json("queue --as reader-a", &q)
	if assert.NotNil(t, q.Reader) {
		assert.True(t, *q.Reader)
	}
	q.Reader = nil
	ta.json("queue --as reader-x-2", &q)
	if assert.NotNil(t, q.Reader) {
		assert.False(t, *q.Reader, "a name with no row: its queue is no beat")
	}
	assert.NotContains(t, ta.readerRows(), "reader-x-2", "a queue never makes the row")
	ta.ok("reader add reader-x-2")
	q.Reader = nil
	ta.json("queue --as reader-x-2", &q)
	if assert.NotNil(t, q.Reader) {
		assert.True(t, *q.Reader)
	}
}
