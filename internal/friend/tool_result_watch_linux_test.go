package friend

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestToolResultInotifyOverflowRescansWithoutSelfWake(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		mask    uint32
		file    string
		changed bool
		bad     bool
	}{
		{"overflow", unix.IN_Q_OVERFLOW, "", true, false},
		{"published", unix.IN_MOVED_TO, "proof.event.json", true, false},
		{"pending", unix.IN_MOVED_TO, "proof.pending.json", true, false},
		{"lease", unix.IN_MOVED_TO, "binding.json", true, false},
		{"lock close", unix.IN_CLOSE_WRITE, ".tool-result.lock", false, false},
		{"temporary close", unix.IN_CLOSE_WRITE, ".atomicfile-123", false, false},
		{"directory gone", unix.IN_DELETE_SELF, "", false, true},
		{"watch lost", unix.IN_IGNORED, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := make([]byte, unix.SizeofInotifyEvent+len(tc.file)+1)
			binary.NativeEndian.PutUint32(raw[4:8], tc.mask)
			binary.NativeEndian.PutUint32(raw[12:16], uint32(len(tc.file)+1))
			copy(raw[unix.SizeofInotifyEvent:], tc.file)
			changed, err := toolResultInotifyChanges(raw)
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.changed, changed)
		})
	}
	_, err := toolResultInotifyChanges([]byte{1})
	require.Error(t, err)
}
