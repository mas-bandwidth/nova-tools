package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
