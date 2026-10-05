package main

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

func init() {
	overrideAskVerb()
}

func inReviewFlash(ta *testApp) {
	ta.t.Helper()
	ta.ok("add --stream s1 --count 1 --one")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
}

func TestAskRealStoreTakebacks(t *testing.T) {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	inReviewFlash(ta)

	// 1. First ask: reader-a gets .r1
	ta.ok("ask")
	require.Equal(t, []string{"s1-1.r1.reader-a"}, ta.askedOf("reader-a"))
	require.Empty(t, ta.askedOf("reader-b"))

	// 2. reader-a quiet -> away; ask takes back reader-a (.r1) and asks reader-b (.r1)
	ta.quiet = map[string]bool{"reader-a": true}
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	ta.ok("ask")
	require.Empty(t, ta.askedOf("reader-a"))
	require.Equal(t, []string{"s1-1.r1.reader-b"}, ta.askedOf("reader-b"))

	// 3. reader-b quiet -> away; reader-a up.
	// ask takes back reader-b (.r1) and asks reader-a again -> .t2!
	ta.quiet = map[string]bool{"reader-b": true}
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	ta.ok("ask")
	require.Equal(t, []string{"s1-1.r1.reader-a.t2"}, ta.askedOf("reader-a"))
	require.Empty(t, ta.askedOf("reader-b"))

	// 4. reader-a quiet -> away; reader-b up.
	// ask takes back reader-a (.t2) and asks reader-b again -> .t2!
	ta.quiet = map[string]bool{"reader-a": true}
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	ta.ok("ask")
	require.Empty(t, ta.askedOf("reader-a"))
	require.Equal(t, []string{"s1-1.r1.reader-b.t2"}, ta.askedOf("reader-b"))

	// 5. reader-b quiet -> away; reader-a up.
	// ask takes back reader-b (.t2) and asks reader-a a third time -> .t3!
	ta.quiet = map[string]bool{"reader-b": true}
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	ta.ok("ask")
	require.Equal(t, []string{"s1-1.r1.reader-a.t3"}, ta.askedOf("reader-a"))
	require.Empty(t, ta.askedOf("reader-b"))

	// 6. reader-a quiet -> away; reader-b up.
	// ask takes back reader-a (.t3) and asks reader-b a third time -> .t3!
	ta.quiet = map[string]bool{"reader-a": true}
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	ta.ok("ask")
	require.Empty(t, ta.askedOf("reader-a"))
	require.Equal(t, []string{"s1-1.r1.reader-b.t3"}, ta.askedOf("reader-b"))

	// 7. reader-b quiet -> away; reader-a up.
	// Both readers have had 3 take-backs (MaxReadTakebacks). A 4th ask is refused
	// because reads are exhausted.
	ta.quiet = map[string]bool{"reader-b": true}
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	code, _, errs := ta.do("ask")
	require.Equal(t, 1, code)
	require.Contains(t, errs, "ASK FAILED")
	require.Contains(t, errs, "needs 1 different readers and 0 is free")
	require.Empty(t, ta.askedOf("reader-a"))
	require.Equal(t, []string{"s1-1.r1.reader-b.t3"}, ta.askedOf("reader-b"))
}
