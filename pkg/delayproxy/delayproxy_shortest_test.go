package delayproxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The shortest hold is kept for writes that reached the target, apart from the
// count a write adds before its bytes go out: a write that fails takes its count
// back and never touches the shortest, so it cannot undo a longer successful
// hold. The interleaving is by hand, with no sockets: A is counted with a 5 ms
// hold, B with 10 ms and B reaches the target, then A fails. The test clock
// stands at 1000.0 seconds and is never read but by the proxy it is given.
func TestAFailedWriteNeverUndoesASuccessfulShortestHold(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	p := &Proxy{delay: delay, clock: Clock{
		Now: func() time.Time { return now },
		Wait: func(_ <-chan struct{}, d time.Duration) bool {
			now = now.Add(d)
			return true
		},
	}}
	p.shortest.Store(noHold)

	p.record(5 * time.Millisecond)
	p.record(10 * time.Millisecond)
	p.succeed(10 * time.Millisecond)
	p.unrecord()

	assert.Equal(t, 10*time.Millisecond, p.Shortest(), "Shortest = %v; want the successful 10 ms hold, not undone by the failed 5 ms one", p.Shortest())
	assert.Equal(t, 1, p.Writes(), "Writes = %d; want the one write that reached the target", p.Writes())
}
