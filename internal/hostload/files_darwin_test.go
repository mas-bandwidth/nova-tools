//go:build darwin

package hostload

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseLsof: lsof's field output (-F pcLf) is a process line set, p c L, then an f
// line per open file; only the numbered descriptors count (cwd, txt and mem are none).
func TestParseLsof(t *testing.T) {
	t.Parallel()
	out := "p45721\nczsh\nLops\nfcwd\nftxt\nf0\nf1\nf2\nf10\np200\ncredis-server\nLbuild\nf0\nf1\nfmem\np9\ncidle\nLroot\nftxt\n"
	require.Equal(t, []Holder{
		{PID: 45721, Command: "zsh", User: "ops", Open: 4},
		{PID: 200, Command: "redis-server", User: "build", Open: 2},
	}, ParseLsof(out), "a process with no numbered descriptor is no holder")
	require.Empty(t, ParseLsof(""))
	require.Empty(t, ParseLsof("pnot-a-pid\nf1\n"), "a line set with no pid counts nothing")
}
