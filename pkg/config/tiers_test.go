package config

import (
	"io/fs"
	"regexp"
	"sort"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/stretchr/testify/require"
)

var (
	capacityTiersLine = regexp.MustCompile(`local TIERS = \{([^}]*)\}`)
	capacityTierWord  = regexp.MustCompile(`'([a-z]+)'`)
)

// TestTiersMatchCapacityFilter reads the one tier list out of capacity.lua and
// holds it equal to Tiers: the Go list apply validates a friend's row against
// and the Lua list capacity_desired's filter_ok accepts are the same list, so
// a friend row apply accepts is never refused by the runtime (the 2026-10-04
// "INVALID studio 0 0" refusal, where heavy was in Tiers but not filter_ok).
func TestTiersMatchCapacityFilter(t *testing.T) {
	t.Parallel()

	b, err := fs.ReadFile(fn.Spec().Files, "lua/capacity.lua")
	require.NoError(t, err, "read capacity.lua")
	m := capacityTiersLine.FindSubmatch(b)
	require.NotNil(t, m, "capacity.lua has no `local TIERS = { ... }` list")
	var lua []string
	for _, w := range capacityTierWord.FindAllSubmatch(m[1], -1) {
		lua = append(lua, string(w[1]))
	}
	require.NotEmpty(t, lua, "capacity.lua's TIERS list is empty")
	got, want := append([]string(nil), lua...), append([]string(nil), Tiers...)
	sort.Strings(got)
	sort.Strings(want)
	require.Equal(t, want, got, "capacity.lua's filter_ok tiers and config.Tiers have drifted; one list only")
}
