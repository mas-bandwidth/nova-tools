package update

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipeStore is a miniredis fake reached in memory: the client's dialer hands
// it one end of a net.Pipe and the fake serves the other (ServeConn), so no
// TCP connection is made. miniredis binds a loopback listener when it starts;
// the cleanup fails the test when anything dialled it.
func pipeStore(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	var piped atomic.Int64
	client := redis.NewClient(&redis.Options{
		Addr:            "127.0.0.1:0", // never dialled (Dialer is the transport); an IP so nothing resolves it
		DisableIdentity: true,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			near, far := net.Pipe()
			piped.Add(1)
			mr.Server().ServeConn(far)
			return near, nil
		},
	})
	t.Cleanup(func() {
		_ = client.Close()
		assert.Equal(t, piped.Load(), int64(mr.TotalConnectionCount()), "the fake took a TCP connection beside its pipes")
	})
	return mr, client
}

// TestFleetBuildsReadOneDriftWithoutASocket is the read and the verdict of the
// functional TestReportStorePrintsOneDriftLineForTheStaleBench, over
// pipeStore: readFleetBuilds reads the benches registry and every registered
// bench's beat build in two round trips, a registered bench with no live beat
// is not beating, and the newest build among the beating benches names the
// stale one; once it beats the newest build too, nothing drifts.
func TestFleetBuildsReadOneDriftWithoutASocket(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mr, client := pipeStore(t)
	trips := redisconn.CountTrips(client)
	_, _ = mr.SAdd(fleetRegistry, "fresh", "stale", "quiet") // ignored: test fixture setup
	beat := func(bench, build string) {
		mr.HSet(fleetBeatKey(bench), "host", bench, "at", "1790186398000", "build", build)
		mr.SetTTL(fleetBeatKey(bench), 3*time.Second)
	}
	beat("fresh", "nova-sprint 20260925120000-aaaaaaaaaaaa darwin/arm64 go1.26.1")
	beat("stale", "nova-sprint 20260924090000-bbbbbbbbbbbb darwin/arm64 go1.26.1")

	fleet, err := readFleetBuilds(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, []benchBuild{
		{Bench: "fresh", Beating: true, Build: "20260925120000-aaaaaaaaaaaa"},
		{Bench: "quiet"},
		{Bench: "stale", Beating: true, Build: "20260924090000-bbbbbbbbbbbb"},
	}, fleet)
	assert.Equal(t, int64(2), trips.N(), "the registry, then every beat in one pipeline")
	assert.Equal(t, "20260925120000-aaaaaaaaaaaa", newestBuild([]string{fleet[0].Build, fleet[2].Build}), "the stale bench is the one off the newest build")

	beat("stale", "nova-sprint 20260925120000-aaaaaaaaaaaa linux/amd64 go1.26.1")
	fleet, err = readFleetBuilds(ctx, client)
	require.NoError(t, err)
	assert.Equal(t, fleet[0].Build, fleet[2].Build, "the stale bench caught up")
}

// --store is the fleet read: it takes no manifest, no snapshot and no note, so
// a combination that would write a bus note is refused with exit 2.
func TestReportStoreRefusesANote(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"report", "--store", "127.0.0.1:1", "--send", "--as", "a", "--to", "b"},
		{"report", "--store", "127.0.0.1:1", "--draft", "--as", "a", "--to", "b"},
		{"report", "--store", "127.0.0.1:1", "--file", "m.tsv"},
	} {
		var out, errs bytes.Buffer
		if code := Run("nova-update", args, "test", &out, &errs, Environment{}); code != 2 || !strings.Contains(errs.String(), "REPORT REFUSED") {
			require.Failf(t, "", "%v: exit %d, want 2 with a refusal\n%s%s", args, code, out.String(), errs.String())
		}
	}
}
