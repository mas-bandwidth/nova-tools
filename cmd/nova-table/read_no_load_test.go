package main

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// fcallRO builds a read-only FCALL as show, list and render send them
// (ntable reads through FCallRO).
func fcallRO(name string) redis.Cmder {
	return redis.NewCmd(context.Background(), "fcall_ro", name, 1, "table:demo")
}

// TestReadVerbsNeverLoadTheLuaLibrary: a read verb on a store that holds no
// nova_sprint library is refused with the deployer's remedy and sends no
// FUNCTION LOAD; only a write verb loads it (SPEC-NOVA-TABLE, read-only
// execution). show, list and render all read through FCALL_RO, so none of
// them may trigger a load; watch reads every table through a pipeline of
// FCALL_ROs (tableSnapshots), so that pipeline is refused too, with neither a
// load nor a resend.
func TestReadVerbsNeverLoadTheLuaLibrary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	verbs := map[string]redis.Cmder{
		"show":   fcallRO("ns_table_read"),
		"list":   fcallRO("ns_table_list"),
		"render": fcallRO("ns_table_read"),
	}
	for name, cmd := range verbs {
		name, cmd := name, cmd
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &fakeStore{}
			hook := libraryHook{state: &firstContact{}, load: s.load}
			err := hook.ProcessHook(s.next)(ctx, cmd)
			require.Error(t, err, "%s on a store with no library: %v; want a refusal", name, err)
			require.Contains(t, err.Error(), deployRemedy, "%s: the refusal lacks the deployer's remedy: %v", name, err)
			require.Contains(t, err.Error(), "nova-redis fn load --redis", "%s: the remedy spells the deprecated --addr: %v", name, err)
			require.EqualValues(t, 0, s.loads, "%s loaded the library (%d loads) on a store that holds none; a read never loads", name, s.loads)
			require.EqualValues(t, 1, s.sends, "%s sent the read command %d times; want one send and no load", name, s.sends)
			require.Equal(t, []string{"fcall_ro"}, s.commands, "%s issued commands to the store; a read sends its FCALL_RO and nothing else", name)
		})
	}

	t.Run("watch pipeline", func(t *testing.T) {
		t.Parallel()
		s := &fakeStore{}
		hook := libraryHook{state: &firstContact{}, load: s.load}
		cmds := []redis.Cmder{fcallRO("ns_table_read"), fcallRO("ns_table_read")}
		err := hook.ProcessPipelineHook(s.pipeline)(ctx, cmds)
		require.Error(t, err, "watch's pipeline of reads on a store with no library: %v; want a refusal", err)
		require.Contains(t, err.Error(), deployRemedy, "watch's pipeline: the refusal lacks the deployer's remedy: %v", err)
		require.EqualValues(t, 0, s.loads, "watch's pipeline loaded the library (%d loads) on a store that holds none; a read never loads", s.loads)
		require.EqualValues(t, 2, s.sends, "watch's pipeline sent its reads %d times; want one send each and no load", s.sends)
		require.Equal(t, []string{"fcall_ro", "fcall_ro"}, s.commands, "watch's pipeline issued commands to the store; a read pipeline sends its FCALL_ROs and nothing else")
		for _, cmd := range cmds {
			require.Error(t, cmd.Err(), "%v carries no refusal", cmd.Args())
		}
	})
}
