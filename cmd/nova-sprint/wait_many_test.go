package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// wait takes several notes, and a group (wait-many-notes-b.w1): on the twin,
// wait n1,n2 --for 3h sets both, and --group with a size other than --expect
// is refused and changes nothing. Each note is set or refused on its own line.
func TestWaitTakesSeveralNotes(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.deal(3)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	first := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, 1, first.Size, "one failure is a group of one: %+v", first)
	require.Len(t, first.Notes, 1, "one failure is one note: %+v", first)
	ta.failOnce("m1", "s1-2.w1@1", "tests red")
	grown := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, first.ID, grown.ID, "the group grew under another id: %+v", grown)
	require.Equal(t, 2, grown.Size, "the group grew under another size: %+v", grown)
	require.Len(t, grown.Notes, 2, "two failures are two notes: %+v", grown)
	require.False(t, grown.Quiet, "nothing has been waited: %+v", grown)

	// --group with the size from before the second failure: refused, nothing changes.
	code, out, errs := ta.do("wait --group " + grown.ID + " --expect 1 --for 3h")
	require.Equal(t, 1, code, "a grown group: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED group "+grown.ID+": it has 2 now, not 1 as printed; nothing changed", "a grown group: %d\n%s%s", code, out, errs)
	require.NotContains(t, out, "WAIT OK", "a grown group wrote a wait: %s", out)
	require.NotContains(t, out, "MOVED", "a grown group moved a card: %s", out)
	held := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, grown.Notes, held.Notes, "a refused wait changed the notes")
	require.Equal(t, 2, held.Size, "a refused wait changed the size")
	require.False(t, held.Quiet, "a refused wait set a review")

	// one note of the pair, and one id that is not a judgment: each on its own line.
	code, out, errs = ta.do("wait " + grown.Notes[0] + ",no-such-note --for 3h")
	require.Equal(t, 1, code, "one note refused: %d\n%s%s", code, out, errs)
	require.Contains(t, out, "WAIT OK note="+grown.Notes[0]+" review=", "the note that exists was not set:\n%s", out)
	require.NotContains(t, out, "WAIT OK note="+grown.Notes[1], "the note not named was set:\n%s", out)
	require.Contains(t, errs, "WAIT REFUSED note=no-such-note:", "the missing note was not refused on its own line:\n%s", errs)
	require.False(t, ta.group(sprint.NWorkFailed, "s1").Quiet, "the un-named note was set")

	// wait n1,n2 --for 3h sets both.
	want := ta.a.now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	out = ta.ok("wait " + strings.Join(grown.Notes, ",") + " --for 3h")
	var oks []string
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(l, "WAIT OK note=") {
			oks = append(oks, l)
		}
	}
	require.Equal(t, []string{
		"WAIT OK note=" + grown.Notes[0] + " review=" + want,
		"WAIT OK note=" + grown.Notes[1] + " review=" + want,
	}, oks, "both notes, each on its own line:\n%s", out)
	quiet := ta.group(sprint.NWorkFailed, "s1")
	require.True(t, quiet.Quiet, "both notes set, the group is quiet: %+v", quiet)
	require.Contains(t, ta.ok("inbox"), "quiet until=", "the inbox shows the review")

	// the third failure changes the size; --expect of the old size changes nothing.
	ta.failOnce("m1", "s1-3.w1@1", "tests red")
	three := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, grown.ID, three.ID, "the third failure opened another group: %+v", three)
	require.Equal(t, 3, three.Size, "the third failure: %+v", three)
	require.Len(t, three.Notes, 3, "the third failure: %+v", three)
	require.False(t, three.Quiet, "the new note is not waited: %+v", three)
	code, out, errs = ta.do("wait --group " + three.ID + " --expect 2 --for 3h")
	require.Equal(t, 1, code, "a group of another size: %d\n%s%s", code, out, errs)
	require.Contains(t, errs, "REFUSED group "+three.ID+": it has 3 now, not 2 as printed; nothing changed", "a group of another size: %d\n%s%s", code, out, errs)
	require.NotContains(t, out, "WAIT OK", "a group of another size wrote a wait: %s", out)
	still := ta.group(sprint.NWorkFailed, "s1")
	require.Equal(t, three.Notes, still.Notes, "a refused group wait changed the notes")
	require.Equal(t, 3, still.Size, "a refused group wait changed the size")
	require.False(t, still.Quiet, "a refused group wait set the new note")

	// --group with the size it has now sets every note of the group.
	want = ta.a.now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	out = ta.ok("wait --group " + three.ID + " --expect 3 --for 3h")
	for _, id := range three.Notes {
		require.Contains(t, out, "WAIT OK note="+id+" review="+want+"\n", "--group did not set %s:\n%s", id, out)
	}
	require.Contains(t, out, "GROUP "+three.ID+" acted on 3, the group had 3 when printed\n", "--group:\n%s", out)
	require.True(t, ta.group(sprint.NWorkFailed, "s1").Quiet, "--group did not set every note")
}
