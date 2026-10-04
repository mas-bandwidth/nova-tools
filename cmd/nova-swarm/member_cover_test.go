package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// coverWaitPacket is the work card the adoption seam is driven with: its card,
// generation and epoch name the launch, hence the pid file, through launchName.
var coverWaitPacket = member.Packet{Card: "c1", Kind: "work", Gen: 1, Attempt: 1, Epoch: 1}

// coverWaitRunner is a nativeRunner whose slots sit in the test's own directory:
// the adoption seam reads only the pid file it names, so nothing else of the
// runner is met on this path (member.go:487-497).
func coverWaitRunner(t *testing.T) *nativeRunner {
	t.Helper()
	slots := filepath.Join(t.TempDir(), "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	return &nativeRunner{slots: slots}
}

// TestMemberCoverNativeChildWait pins Wait (member.go:676, member.Waiter): the channel it
// hands back is the child's own done channel, open while the child runs and closed when it
// has ended. The running child comes through the package's adoption seam — a pid file
// naming this test's own process, which nativeRunner.Start adopts and never runs — and the
// ended one is the shape that wait goroutine leaves behind (member.go:567-574): done
// closed. No sleep and no real time: a receive on the closed channel returns at once, and
// the open one is judged by a non-blocking select whose other arm the live pid makes
// impossible (the adoption poll never ends while this process runs). Changing Wait to
// return a fresh channel goes red on the identity line; closing the adopted child's done
// goes red on the running row; dropping the close goes red on the ended row.
func TestMemberCoverNativeChildWait(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t) // processAlive's signal 0 is POSIX: the seam is unreachable on Windows
	cases := []struct {
		name  string
		ready bool // whether Wait's channel holds a value to receive at the check
		child func(t *testing.T) *nativeChild
	}{
		{"running-adopted", false, func(t *testing.T) *nativeChild {
			r := coverWaitRunner(t)
			write(t, filepath.Join(r.slots, launchName(coverWaitPacket)+".pid"), strconv.Itoa(os.Getpid())+"\n")
			// Removing the adoption branch in Start (member.go:487) goes red here: an
			// empty self path means no fresh launch, so Start fails and no child is had.
			ch, err := r.Start(coverWaitPacket)
			require.NoError(t, err, "Start adopts a pid file naming a live process")
			c, ok := ch.(*nativeChild)
			require.True(t, ok, "the adopted child is %T, want *nativeChild", ch)
			return c
		}},
		{"ended", true, func(t *testing.T) *nativeChild {
			c := &nativeChild{card: coverWaitPacket.Card, done: make(chan struct{})}
			close(c.done) // the wait goroutine's finish (member.go:573)
			return c
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.child(t)
			got := c.Wait()
			assert.True(t, got == c.done, "%s: Wait must return the child's own done channel", tc.name)
			ready := false
			select {
			case <-got:
				ready = true
			default:
			}
			assert.Equal(t, tc.ready, ready, "%s: Wait's channel ready to receive", tc.name)
			assert.Equal(t, tc.ready, c.Done(), "%s: Done must agree with the wait state", tc.name)
		})
	}
}

// TestMemberCoverStartRefusalHandsNoChild pins the one refusal on the way to a child and
// its Wait channel: Start refuses a packet whose card is not a name (member.go:465-467)
// before it claims a launch, so the member is handed no child to wait on and the slots
// hold nothing. Cutting that guard goes red here: the name reaches launchName and a path
// element is built from the unnameable card.
func TestMemberCoverStartRefusalHandsNoChild(t *testing.T) {
	t.Parallel()
	cards := []string{"not a name", "a/b", ""}
	for _, card := range cards {
		card := card
		t.Run("card="+card, func(t *testing.T) {
			t.Parallel()
			r := coverWaitRunner(t)
			p := coverWaitPacket
			p.Card = card
			ch, err := r.Start(p)
			assert.Nil(t, ch, "card %q: a refused packet hands no child", card)
			assert.ErrorContains(t, err, "is not a name", "card %q: the refusal", card)
			entries, err := os.ReadDir(r.slots)
			require.NoError(t, err, "the slots dir is readable after the refusal")
			assert.Empty(t, entries, "card %q: the refusal touched the slots dir", card)
		})
	}
}
