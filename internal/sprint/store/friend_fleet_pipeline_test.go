package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friendSetHook answers only the existing read-set protocol; it cannot dial.
type friendSetHook struct {
	t                   *testing.T
	pipelines, commands int
	fault               string
}

func (*friendSetHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("the read-set fixture cannot dial")
	}
}
func (*friendSetHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(context.Context, redis.Cmder) error {
		return errors.New("the read-set fixture accepts pipelines only")
	}
}
func (h *friendSetHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, commands []redis.Cmder) error {
		h.pipelines++
		require.LessOrEqual(h.t, len(commands), 8)
		for _, command := range commands {
			h.commands++
			args := command.Args()
			require.Len(h.t, args, 7, "old-epoch reads retain the pinned epoch")
			require.Equal(h.t, ntable.FnReadSet, args[1])
			require.Equal(h.t, "7", args[6])
			var scope struct {
				Members []string `json:"members"`
				Fields  []string `json:"fields"`
			}
			require.NoError(h.t, json.Unmarshal([]byte(fmt.Sprint(args[5])), &scope))
			require.LessOrEqual(h.t, len(scope.Members), ntable.LimitReadSetMembers)
			require.Equal(h.t, []string{sprint.FieldUsage}, scope.Fields)
			var members []any
			for _, id := range scope.Members {
				members = append(members, []any{id, "1", "1", "friend.alpha", sprint.DoneOK, "1", []any{sprint.FieldUsage, "input=11 output=7 actual_usd=0.125 cost=actual"}})
			}
			epoch, revision := "7", "12"
			var missing []any
			if h.commands == 1 {
				switch h.fault {
				case "epoch":
					epoch = "8"
				case "revision":
					revision = "13"
				case "missing":
					missing = []any{scope.Members[0]}
				case "unplaced":
					members[0].([]any)[2] = "0"
				case "error":
					return errors.New("pipeline failed")
				}
			}
			command.(*redis.Cmd).SetVal([]any{"SET", "t-fleet", epoch, revision, members, missing})
		}
		return nil
	}
}

// Current-size chunks share one Redis flight; a hundred-times history is never
// queued whole. Epoch, revision, missing, placement and transport failures remain
// refusals, and the usage field needed for exact totals is preserved.
func TestFriendUsageReadSetsAreBatchedAndFailClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fault      string
		count, pipelines int
	}{
		{"small", "", 8, 1}, {"current", "", 4479, 1}, {"bounded next flight", "", 8193, 2},
		{"epoch", "epoch", 2048, 1}, {"revision", "revision", 2048, 1}, {"missing", "missing", 2048, 1}, {"unplaced", "unplaced", 2048, 1}, {"transport", "error", 2048, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := redis.NewClient(&redis.Options{})
			defer func() { assert.NoError(t, client.Close()) }()
			hook := &friendSetHook{t: t, fault: tc.fault}
			client.AddHook(hook)
			st := &Store{B: &Redis{C: client, Old: true, Pinned: 7}, Names: sprint.Names{Prefix: "t-"}}
			fleet := sprint.NewTable(sprint.Fleet)
			fleet.Epoch, fleet.Revision = 7, 12
			ids := make([]string, tc.count)
			for i := range ids {
				ids[i] = fmt.Sprintf("usage-%d", i)
			}
			err := st.readFriendFleetInto(context.Background(), fleet, ids)
			if tc.fault != "" {
				if tc.fault == "error" {
					assert.ErrorContains(t, err, "pipeline failed")
				} else {
					assert.ErrorIs(t, err, errMoved)
				}
			} else {
				require.NoError(t, err)
				assert.Len(t, fleet.Cards(), tc.count)
				assert.EqualValues(t, 18*tc.count, sprint.FriendTokensFromCards(fleet.Cards()))
				assert.Equal(t, "", fleet.Cards()[0].F("other"), "only explicit usage fields reach this private projection")
			}
			assert.Equal(t, tc.pipelines, hook.pipelines)
			if tc.fault == "" {
				assert.Equal(t, (tc.count+1023)/1024, hook.commands)
			}
		})
	}
}
