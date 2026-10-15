package notice

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule 6 for DSH: Follow moves the receiver's follow file to the friend's
// current session; a failed look and a pinned route stay where they are.
func TestDshFollowRewritesTheTargetFile(t *testing.T) {
	t.Parallel()
	events := t.TempDir()
	_, err := dshdesktop.WriteTarget(events, "session-one")
	require.NoError(t, err)
	answers := []struct {
		id  string
		err error
	}{{"session-one", nil}, {"", errors.New("workspace unreadable")}, {"session-two", nil}}
	d := &Dsh{Route: dshdesktop.Route{EventsDir: events, SessionID: "session-one"}, Discover: func() (string, error) {
		a := answers[0]
		answers = answers[1:]
		return a.id, a.err
	}}
	var _ Follower = d

	from, to, moved, err := d.Follow(context.Background())
	require.NoError(t, err)
	assert.False(t, moved)
	assert.Equal(t, []string{"session-one", "session-one"}, []string{from, to})

	_, _, moved, err = d.Follow(context.Background())
	require.Error(t, err)
	assert.False(t, moved)
	assert.Equal(t, "session-one", dshdesktop.ReadTarget(events), "a failed look keeps the target")

	from, to, moved, err = d.Follow(context.Background())
	require.NoError(t, err)
	assert.True(t, moved)
	assert.Equal(t, []string{"session-one", "session-two"}, []string{from, to})
	assert.Equal(t, "session-two", dshdesktop.ReadTarget(events))
	assert.Equal(t, "session-two", d.Route.SessionID)

	pinned := &Dsh{Route: dshdesktop.Route{EventsDir: events, SessionID: "session-two"}}
	_, _, moved, err = pinned.Follow(context.Background())
	require.NoError(t, err)
	assert.False(t, moved, "a pinned route never moves")
}
