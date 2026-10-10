package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// stalePlacementBackend leaves the table placement intact while supplying a
// conflicting legacy hash field, as a reader can encounter in an old record.
type stalePlacementBackend struct{ store.Backend }

func (b stalePlacementBackend) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	res, err := b.Backend.ReadSet(ctx, table, ids)
	for i := range res.Members {
		if table == sprint.Work {
			res.Members[i].Fields["place:work"] = "s1:landed"
		}
	}
	return res, err
}

// SPEC-SPRINT section 11: card, needs and bulk readers name the same live
// table column; a waiting card's column is distinct from its merging need's.
func TestCardStateIsTheTableColumnInEveryReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.toMerging("s1")
	ta.ok("add --one --stream s2 waiter --needs s1-1")
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) {
		return stalePlacementBackend{ta.m}, nil
	}

	type row struct {
		ID     string            `json:"id"`
		Column string            `json:"column"`
		State  string            `json:"state"`
		Fields map[string]string `json:"fields"`
	}
	for _, tc := range []struct {
		id, column string
	}{{"waiter", sprint.Waiting}, {"s1-1", sprint.Merging}} {
		t.Run(tc.id, func(t *testing.T) {
			var card struct {
				Column  string `json:"column"`
				Primary row    `json:"primary"`
			}
			ta.json("card "+tc.id, &card)
			assert.Equal(t, "s1:landed", card.Primary.Fields["place:work"], "the record disagrees with its table row")
			assert.Equal(t, tc.column, card.Column)
		})
	}

	var needs struct {
		Streams []struct {
			Cards []struct {
				row
				Needs []row `json:"needs"`
			} `json:"cards"`
		} `json:"streams"`
	}
	ta.json("needs --stream s2", &needs)
	require.Len(t, needs.Streams, 1)
	require.Len(t, needs.Streams[0].Cards, 1)
	waiting := needs.Streams[0].Cards[0]
	assert.Equal(t, "waiter", waiting.ID)
	assert.Equal(t, sprint.Waiting, waiting.Column)
	require.Len(t, waiting.Needs, 1)
	assert.Equal(t, "s1-1", waiting.Needs[0].ID)
	assert.Equal(t, sprint.Merging, waiting.Needs[0].Column)
	out := ta.ok("needs --stream s2")
	assert.Contains(t, out, "card waiter column waiting")
	assert.Contains(t, out, "need s1-1 column merging")

	var where struct {
		Rows []row `json:"rows"`
	}
	ta.json("where --rows", &where)
	require.Len(t, where.Rows, 4)
	for _, r := range where.Rows {
		want := sprint.Merging
		if r.ID == "waiter" {
			want = sprint.Waiting
		}
		assert.Equal(t, want, r.Column, r.ID)
		assert.Equal(t, r.Column, r.State, "state remains a deprecated alias for one release")
	}
	lines := strings.Split(strings.TrimSpace(ta.ok("card --all --json")), "\n")
	require.Len(t, lines, 4)
	for _, line := range lines {
		var r row
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		want := sprint.Merging
		if r.ID == "waiter" {
			want = sprint.Waiting
		}
		assert.Equal(t, want, r.Column, r.ID)
	}
	ta.clean()
}
