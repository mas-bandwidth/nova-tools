package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// The final primary can land after a returned read is reasked in place and
// replaced: the obsolete reader's delayed verdict cannot satisfy acceptance.
func TestTheLastCardLandsAfterReplacingItsReaskedReader(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.a.serveAddr = "mem:0"
	r.ok("add --stream s1 --count 1 --one --brief-file " + proBriefFile(t))
	head := r.head("s1-1", "main", "last.txt", "finished\n")
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/s1-1:refs/heads/sprint/s1-1")
	r.deal(1)
	send := func(line string) sprintwire.Result {
		t.Helper()
		return r.a.serveFrom(sprintwire.Request{Verbs: [][]string{split(line)}}, true).Results[0]
	}
	for _, line := range []string{
		"take --as m1 s1-1.w1@1 --epoch 0",
		"finish --as m1 s1-1.w1@1 --head " + head + " --epoch 0",
		"ask s1-1 --actor coordinator", // both reads, asked together
		"read --as reader-a --ok s1-1.r1.reader-a --epoch 0",
		"read --as reader-b --return s1-1.r1.reader-b --reason no-verdict --epoch 0",
		"ask s1-1 --actor coordinator",
		"read --as reader-b --begin s1-1.r1.reader-b --epoch 0",
		"reader add reader-c --actor coordinator",
		"queue --as reader-c --json",
		"ask s1-1 --instead reader-b --actor coordinator",
	} {
		res := send(line)
		require.Equal(t, 0, res.Code, "%s\n%s%s", line, res.Stdout, res.Stderr)
	}
	late := send("read --as reader-b --ok s1-1.r1.reader-b --epoch 0")
	assert.NotZero(t, late.Code)
	assert.Contains(t, late.Stdout+late.Stderr, "the coordinator took the read back")
	var pending cardView
	r.json("card s1-1", &pending)
	assert.Equal(t, "review", pending.Primary.Col, "the obsolete verdict did not accept the last primary")
	for _, line := range []string{
		"read --as reader-c --ok s1-1.r1.reader-c --epoch 0",
		"accept s1-1 --read-ok --actor coordinator",
	} {
		res := send(line)
		require.Equal(t, 0, res.Code, "%s\n%s%s", line, res.Stdout, res.Stderr)
	}
	r.ok("start")
	r.markProtected()
	assert.Contains(t, r.ok("land --repo-dir "+r.clone+" --base main --check 'test -f last.txt'"), "LAND DONE batches=1 cards=1 refused=0")
	assert.Contains(t, r.ok("tick"), "the sprint is done")
	assert.Contains(t, r.ok("where"), "DONE")
	assert.Contains(t, r.ok("stop --reason r --until 9999h"), "before=STOPPED after=STOPPED unchanged")
	assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
	r.clean()
}
