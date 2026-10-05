package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// reader up of a reader no process serves is refused, exit 1 and nothing
// written, naming the beat that would serve it; the readers table shows each
// row served or unserved from its beat (reader.go). The incident, 2026-10-05:
// reader up of two readers printed OK, and both were away on the
// next tick because nothing served them.
func TestReaderUpRefusesAReaderNobodyServes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.quiet = map[string]bool{"reader-x": true, "reader-y": true}
	ta.ok("init --readers reader-a,reader-b,reader-x,reader-y --members m1")

	// the incident's call: refused, both named, each with the beat that would serve it
	code, out, errs := ta.do("reader up reader-x reader-y")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.NotContains(t, out, "READER-UP OK")
	assert.Contains(t, errs, "no process serves the reader")
	assert.Contains(t, errs, "no process has ever beaten for reader-x")
	assert.Contains(t, errs, "no process has ever beaten for reader-y")
	assert.Contains(t, errs, "nova-sprint queue --as reader-x")
	assert.Contains(t, errs, "nova-swarm member --reader --as reader-y on y")
	assert.Contains(t, errs, "nothing was changed")

	// one unserved reader refuses the whole call: a served reader held stays held
	ta.ok("reader away reader-a")
	code, out, errs = ta.do("reader up reader-a reader-x")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Equal(t, sprint.ReaderHeld, ta.readerState("reader-a"), "the call was refused: nothing was written")
	assert.Contains(t, ta.ok("reader up reader-a"), "READER-UP OK readers=reader-a")
	assert.Equal(t, sprint.ReaderUp, ta.readerState("reader-a"))

	// the rows say which are served
	served := func() map[string]string {
		var v struct {
			Tables map[string]map[string]map[string]string
		}
		ta.json("where", &v)
		out := map[string]string{}
		for r, cells := range v.Tables[sprint.Readers] {
			out[r] = cells["served"]
		}
		return out
	}
	assert.Equal(t, map[string]string{"reader-a": "served", "reader-b": "served", "reader-x": "unserved", "reader-y": "unserved"}, served())
	assert.Regexp(t, `served[\s\S]*\| 2/4 `, ta.ok("where --all"), "the summed readers row counts the served")

	// a beat serves it: reader up is then OK
	ta.ok("queue --as reader-x")
	assert.Equal(t, "served", served()["reader-x"])
	assert.Contains(t, ta.ok("reader up reader-x"), "READER-UP OK readers=reader-x")

	// a beat past the bound serves it no more, and the refusal says how old it is
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	code, _, errs = ta.do("reader up reader-x")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "reader-x's last beat was")
	assert.Equal(t, "unserved", served()["reader-x"])

	// --unserved releases the hold anyway, and the reader stays as its beat says
	require.Contains(t, ta.ok("reader up reader-y --unserved"), "READER-UP OK readers=reader-y")
	assert.Equal(t, sprint.ReaderDown, ta.readerState("reader-y"))
}
