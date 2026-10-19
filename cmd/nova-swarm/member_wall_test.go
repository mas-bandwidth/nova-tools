package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// The member tests must not read the host PATH: certification runs cmd/nova-swarm on
// machines with no nova-sandbox, so every cmdMember that does not name a wall or --no-wall
// would otherwise be refused here. memberLookPath is a package var (production keeps
// exec.LookPath); this answers it with the running test binary, which exists. The lookup
// itself is pinned through memberWallRefusal with an explicit fake below.
func init() {
	memberLookPath = func(string) (string, error) { return os.Executable() }
}

// A member whose PATH has no nova-sandbox and no --sandbox/--no-wall refuses to start
// before its first take: exit 2 and one line naming the remedy, so no card is ever dealt to
// a member that cannot wall it (docs/SPEC-SWARM.md, `member`).
func TestMemberWithNoWallRefusesToStart(t *testing.T) {
	t.Parallel()
	want := "no wall: " + swarm.SandboxBinary + " is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes"
	require.Equal(t, want, memberWallRefusal(false, "", noPath), "a PATH without nova-sandbox names the remedy")

	root := t.TempDir()
	args := memberFull(root)
	args = append(args, "--sandbox", filepath.Join(root, "no-such-sandbox"))
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb, time.Unix(1000, 0))
	require.Equal(t, 2, code, "exit %d, stderr %q", code, errb.String())
	require.Contains(t, errb.String(), "no wall")
	require.Contains(t, errb.String(), "missing")
	require.Contains(t, errb.String(), "--no-wall")
	assert.NotContains(t, out.String(), "MEMBER ", "a refused member never starts")
}

// A native launch whose wall did not resolve is refused before any child ran: the member
// ends it as a staging refusal carrying the native no-wall line, never the card's failed
// work (docs/SPEC-SWARM.md, `member`).
func TestANativeLaunchWithoutAWallEndsStagingRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	log := "NATIVE REFUSED: c1 no wall: " + swarm.SandboxBinary + " is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes\n"
	require.NoError(t, os.WriteFile(logPath, []byte(log), 0o644))
	ch := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}
	res := ch.Result()
	assert.Equal(t, member.EndStaging, res.End, "a no-wall launch ends staging refused")
	assert.Contains(t, res.Staging, "no wall:", "the staging reason is the native no-wall line")
	assert.Contains(t, res.Report, "no wall:", "the report names the native refusal line")
}

// --no-wall skips the wall check: a member that names it starts even where nova-sandbox is
// on no PATH entry, owning every read and write its children make (docs/SPEC-SWARM.md, `member`).
func TestMemberNoWallFlagStillStarts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	args := append(memberWithoutOnce(root), "--ticks", "2", "--every", "1ms", "--no-wall")
	var out, errb bytes.Buffer
	require.Equal(t, 0, cmdMember(args[1:], &out, &errb, noServer), "stderr %q", errb.String())
	require.Contains(t, out.String(), "MEMBER OK as=m1 ticks=2 running=0")
}
