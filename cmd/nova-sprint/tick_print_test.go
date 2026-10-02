package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A tick halted by a stop, and a tick that left moves due past its bounds,
// say so.
func TestATickSaysItHaltedAndWhatIsDue(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	var out, errb bytes.Buffer
	ta.a.printTick(store.TickResult{State: store.Stopped, Halted: "the machine was stopped during the tick: the part deal did not begin"}, nil, 20, &out, &errb)
	ta.a.printTick(store.TickResult{State: store.Running, Due: 50}, nil, 20, &out, &errb)
	got := out.String()
	for _, want := range []string{"HALTED the machine was stopped during the tick: the part deal did not begin",
		"DUE 50 moves past the tick's bounds: the next ticks catch up"} {
		require.Contains(t, got, want)
	}
}
