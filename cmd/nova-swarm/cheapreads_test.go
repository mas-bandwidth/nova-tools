package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// The stage wall scales with the machine: a member's --stage-wall (default 120s, the loop
// row's argv in nova-config) is every launch's native --stage-timeout (a 36-thread bench,
// 2026-10-02: a 2.3 GHz bench under load staged in 73-108 s median against a 120 s wall).
func TestAMembersStageWallIsEveryLaunchsStageTimeout(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	args, _ := launched(t, r, member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	assert.Equal(t, "2m0s", args["--stage-timeout"], "the default wall is native's own 120s, said")
	r = argsRunner(t, "override/model", "999", 9*time.Second)
	r.stageWall = 5 * time.Minute
	args, _ = launched(t, r, member.Packet{Card: "c2", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c2"})
	assert.Equal(t, "5m0s", args["--stage-timeout"])
}

// --stage-wall is a positive duration or whole seconds; anything else is refused before
// the member makes anything.
func TestMemberRefusesAStageWallThatIsNoBound(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"0", "-5s", "soon"} {
		root := t.TempDir()
		var out, errb bytes.Buffer
		code := run(append(memberFull(root), "--stage-wall", v), strings.NewReader(""), &out, &errb, time.Now())
		assert.Equal(t, 2, code, "--stage-wall %s: stderr %q", v, errb.String())
		assert.Contains(t, errb.String(), "--stage-wall", v)
		_, err := os.Stat(filepath.Join(root, "slots"))
		assert.True(t, os.IsNotExist(err), "--stage-wall %s: a directory was made before the refusal", v)
	}
}
