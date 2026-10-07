package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestBackupRecoveryCommandsReachTheStore runs each nova-sprint command the
// backup judgment prints. NOVA_SPRINT_PREFIX is set, so a command the grammar
// accepts reaches the store and stops there: no Redis, no write. A command
// the grammar refuses stops earlier, and nova-sprint has no route verb.
func TestBackupRecoveryCommandsReachTheStore(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{
		Readers: sprint.NewTable(sprint.Readers),
		Fleet:   sprint.NewTable(sprint.Fleet),
		Merge:   sprint.NewTable(sprint.Merge),
		Routes: []sprint.Route{
			{Name: "flash-off", Enabled: false},
		},
	}
	s.Readers.SetRows([]string{"reader-m1", "reader-free"})
	s.Fleet.SetRows([]string{"m1"})
	s.Fleet.Put(&sprint.Card{ID: "ctl-m1", Row: "m1", Col: sprint.Ctl, Score: 1, Rev: 1, Fields: map[string]string{sprint.FieldWidth: "4"}})
	s.Merge.SetRows([]string{"s9"})
	s.Merge.Put(&sprint.Card{ID: "ctl-s9", Row: "s9", Col: sprint.Ctl, Score: 1, Rev: 1, Fields: map[string]string{"state": sprint.StreamStopped}})

	var sprintLines, configLines int
	for _, line := range sprint.BackupRecoveryCommands(s) {
		switch {
		case strings.HasPrefix(line, "nova-sprint "):
			sprintLines++
			code, errs := runPrefixed(t, strings.Fields(line)[1:])
			require.Equal(t, 2, code, "%s: %s", line, errs)
			require.Contains(t, errs, "NOVA_SPRINT_PREFIX is set", line)
			require.NotContains(t, errs, "wants ", line)
		case strings.HasPrefix(line, "nova-config "):
			configLines++
			require.True(t, strings.HasPrefix(line, "nova-config route set flash-off --enabled true") || line == "nova-config apply --kind route", line)
		default:
			t.Fatalf("not a command the judgment may print: %s", line)
		}
	}
	require.Equal(t, 3, sprintLines)
	require.Equal(t, 2, configLines)

	// The forms attempt 2 printed: missing values, or a verb nova-sprint has not got.
	// Each stops before the store.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"reader", "set", "--tiers"}, "--tiers wants a value"},
		{[]string{"reader", "set", "reader-m1"}, "wants --tiers"},
		{[]string{"fleet", "up", "--width"}, "--width wants a value"},
		{[]string{"fleet", "up", "m1", "--width"}, "--width wants a value"},
		{[]string{"resume"}, "wants --stream"},
		{[]string{"resume", "--stream"}, "--stream wants a value"},
		{[]string{"route", "enable"}, "unknown verb"},
	} {
		code, errs := runPrefixed(t, tc.args)
		require.Equal(t, 2, code, "%v: %s", tc.args, errs)
		require.Contains(t, errs, tc.want, "%v: %s", tc.args, errs)
		require.NotContains(t, errs, "NOVA_SPRINT_PREFIX is set", "%v reached the store: %s", tc.args, errs)
	}
}

func runPrefixed(t *testing.T, args []string) (int, string) {
	t.Helper()
	a := newApp(func(k string) string {
		if k == "NOVA_SPRINT_PREFIX" {
			return "probe"
		}
		return ""
	})
	t.Cleanup(a.close)
	var out, errb bytes.Buffer
	code := a.run(args, &out, &errb)
	return code, errb.String()
}
