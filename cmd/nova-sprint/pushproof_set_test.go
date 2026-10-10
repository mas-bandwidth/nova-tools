package main

import (
	"context"
	"encoding/json"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAStartWithAStaleBusPushIsRefusedNamingTheCommand(t *testing.T) {
	t.Parallel()
	const name = "push-set-stale-bus"
	ta, _ := pushProofSprint(t, name)
	ta.ok("init --readers reader-a --members m1")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	rec := sprint.PushRecord{Name: name, Harness: "claude", Adapter: sprint.AdapterFolder, Target: t.TempDir(), Nonce: "n1", Sent: ta.now, Proven: ta.now, PongOf: "n1"}
	raw, err := json.Marshal(struct {
		sprint.PushRecord
		Watches map[string]map[string]any `json:"watches"`
	}{rec, map[string]map[string]any{
		"bus":         {"at": ta.now.Add(-3*time.Minute - time.Second), "epoch": 0, "generation": 1, "target": rec.Target, "session": rec.Session},
		"friends":     {"at": ta.now, "epoch": 0, "generation": 1, "target": rec.Target, "session": rec.Session},
		"transitions": {"at": ta.now, "epoch": 0, "generation": 1, "target": rec.Target, "session": rec.Session},
	}})
	require.NoError(t, err)
	require.NoError(t, st.B.(store.KV).SetKey(context.Background(), store.SeatPushKey(name), string(raw)))
	before := ta.applies()
	code, out, errs := ta.do("start")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "PUSH DOWN")
	assert.Contains(t, errs, "bus")
	assert.Contains(t, errs, "nova-bus recv --as "+name+" --forever --exec")
	assert.Equal(t, before, ta.applies(), "a stale push refuses before changing who works")
	machine, _, err := st.Machine(context.Background())
	require.NoError(t, err)
	assert.False(t, machine.Running(), "the refused start leaves the sprint stopped")
}
