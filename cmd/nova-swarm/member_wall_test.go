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
	code := run(args, strings.NewReader(""), &out, &errb, time.Now())
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

// A no-wall NATIVE REFUSED before any child started is a staging refusal, the machine's,
// never the card's failed work: the member ends it staging refused with the native refusal
// line (member.go, nativeChild.Result). This pins that branch apart from every other
// pre-child refusal, which stays the card's (native_yield_test.go).
func TestANativeNoWallRefusalEndsStagingRefused(t *testing.T) {
	t.Parallel()
	noWall := "NATIVE REFUSED: c1 no wall: " + swarm.SandboxBinary + " is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes\n"
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	require.NoError(t, os.WriteFile(logPath, []byte(noWall), 0o644))
	ch := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}
	res := ch.Result()
	assert.Equal(t, member.EndStaging, res.End, "a no-wall refusal is the machine's, never the card's")
	require.Contains(t, res.Staging, "no wall:")
	fin, why := member.Judge(res, member.Push{None: "the child committed nothing"})
	assert.Equal(t, member.FinishFailed, fin, "no result is never an ok finish (%s)", why)
	assert.True(t, strings.HasPrefix(why, member.EndStaging+": "+res.Staging), "the finish's reason %q", why)
	assert.Contains(t, res.Report, "native refused:")
	assert.Contains(t, res.Report, "no wall:")
}
