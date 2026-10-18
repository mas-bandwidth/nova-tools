package ci

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 2026-10-17: TestWallNetDenyRefusesATCPConnect connected to 127.0.0.1 through
// bash's /dev/tcp and the unit-sockets rule did not see it, so it ran in the unit
// tier and timed out on loaded benches. A networked program run by exec, and
// every net dial and listen shape, is a socket.
func TestUnitSocketsSeesANetworkedExecAndEveryDialShape(t *testing.T) {
	t.Parallel()
	const src = `package p

import (
	"context"
	"net"
	"os/exec"
	"testing"
)

func TestShells(t *testing.T) {
	_ = exec.Command("bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/45678")
	_ = exec.CommandContext(context.Background(), "curl", "-s", "http://127.0.0.1:1")
	_ = exec.Command("nc", "-z", "127.0.0.1", "1")
	_ = exec.Command("redis-cli", "-p", "6390", "ping")
	_ = exec.Command("git", "status")
	_ = exec.Command("bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/"+port)
	_, _ = net.DialTCP("tcp", nil, nil)
	_, _ = net.DialUDP("udp", nil, nil)
	_, _ = net.DialUnix("unix", nil, nil)
	_, _ = net.ListenUDP("udp", nil)
}
`
	const rel = "cmd/p/p_test.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	require.NoError(t, err)
	var got []string
	for _, s := range unitSocketsIn(fset, f) {
		got = append(got, fmt.Sprintf("%s:%d %s", rel, s.Line, s.What))
	}
	want := []string{
		rel + ":11 exec.Command bash /dev/tcp",
		rel + ":12 exec.CommandContext curl",
		rel + ":13 exec.Command nc",
		rel + ":14 exec.Command redis-cli",
		rel + ":16 exec.Command bash /dev/tcp",
		rel + ":17 net.DialTCP",
		rel + ":18 net.DialUDP",
		rel + ":19 net.DialUnix",
		rel + ":20 net.ListenUDP",
	}
	assert.Equal(t, want, got, "git status is no socket; every other line is:\n%s", strings.Join(got, "\n"))
}

// The real scan parses under an absolute path; the shared net shapes must still
// be read there once the caller says the file is in the unit tier (and not when
// it does not).
func TestUnitSocketsReadsTheSharedNetShapesForATierFileUnderAnAbsolutePath(t *testing.T) {
	t.Parallel()
	const src = `package p

import "net"

func TestDial() { _, _ = net.DialTCP("tcp", nil, nil) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "/work/repo/internal/p/p_test.go", src, 0)
	require.NoError(t, err)
	assert.Empty(t, unitSocketsIn(fset, f), "an absolute name is not judged to be in the tier")
	assert.Empty(t, unitSocketsInTier(fset, f, false))
	got := unitSocketsInTier(fset, f, true)
	require.Len(t, got, 1)
	assert.Equal(t, "net.DialTCP", got[0].What)
}
