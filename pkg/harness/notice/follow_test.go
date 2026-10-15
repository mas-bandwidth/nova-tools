package notice

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codexFollowRig is a Codex route whose friend can move to a new chat: the
// current chat is a variable the test sets, as the Codex home's newest chat
// would say it. Deliver records which chat each push went to.
func codexFollowRig(t *testing.T) (*rig, *string, *[]string, *error) {
	current := "chat-old"
	var lookErr error
	var into []string
	r := newRig(t, func(*testing.T) *harness {
		noop := func(*testing.T) {}
		return &harness{busy: noop, idle: noop, shim: &Codex{Socket: "/owned/ipc.sock", Thread: "chat-old", Directory: "/private/events",
			Deliver: func(_ context.Context, _, thread, id, _ string) (string, error) {
				into = append(into, thread)
				return "turn-" + id, nil
			},
			Discover: func() (string, error) { return current, lookErr },
		}}
	})
	return r, &current, &into, &lookErr
}

func TestCodexFollowsTheFriendToANewChat(t *testing.T) {
	t.Parallel()
	r, current, into, _ := codexFollowRig(t)
	r.arrive(r.store.challenge("first"))
	require.Equal(t, []string{"chat-old"}, *into)
	pending, _ := r.core.Pending()

	*current = "chat-new" // Stella starts a new chat
	r.store.clock += FollowEvery / 2
	require.NoError(t, r.core.Step(context.Background()))
	assert.Equal(t, []string{"chat-old"}, *into, "the route looks once a FollowEvery")

	r.store.clock += FollowEvery / 2
	require.NoError(t, r.core.Step(context.Background()))
	assert.Equal(t, []string{"chat-old", "chat-new"}, *into, "the pending notice is told once to the chat she is in")
	again, ok := r.core.Pending()
	require.True(t, ok)
	assert.Equal(t, pending.ID, again.ID, "the same notice: still one pending")
	assert.Equal(t, "chat-new", r.core.Shim.(*Codex).Thread)

	assert.Equal(t, "first", r.act(), "she proves from the new chat")
	r.store.clock += hook.ChallengeEvery
	r.arrive(r.store.challenge("second"))
	assert.Equal(t, []string{"chat-old", "chat-new", "chat-new"}, *into, "later notices go to the new chat")
}

func TestCodexFollowFailureKeepsTheRoute(t *testing.T) {
	t.Parallel()
	r, current, into, lookErr := codexFollowRig(t)
	r.arrive(r.store.challenge("first"))
	*current, *lookErr = "", errors.New("codex chat header: unexpected end of JSON input")
	r.store.clock += FollowEvery
	require.NoError(t, r.core.Step(context.Background()), "a failed look never stops the route")
	assert.Equal(t, "chat-old", r.core.Shim.(*Codex).Thread)
	assert.Equal(t, []string{"chat-old"}, *into)
	assert.Zero(t, r.store.unproved)
}

func TestCodexNamedThreadDoesNotFollow(t *testing.T) {
	t.Parallel()
	c := &Codex{Socket: "/owned/ipc.sock", Thread: "chat-named", Directory: "/private/events"}
	from, to, moved, err := c.Follow(context.Background())
	require.NoError(t, err)
	assert.False(t, moved)
	assert.Equal(t, "chat-named", from)
	assert.Equal(t, "chat-named", to)
}
