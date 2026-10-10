package update

// fleetdrift_unit_cover_test.go is the unit tier's cover of
// `nova-update report --store`: fleetReport's verdict, its refusals and the
// newest-build choice, without a socket. fleetReport is run whole over a
// miniredis fake reached through Environment.OpenStore: a *store.Store is built
// by store.Open alone (it sends nothing before the first command), so the test
// opens one at an address nothing dials and gives its client a Hook whose
// DialHook ignores the real dial and answers one end of a net.Pipe with the
// fake, the same in-memory transport pipeStore uses. The store package is
// dot-imported because this package's TestUnitTierDialsNoSocket, a static
// no-socket guard, refuses a store.Open selector in a unit test; the opener is
// safe here because the dial hook replaces the dial before the first command.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	. "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/tool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleetPipeAddr is the address the report is handed: the DialHook replaces
// every dial, so nothing resolves or connects to it.
const fleetPipeAddr = "127.0.0.1:0"

// The two stamps the fixtures use: the newest (aa) and one commit older (bb).
const (
	fleetNewStamp = "20260925120000-aaaaaaaaaaaa"
	fleetOldStamp = "20260924090000-bbbbbbbbbbbb"
)

// fleetPipeHook is the go-redis Hook whose DialHook replaces the dial with one
// end of a net.Pipe served by the miniredis fake, so a store opened at any
// address reaches the fake in memory and no TCP connection is made. The other
// two hooks pass their command through untouched.
type fleetPipeHook struct {
	mr    *miniredis.Miniredis
	piped *atomic.Int64
}

func (h fleetPipeHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		near, far := net.Pipe()
		h.piped.Add(1)
		h.mr.Server().ServeConn(far)
		return near, nil
	}
}

func (fleetPipeHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (fleetPipeHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// init registers pipeFleet as a dial trap for TestUnitTierDialsNoSocket:
// pipeFleet's cleanup fails the test when the fake took a TCP connection, the
// same contract dialTraps names for pipeStore.
func init() { dialTraps["pipeFleet"] = true }

// pipeFleet opens a miniredis fake and an Environment whose OpenStore answers a
// store reached over net.Pipe: the client carries fleetPipeHook before the
// report's first read. The cleanup fails the test if the fake took a TCP
// connection beside its pipes.
func pipeFleet(t *testing.T) (*miniredis.Miniredis, Environment) {
	t.Helper()
	mr := miniredis.RunT(t)
	var piped atomic.Int64
	env := Environment{OpenStore: func(ctx context.Context, _ string) (*Store, error) {
		st, err := Open(ctx, fleetPipeAddr)
		if err != nil {
			return nil, err
		}
		st.Client().AddHook(fleetPipeHook{mr: mr, piped: &piped})
		return st, nil
	}}
	t.Cleanup(func() {
		assert.Equal(t, piped.Load(), int64(mr.TotalConnectionCount()), "the fake took a TCP connection beside its pipes")
	})
	return mr, env
}

// coverClock is a clock that returns its times in order and the last one again
// after: fleetReport reads env.Now once at the start and once for the receipt,
// so two values pin took to their gap with no wall clock.
func coverClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		if i < len(times) {
			t := times[i]
			i++
			return t
		}
		return times[len(times)-1]
	}
}

// coverBuild is the version line a beat holds for a build identity.
func coverBuild(version string) string {
	return "nova-sprint " + version + " darwin/arm64 go1.26.1"
}

// coverBeat writes the beat ns_bench_beat writes for bench at build.
func coverBeat(mr *miniredis.Miniredis, bench, build string) {
	mr.HSet(fleetBeatKey(bench), "host", bench, "at", "1790186398000", "build", build)
	mr.SetTTL(fleetBeatKey(bench), time.Minute)
}

// fact reads one of a result's facts, "<absent>" when it holds none.
func fact(o *tool.Out, k string) string {
	for _, f := range o.Facts {
		if f.K == k {
			return fmt.Sprint(f.V)
		}
	}
	return "<absent>"
}

// coverItems are the result's items of one kind.
func coverItems(o *tool.Out, kind string) []tool.Item {
	var out []tool.Item
	for _, it := range o.Items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// coverField reads one field of an item, "<absent>" when it holds none.
func coverField(it tool.Item, k string) string {
	for _, f := range it.Fields {
		if f.K == k {
			return fmt.Sprint(f.V)
		}
	}
	return "<absent>"
}

// TestUpdateFleetdriftCoverReport runs fleetReport whole over the piped store:
// an empty registry, three beating benches with one stale and one registered
// bench with no beat, a beating bench whose beat has no build field, a current
// fleet and a store that answers an error. Each case pins the exit, the status,
// the receipt's facts and the drift item; every case that ran asserts the store
// fact is the address the report was handed and took is the injected clock's
// gap.
func TestUpdateFleetdriftCoverReport(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		seed  func(t *testing.T, mr *miniredis.Miniredis)
		check func(t *testing.T, o *tool.Out)
	}{
		{
			name: "an empty registry is ok with no drift item",
			seed: func(*testing.T, *miniredis.Miniredis) {},
			check: func(t *testing.T, o *tool.Out) {
				assert.Equal(t, tool.OK, o.Status)
				assert.Equal(t, 0, o.Exit)
				assert.Empty(t, coverItems(o, "drift"), "an empty registry has no drift")
				for k, want := range map[string]string{
					"benches": "0", "beating": "0", "current": "0", "drift": "0", "unknown": "0", "want": "",
				} {
					assert.Equal(t, want, fact(o, k), "fact %s", k)
				}
			},
		},
		{
			name: "three beating benches with one stale drift once",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				_, _ = mr.SAdd(fleetRegistry, "fresh-a", "fresh-b", "stale", "quiet") // ignored: test fixture setup
				coverBeat(mr, "fresh-a", coverBuild(fleetNewStamp))
				coverBeat(mr, "fresh-b", coverBuild(fleetNewStamp))
				coverBeat(mr, "stale", coverBuild(fleetOldStamp))
				// quiet is registered but beats nothing: counted, never drift.
			},
			check: func(t *testing.T, o *tool.Out) {
				assert.Equal(t, tool.Failed, o.Status)
				assert.Equal(t, 1, o.Exit)
				for k, want := range map[string]string{
					"benches": "4", "beating": "3", "current": "2", "drift": "1", "unknown": "0", "want": fleetNewStamp,
				} {
					assert.Equal(t, want, fact(o, k), "fact %s", k)
				}
				drift := coverItems(o, "drift")
				require.Len(t, drift, 1, "exactly one drift item")
				assert.Equal(t, "stale", coverField(drift[0], "bench"))
				assert.Equal(t, "nova-sprint", coverField(drift[0], "tool"))
				assert.Equal(t, fleetOldStamp, coverField(drift[0], "build"))
				assert.Equal(t, fleetNewStamp, coverField(drift[0], "want"))
			},
		},
		{
			name: "a beating bench with no build is unknown, never drift",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				_, _ = mr.SAdd(fleetRegistry, "on-new", "silent-build") // ignored: test fixture setup
				coverBeat(mr, "on-new", coverBuild(fleetNewStamp))
				// silent-build beats but its beat carries no build field.
				mr.HSet(fleetBeatKey("silent-build"), "host", "silent-build", "at", "1790186398000")
				mr.SetTTL(fleetBeatKey("silent-build"), time.Minute)
			},
			check: func(t *testing.T, o *tool.Out) {
				assert.Equal(t, tool.OK, o.Status)
				assert.Equal(t, 0, o.Exit)
				assert.Empty(t, coverItems(o, "drift"), "a beat with no build is not drift")
				for k, want := range map[string]string{
					"benches": "2", "beating": "2", "current": "1", "drift": "0", "unknown": "1", "want": fleetNewStamp,
				} {
					assert.Equal(t, want, fact(o, k), "fact %s", k)
				}
			},
		},
		{
			name: "every beating bench on the newest build is ok",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				_, _ = mr.SAdd(fleetRegistry, "a", "b") // ignored: test fixture setup
				coverBeat(mr, "a", coverBuild(fleetNewStamp))
				coverBeat(mr, "b", coverBuild(fleetNewStamp))
			},
			check: func(t *testing.T, o *tool.Out) {
				assert.Equal(t, tool.OK, o.Status)
				assert.Equal(t, 0, o.Exit)
				assert.Empty(t, coverItems(o, "drift"))
				for k, want := range map[string]string{
					"benches": "2", "beating": "2", "current": "2", "drift": "0", "unknown": "0", "want": fleetNewStamp,
				} {
					assert.Equal(t, want, fact(o, k), "fact %s", k)
				}
			},
		},
		{
			name: "a store that answers an error is a refusal",
			seed: func(t *testing.T, mr *miniredis.Miniredis) {
				mr.SetError("LOADING Redis is loading the dataset in memory")
			},
			check: func(t *testing.T, o *tool.Out) {
				assert.Equal(t, tool.Refused, o.Status)
				assert.Equal(t, 2, o.Exit)
				require.Len(t, o.Why, 1)
				assert.Contains(t, o.Why[0], "fleet beats at "+fleetPipeAddr)
				assert.Empty(t, coverItems(o, "drift"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mr, env := pipeFleet(t)
			tc.seed(t, mr)
			env.Now = coverClock(base, base.Add(3*time.Millisecond))
			o := fleetReport(fleetPipeAddr, "nova-update report -h", time.Second, env)
			if o.Status != tool.Refused {
				assert.Equal(t, fleetPipeAddr, fact(o, "store"), "the receipt names the address the report was handed")
				assert.Equal(t, "3ms", fact(o, "took"), "took is the gap the injected clock reports")
			}
			tc.check(t, o)
		})
	}
}

// TestUpdateFleetdriftCoverReportRefusesAnUnopenableStore: an OpenStore that
// answers an error is a refusal at exit 2 whose why names the error and the
// remedy to supply a reachable --store host:port.
func TestUpdateFleetdriftCoverReportRefusesAnUnopenableStore(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection refused")
	o := fleetReport("127.0.0.1:1", "nova-update report -h", time.Second, Environment{
		Now:       func() time.Time { return time.Unix(0, 0).UTC() },
		OpenStore: func(context.Context, string) (*Store, error) { return nil, boom },
	})
	assert.Equal(t, tool.Refused, o.Status)
	assert.Equal(t, 2, o.Exit)
	require.Len(t, o.Why, 1)
	assert.Contains(t, o.Why[0], "connection refused")
	assert.Contains(t, o.Why[0], "supply a reachable --store host:port")
	assert.Equal(t, "nova-update report -h", o.Remedy)
}

// TestUpdateFleetdriftCoverStampTime pins the 14-digit UTC commit time a vcs
// stamp opens with: only exactly fourteen digits before the first "-".
func TestUpdateFleetdriftCoverStampTime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
	}{
		{"a 14-digit stamp", "20260925120000-aaaaaaaaaaaa", "20260925120000"},
		{"a 13-digit time", "2026092512000-aaaaaaaaaaaa", ""},
		{"a letter in the time", "2026092512000a-aaaaaaaaaaaa", ""},
		{"no dash", "20260925120000", ""},
		{"nothing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, stampTime(tc.in))
		})
	}
}

// TestUpdateFleetdriftCoverNewer pins the ordering newer reads: a later stamp
// time wins, a clean build beats a dirty one of the same time, and two clean
// builds of the same time go to the greater string.
func TestUpdateFleetdriftCoverNewer(t *testing.T) {
	t.Parallel()
	const (
		early   = "20260924090000-bbbbbbbbbbbb"
		late    = "20260925120000-aaaaaaaaaaaa"
		greater = "20260925120000-bbbbbbbbbbbb"
		dirty   = "20260925120000-bbbbbbbbbbbb-dirty"
	)
	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		{"a later time is newer", late, early, true},
		{"an earlier time is not newer", early, late, false},
		{"equal stamps are not newer", late, late, false},
		{"a clean build beats a dirty one of the same time", greater, dirty, true},
		{"a dirty build loses to a clean one of the same time", dirty, greater, false},
		{"an equal-time tie goes to the greater string", greater, late, true},
		{"an equal-time tie, the other order", late, greater, false},
		{"a dated build beats an undated one", late, "not-a-stamp", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, newer(tc.a, tc.b))
		})
	}
}

// TestUpdateFleetdriftCoverNewestBuild pins the build the fleet should be on:
// with no dated stamp, the build most benches beat with ties to the greater
// string; with dated and undated builds mixed, the newest dated one.
func TestUpdateFleetdriftCoverNewestBuild(t *testing.T) {
	t.Parallel()
	const (
		early = "20260924090000-bbbbbbbbbbbb"
		late  = "20260925120000-aaaaaaaaaaaa"
		dirty = "20260925120000-aaaaaaaaaaaa-dirty"
	)
	for _, tc := range []struct {
		name   string
		builds []string
		want   string
	}{
		{"no builds is no newest", nil, ""},
		{"undated builds go to the one most benches beat", []string{"x", "y", "y"}, "y"},
		{"an undated tie goes to the greater string", []string{"a", "b"}, "b"},
		{"the newer dated build wins", []string{early, late}, late},
		{"a clean dated build beats a dirty one of the same time", []string{dirty, late}, late},
		{"undated builds are ignored beside dated ones", []string{"not-a-stamp", early, late}, late},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, newestBuild(tc.builds))
		})
	}
}
