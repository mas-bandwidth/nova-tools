package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A read names its read card; a reader that names the primary instead is
// refused naming the read card it holds for that primary
// (<primary>.r<attempt>.<reader>), asked or begun, so the next call is a
// paste. A name that is no card at all is refused as before.
func TestAReadOfAPrimaryNamesTheReadCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1")
	ta.ok("finish --as m1 s1-1.w1@1 --report done")
	ta.ok("tick")
	ta.ok("read --as reader-b --begin")
	for _, row := range []struct{ line, reader string }{
		{"read --as reader-a --ok s1-1", "reader-a"}, // asked
		{"read --as reader-b --ok s1-1", "reader-b"}, // reading
	} {
		code, _, errs := ta.do(row.line)
		assert.Equal(t, 1, code, row.line)
		assert.Contains(t, errs, "REFUSED s1-1: no such card on the table: s1-1 is a primary, and a read names its read card: s1-1.r1."+row.reader+"\n", row.line)
	}
	code, _, errs := ta.do("read --as reader-a --ok nothing-1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED nothing-1: no such card on the table\n")
	// the named read card is the one the read takes
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
}
