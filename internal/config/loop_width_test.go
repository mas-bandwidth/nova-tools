package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A loop carries no width of its own: a nova-swarm member's width, a reader's
// too, is its machine row's (machine set <m> --width <n>), read from the fleet
// row every tick, so an argv that spells --width is refused naming the rule
// (the owner, 2026-10-02: "The reader widths seem to be very ad-hoc, unlike the
// machine widths"; "why not just have as many readers as workers
// per-machine"). Another program's --width is its own.
func TestALoopArgvCarryingAWidthIsRefused(t *testing.T) {
	t.Parallel()
	loop, ok := Lookup(KindLoop)
	require.True(t, ok)
	for _, c := range []struct {
		name string
		argv string
		want string
	}{
		{"a reader's --width", `["nova-swarm","member","--as","reader-m1","--reader","--width","8"]`, "its machine row's"},
		{"a member's --width", `["/opt/bin/nova-swarm","member","--as","m1","--width=4"]`, "its machine row's"},
		{"a single dash", `["nova-swarm","member","-width","4"]`, "its machine row's"},
		{"after -- it is the child's", `["nova-swarm","member","--","--width","4"]`, ""},
		{"another program's", `["/bin/other","--width","4"]`, ""},
		{"no width", `["nova-swarm","member","--as","reader-m1","--reader"]`, ""},
	} {
		_, err := loop.NewRow("l", map[string]string{"machine": "m1", "argv": c.argv, "keepalive": "true"})
		if c.want == "" {
			assert.NoError(t, err, c.name)
			continue
		}
		require.Error(t, err, c.name)
		assert.Contains(t, err.Error(), c.want, c.name)
		assert.Contains(t, err.Error(), "--width", c.name)
	}
}

// The loop kind has no width field: the plays render the argv as written.
func TestTheLoopKindHasNoWidthField(t *testing.T) {
	t.Parallel()
	loop, ok := Lookup(KindLoop)
	require.True(t, ok)
	_, has := loop.Field("width")
	assert.False(t, has, "fields: %v", loop.FieldNames())
}
