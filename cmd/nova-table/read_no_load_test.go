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
// them may trigger a load.
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
			require.EqualValues(t, 0, s.loads, "%s loaded the library (%d loads) on a store that holds none; a read never loads", name, s.loads)
			require.EqualValues(t, 1, s.sends, "%s sent the read command %d times; want one send and no load", name, s.sends)
			require.Equal(t, []string{"fcall_ro"}, s.commands, "%s issued commands to the store; a read sends its FCALL_RO and nothing else", name)
		})
	}
}
