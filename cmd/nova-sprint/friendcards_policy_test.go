package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func TestFriendReadBriefUsesEffectiveDeadlineAndRefusesFailedPolicyRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	m := store.NewMem()
	m.SetPolicy(sprint.PolicyFriendReadDeadline, "1h")
	st := &store.Store{B: m, Now: func() time.Time { return now }}
	p := sprint.Packet{ReadJob: "read-c", Primary: "c", Brief: "AS A READ\ncheck\n", Attempt: 1}

	brief, err := friendReadTextContext(context.Background(), st, "amy", p, nil)
	require.NoError(t, err)
	assert.Contains(t, brief, "deadline: "+now.Add(time.Hour).Format(time.RFC3339))

	m.Fail = func(point string) error {
		if point == "policy" {
			return errors.New("policy unavailable")
		}
		return nil
	}
	brief, err = friendReadTextContext(context.Background(), st, "amy", p, nil)
	require.ErrorContains(t, err, "friend_read_deadline")
	assert.Empty(t, brief)
}
