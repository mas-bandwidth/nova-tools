package main

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// versionLine is the one line the version verb prints: the tool's own build
// identity, its platform and the toolchain. The test reads it back through
// buildinfo.Parse, the one reader of the shape, so a field that drifted is a
// red line here.
func TestVerbsCoverVersionLineNamesTheToolItsPlatformAndItsBuild(t *testing.T) {
	t.Parallel()
	line := versionLine()
	f, ok := buildinfo.Parse(line)
	require.True(t, ok, "versionLine is not a version line this tree reads: %q", line)
	assert.Equal(t, prog, f.Tool, "the first field names the tool itself: %q", line)
	assert.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, f.Platform, "the third field is the platform: %q", line)
	assert.NotEmpty(t, f.Version, "the build identity is never empty: %q", line)
	assert.NotEmpty(t, f.GoVersion, "the toolchain is named: %q", line)
}

// tookSince is the " in <duration>" the sprint line carries once every card
// has landed: the wall time from the machine's first start of the epoch to
// the clock's reading. The clock is the app's own fake, stepped by hand, so
// the test never waits.
func TestVerbsCoverTookSinceCountsFromTheFirstStart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	ta.a.sleep(time.Hour)
	assert.Equal(t, " in 1h0m0s", tookSince(context.Background(), st))
}

// tookSince says nothing when the machine has not started in the epoch: with
// no first start there is no wall time to report, and the sprint line carries
// no " in <duration>". This is the function's negative branch.
func TestVerbsCoverTookSinceIsEmptyBeforeTheFirstStart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	assert.Empty(t, tookSince(context.Background(), st), "not started: no duration")
}
