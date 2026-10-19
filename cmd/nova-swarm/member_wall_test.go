package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// The wall tests' clock starts at 1000 (a fixed time, never the host's), so a run's
// tick is reproducible whatever the wall clock says.
var wallClock = time.Unix(1000, 0)

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
	code := run(args, strings.NewReader(""), &out, &errb, wallClock)
	require.Equal(t, 2, code, "exit %d, stderr %q", code, errb.String())
	require.Contains(t, errb.String(), "no wall")
	require.Contains(t, errb.String(), "missing")
	require.Contains(t, errb.String(), "--no-wall")
	assert.NotContains(t, out.String(), "MEMBER ", "a refused member never starts")
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

// A native child whose log carries native's real no-wall refusal (its wall step refused
// before any child ran) ends as a staging refusal, never the card's failed work: the
// member finishes it staging refused with the native refusal line, and the sprint deals the
// card to another member (docs/SPEC-SWARM.md, `member`; tla/CardContract.tla, StageRefused).
func TestNativeChildNoWallRefusalEndsStaging(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	log := "NATIVE REFUSED: c1 no wall: " + swarm.SandboxBinary + " is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes; run: nova-swarm native -h\n"
	write(t, logPath, log)
	ch := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}
	res := ch.Result()
	require.Equal(t, member.EndStaging, res.End, "a no-wall launch refusal is a staging refusal")
	assert.Contains(t, res.Staging, "no wall:", "the staging reason is the native refusal line, got %q", res.Staging)
	fin, why := member.Judge(res, member.Push{None: "the child committed nothing"})
	assert.Equal(t, member.FinishFailed, fin)
	assert.True(t, strings.Contains(why, member.EndStaging+": "), "the finish's reason %q", why)
	assert.Contains(t, why, "no wall:", "the finish's reason carries the native line: %q", why)
}
