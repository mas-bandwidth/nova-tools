package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The land loop beats (Glenn, 2026-10-05: nine streams held queued cards and no LAND line
// was printed for 40 minutes while the lander's `go build ./...` ran on a loaded machine,
// and nothing in the log said the lander was stuck or on what). Every cycle writes a line:
// the round's LAND lines when a round ended in it, else one LAND IDLE line with the cards
// queued, the step the landing is in, how long it has been there and the process it waits
// on. A landing that has run past the land deadline with cards queued raises one judgment
// naming the step and the process, and only one however many cycles follow. A landed
// batch's line carries the tree gate's wall time and the machine it ran on.
func TestTheLandLoopBeatsAndRaisesAStuckLanding(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	stop := filepath.Join(r.dir, "stop")
	check := "until [ -e " + stop + " ]; do sleep 0.05; done"
	more := []string{"--repo-dir", r.clone, "--base", "main", "--check", check}
	b := &landBeat{}
	ctx := withLandBeat(context.Background(), b)
	landed := regexp.MustCompile(`(?m)^\S+ LAND (OK|REFUSED|FAILED) `)
	idle := regexp.MustCompile(`^\S+ LAND IDLE queued=(\d+) step=(\S+) since=\S+`)
	// cycle runs one cycle and holds it to its one line: a round's LAND lines, or one IDLE
	cycle := func() string {
		t.Helper()
		var out bytes.Buffer
		r.a.landCycle(ctx, "mem:0", more, b, 100*time.Millisecond, &out)
		said := out.String()
		if !landed.MatchString(said) {
			require.Equal(t, 1, strings.Count(said, "\n"), "a cycle with no landing ended says one line: %q", said)
			require.Regexp(t, idle, said)
		}
		return said
	}
	until := func(what string, ok func(string) bool) string {
		t.Helper()
		for range 1200 {
			if said := cycle(); ok(said) {
				return said
			}
		}
		require.FailNow(t, "the loop never said "+what)
		return ""
	}

	said := cycle()
	assert.Equal(t, "0", idle.FindStringSubmatch(said)[1], "nothing queued: %s", said)

	r.ok("add --stream s1 --count 2")
	heads := map[string]string{
		"s1-1": r.card("s1-1", map[string]string{"a.go": "package main\n\nfunc a() {}\n"}),
		"s1-2": r.card("s1-2", map[string]string{"b.go": "package main\n\nfunc b() {}\n"}),
	}
	r.queued(heads, "s1-1", "s1-2")

	// the landing waits on its check: the beat names the step and the process
	said = until("the check step", func(s string) bool { return strings.Contains(s, "step=check") })
	assert.Regexp(t, `LAND IDLE queued=2 step=check since=\S+ proc=`, said)
	assert.Contains(t, said, "sh -c "+check)
	assert.NotContains(t, r.ok("inbox"), sprint.NOpStuck, "within the deadline nothing is raised")

	// past the land deadline: one judgment, naming the step and the process
	r.after(LandDeadline + time.Minute)
	said = cycle()
	assert.Contains(t, said, "judgment=raised", said)
	for range 3 {
		cycle()
	}
	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NOpStuck), "one judgment however many cycles: %s", inbox)
	assert.Contains(t, inbox, "step check")
	assert.Contains(t, inbox, "sh -c "+check)
	assert.Contains(t, inbox, "2 cards queued")

	// the check ends: the batch lands, its line carries the gate's wall time and machine
	require.NoError(t, os.WriteFile(stop, nil, 0o600))
	said = until("the landing", landed.MatchString)
	host, err := os.Hostname()
	require.NoError(t, err)
	assert.Regexp(t, `LAND OK stream=s1 cards=2 .* gate=\d+\.\ds gate_on=`+regexp.QuoteMeta(host)+`\s`, said)
	assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))

	said = until("idle again", func(s string) bool { return idle.MatchString(s) && idle.FindStringSubmatch(s)[1] == "0" })
	assert.NotContains(t, said, "judgment=")
	r.clean()
}
