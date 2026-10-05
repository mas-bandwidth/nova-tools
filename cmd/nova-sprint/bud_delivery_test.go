package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
)

// staleRoster is a bus store whose sets were written once, before a friend's row was
// added: the fleet's bus store is its own Redis (fleet:bus), and nova-config apply writes
// the friend rows into the sprint store alone. AddFriends adds to its set `friends`, as
// bus.Redis does with SADD.
type staleRoster struct {
	*bustest.Fake
	mu    sync.Mutex
	added []string
	adds  int
}

func (s *staleRoster) Members(ctx context.Context) ([]string, []string, time.Time, error) {
	friends, machines, now, err := s.Fake.Members(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(friends, s.added...), machines, now, err
}

func (s *staleRoster) Roster(ctx context.Context) ([]string, time.Time, error) {
	friends, machines, now, err := s.Members(ctx)
	return append(friends, machines...), now, err
}

func (s *staleRoster) AddFriends(_ context.Context, names ...string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adds++
	var added []string
	for _, n := range names {
		if !slices.Contains(s.added, n) {
			s.added = append(s.added, n)
			added = append(added, n)
		}
	}
	return added, nil
}

// A card dealt to a bud is delivered on the bus: her name is a nova-config friend row,
// so the deal's note to her is sent although the bus store's roster predates her row
// (her name is copied into it first, and said once), her runner's recv takes the note,
// and `a friend was not told of her card` is never written for her.
func TestADealToABudIsDeliveredOnTheBus(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend rowan-space", "rowan-space")
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		return map[string]string{busRedisEnv: "bus.test:6381"}[k] + env(k)
	}
	store := &staleRoster{Fake: bustest.NewFake(time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC), "coordinator", "studio")}
	store.Friends = []string{"coordinator"}
	ta.a.busOpen = func(context.Context, string, string) (*bus.Bus, func(), error) {
		return &bus.Bus{Store: store}, func() {}, nil
	}
	ta.a.bus = ta.a.sendBus

	ta.ok("tick")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=rowan-space card=s1-1.w1")
	assert.NotContains(t, out, "was not sent", out)
	assert.Contains(t, out, "FRIEND-CARD BUS-NAME friend=rowan-space: her nova-config friend row was no name on the bus store bus.test:6381; it is one now")
	assert.NotContains(t, ta.ok("card s1-1"), "a friend was not told of her card")
	assert.Equal(t, 1, store.Len(bus.StreamOf("rowan-space")), "the note is on her stream")

	// her runner wakes on the note: its recv as her takes it, the card and its inbox named
	b := &bus.Bus{Store: store}
	e, ok, err := b.Recv(context.Background(), "rowan-space", 0)
	require.NoError(t, err)
	require.True(t, ok, "the note is there for her recv")
	m := e.Message()
	assert.Equal(t, "coordinator", m.From)
	assert.True(t, strings.HasPrefix(m.Subject, "card s1-1.w1 dealt: "), m.Subject)
	assert.Contains(t, m.Body, "inbox")

	// a name already there is copied no more and said no more
	require.NoError(t, ta.a.sendBus(context.Background(), bus.Message{From: "coordinator", To: []string{"rowan-space"}, Subject: "again", Body: "x"}, func(l string) {
		t.Errorf("a second send said %q", l)
	}))
	assert.Equal(t, 1, store.adds, "the roster is read before it is written: a known name is never added again")
}
