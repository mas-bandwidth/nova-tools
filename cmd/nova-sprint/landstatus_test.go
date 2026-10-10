package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A live land pass prints one LANDING line per phase and records the phase it
// is in beside the repository's cache clone: `land --status` names the check
// phase while a fake check blocks the pass, and says no pass is running once
// the status file is removed at the pass's end.
func TestLandPrintsEachPhaseAndStatusShowsIt(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2")

	// the check blocks until the test has read what the live pass recorded for
	// the check phase: a real land pass, never printPhase called by hand
	stop := filepath.Join(t.TempDir(), "stop")
	check := "while [ ! -e " + stop + " ]; do sleep 0.02; done"
	type result struct {
		code int
		out  string
	}
	done := make(chan result, 1)
	go func() {
		var out, errb bytes.Buffer
		code := r.a.run(split("land --repo-dir "+r.clone+" --base main --check '"+check+"'"), &out, &errb)
		done <- result{code, out.String()}
	}()

	// while the check blocks, `land --status` names the check phase
	require.Eventually(t, func() bool {
		var buf bytes.Buffer
		r.a.run(split("land --status"), &buf, io.Discard)
		return strings.Contains(buf.String(), "phase=check")
	}, 10*time.Second, 5*time.Millisecond, "land --status names the blocked check phase")
	var during bytes.Buffer
	require.Equal(t, 0, r.a.run(split("land --status"), &during, io.Discard), during.String())
	assert.Contains(t, during.String(), "LANDING PASS stream=s1 phase=check")
	require.NoError(t, os.WriteFile(stop, nil, 0o600))

	got := <-done
	require.Equal(t, 0, got.code, "land over the temporary origin: %s", got.out)
	for _, phase := range []string{"fetch", "merge", "check", "queue", "push", "report"} {
		assert.Contains(t, got.out, "LANDING stream=s1 phase="+phase+" cards=2 at=", "the %s phase", phase)
	}

	// the pass removed its status file: no pass is running now
	var after bytes.Buffer
	require.Equal(t, 0, r.a.run(split("land --status"), &after, io.Discard), after.String())
	assert.Contains(t, after.String(), "no land pass running")
	r.clean()
}
