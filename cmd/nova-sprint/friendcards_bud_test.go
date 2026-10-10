package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
)

// staleRoster is the bus store as the fleet had it on 2026-10-05: a Redis
// apart from the sprint store, whose `friends` set was filled once by hand and
// never by nova-config's apply, so a bud's row is no name there until
// someone tells it (bus.Enroller, which the fake alone is not).
type staleRoster struct {
	*bustest.Fake
	enrolled []string
}

func (s *staleRoster) Roster(ctx context.Context) ([]string, time.Time, error) {
	names, now, err := s.Fake.Roster(ctx)
	return slices.Concat(names, s.enrolled), now, err
}

func (s *staleRoster) Members(ctx context.Context) ([]string, []string, time.Time, error) {
	friends, machines, now, err := s.Fake.Members(ctx)
	return slices.Concat(friends, s.enrolled), machines, now, err
}

func (s *staleRoster) Enroll(_ context.Context, friends ...string) error {
	for _, f := range friends {
		if !slices.Contains(s.enrolled, f) {
			s.enrolled = append(s.enrolled, f)
		}
	}
	return nil
}

// A card dealt to a bud is delivered on the bus: the bud's name is read from
// the friend row friend sync already has and put on the bus store's roster,
// so the note reaches her stream, her runner's read (the daemon's Recv as
// herself) wakes on it, and `a friend was not told of her card` is never
// written for her. A second deal adds no name again.
func TestADealToABudIsDeliveredOnTheBus(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend bud-a", "bud-a")
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		return map[string]string{busRedisEnv: "127.0.0.1:6381"}[k] + env(k)
	}
	fake := bustest.NewFake(time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC), "coordinator", "ada", "bob")
	fake.Friends = []string{"coordinator", "ada", "bob"}
	store := &staleRoster{Fake: fake}
	ta.a.busOpen = func(context.Context, string, string) (*bus.Bus, func(), error) {
		return &bus.Bus{Store: store}, func() {}, nil
	}
	ta.a.bus = ta.a.sendBus
	ta.ok("tick")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=bud-a card=s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD BUS-NAMES added=bud-a: the bus store now knows the friend row")
	assert.NotContains(t, out, "FRIEND-CARD NOTE", out)
	assert.NotContains(t, out, "is no known name", out)
	assert.NotContains(t, ta.ok("card s1-1"), "a friend was not told of her card")

	require.Equal(t, 1, fake.Len(bus.StreamOf("bud-a")), "the note is on her stream")
	e, ok, err := (&bus.Bus{Store: store}).Recv(context.Background(), "bud-a", 0)
	require.NoError(t, err, "her runner reads as herself: a known name")
	require.True(t, ok, "her runner wakes on the note")
	m := e.Message()
	assert.Equal(t, "coordinator", m.From)
	assert.Equal(t, []string{"bud-a"}, m.To)
	assert.Equal(t, "cards dealt: 1 (s1-1.w1)", m.Subject)

	// a name the roster already holds is not added again, and no name is no name
	added, err := (&bus.Bus{Store: store}).Enroll(context.Background(), "bud-a", "bob")
	require.NoError(t, err)
	assert.Empty(t, added)
	added, err = (&bus.Bus{Store: store}).Enroll(context.Background(), "Bad Name", "bud-b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the name "Bad Name" is not lowercase letters, digits and hyphens`)
	assert.Equal(t, []string{"bud-b"}, added, "the good names are added all the same")
	ta.clean()
}
