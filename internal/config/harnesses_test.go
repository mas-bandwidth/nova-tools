package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A machine row's harnesses are the harnesses its member can launch: opencode only by
// default, a headless harness only where it is listed, a word that is no harness
// refused at the row, and the list carried to Redis by apply with the rest of the row,
// set alone when it is all that changed (member-draws-only-routes-it-can-launch-b.w1).
func TestAMachineRowListsTheHarnessesItsMemberCanLaunchAndApplyPushesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for field, want := range map[string][]string{
		"":                       {"opencode"},
		"opencode":               {"opencode"},
		"claude,opencode":        {"opencode", "claude"},
		"grok,codex,claude":      {"claude", "codex", "grok"},
		"claude,claude,nonsense": {"claude"},
	} {
		require.Equal(t, want, HarnessesOf(field), "HarnessesOf(%q)", field)
	}

	machine, _ := Lookup(KindMachine)
	row, err := machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)
	require.Equal(t, "opencode", row.Fields["harnesses"], "a row added with no --harnesses lists opencode only")
	_, err = machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "8", "harnesses": "claud"})
	require.Error(t, err, "a word that is no harness is refused at the row")

	st := NewMem()
	_, err = st.Insert(ctx, KindMachine, row, "t")
	require.NoError(t, err)
	keeper, err := machine.NewRow("keeper", map[string]string{"user": "u", "seat": "s", "slots": "8", "harnesses": "opencode,claude"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, keeper, "t")
	require.NoError(t, err)
	hs, err := Harnesses(ctx, st)
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"m1": {"opencode"}, "keeper": {"opencode", "claude"}}, hs)

	ap := newFake()
	_, err = Apply(ctx, st, ap, KindMachine, "t", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, "opencode", ap.views[KindMachine]["m1"]["harnesses"])
	require.Equal(t, "claude,opencode", ap.views[KindMachine]["keeper"]["harnesses"], "apply pushes the harness list with the machine row")

	set, err := machine.Changes(map[string]string{"harnesses": "opencode,codex"}) // machine set m1 --harnesses opencode,codex
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindMachine, "m1", set, "t")
	require.NoError(t, err)
	var ops []string
	_, err = Apply(ctx, st, ap, KindMachine, "t", false, func(op Op) { ops = append(ops, OpLine("APPLY", KindMachine, op)) })
	require.NoError(t, err)
	require.Equal(t, []string{"APPLY SET kind=machine name=m1 changed=harnesses"}, ops)
	require.Equal(t, "codex,opencode", ap.views[KindMachine]["m1"]["harnesses"])
}
