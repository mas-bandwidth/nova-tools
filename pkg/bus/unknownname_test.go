package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestUnknownNameIsTheRosterRefusalAlone pins UnknownName, which the heartbeat
// server counts a challenge's miss by: true for the send's own refusal of the
// name as no known name, false for a name the refusal does not name, for a
// timeout and for a store that does not answer.
func TestUnknownNameIsTheRosterRefusalAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Unix(1000, 0).UTC(), "ada", "bob")
	b := &Bus{Store: f}
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	assert.True(t, UnknownName(err, "zed"))
	assert.False(t, UnknownName(err, "ada"), "the sender is known")
	assert.False(t, UnknownName(err, "ze"), "the whole name, not a prefix")
	assert.False(t, UnknownName(context.DeadlineExceeded, "zed"))
	assert.False(t, UnknownName(&TimeoutError{Addr: "x", After: time.Second}, "zed"))
	f.Fail = context.DeadlineExceeded
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	assert.False(t, UnknownName(err, "zed"), "a store that does not answer refuses no name")
}
