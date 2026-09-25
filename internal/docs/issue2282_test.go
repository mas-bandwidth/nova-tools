package docs

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/presence"
)

// TestIssue2282 pins the presence behaviour of nova-tools #2282 against the
// production heartbeat writer and reader (internal/presence: Beat, Read, Line
// over the live go-redis Store), not against hand-made keys:
//
// The spec (docs/SPEC-REDIS.md:68-69):
//
//	"presence lists the live lines seen by heartbeat keys whose at is inside
//	 the window each beat promises (stale_ms). The keys never expire
//	 (nova-tools #3878, keys do not expire), so a crashed line ages out, read
//	 down and dated, without anyone writing a tombstone."
//
// And (docs/SPEC-REDIS.md:103-104):
//
//	"presence ageing out a heartbeat"
//
// This test verifies:
//  1. The spec file carries the presence and heartbeat contract text.
//  2. Live: every friend whose presence.Beat landed inside the TTL is read Up
//     by presence.Read and listed live on presence.Line.
//  3. Missing: a friend that never beat is read Never, not Up.
//  4. Expired: a friend that stops beating ages out to Away once the window
//     passes, while a friend still beating stays Up.
//  5. No tombstone: ageing out writes nothing. The presence key never
//     expires (#3878): it stays exactly as the last beat wrote it, beside the
//     untimed memory Beat itself wrote, both unchanged since the last beat.
func TestIssue2282(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REDIS.md: %v", err)
	}
	for _, want := range []string{
		"presence` lists the live lines seen by heartbeat keys whose `at` is inside",
		"The keys never expire",
		"without anyone writing a tombstone",
		"presence` ageing out a heartbeat",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("docs/SPEC-REDIS.md missing required text: %q", want)
		}
	}

	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := presence.Open(ctx, mr.Addr(), presence.DefaultUser)
	if err != nil {
		t.Fatalf("presence.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	t0 := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	ttl := presence.DefaultTTL
	live := []string{"johnny", "stella", "emma"}
	roster := append(append([]string{}, live...), "freddy") // freddy never beats

	for _, n := range live {
		if err := presence.Beat(ctx, st, n, t0, ttl); err != nil {
			t.Fatalf("presence.Beat %s: %v", n, err)
		}
	}

	t.Run("PresenceListsLiveLines", func(t *testing.T) {
		now := t0.Add(5 * time.Second)
		sts, err := presence.Read(ctx, st, roster, now)
		if err != nil {
			t.Fatalf("presence.Read: %v", err)
		}
		for i, n := range live {
			if sts[i].State != presence.Up {
				t.Errorf("%s inside the ttl: state %v; want Up", n, sts[i].State)
			}
		}
		if got := sts[3].State; got != presence.Never {
			t.Errorf("freddy never beat: state %v; want Never", got)
		}
		line := presence.Line(sts, now)
		for _, want := range []string{"johnny up 5s", "stella up 5s", "emma up 5s", "freddy down"} {
			if !strings.Contains(line, want) {
				t.Errorf("presence line %q does not list %q", line, want)
			}
		}
	})

	t.Run("PresenceAgesOutHeartbeats", func(t *testing.T) {
		// emma crashes: she never beats again. johnny and stella keep
		// beating on the production cadence while the clock passes
		// emma's TTL.
		lastBeat := t0
		for elapsed := presence.DefaultEvery; elapsed <= ttl+presence.DefaultEvery; elapsed += presence.DefaultEvery {
			mr.FastForward(presence.DefaultEvery)
			lastBeat = t0.Add(elapsed)
			for _, n := range []string{"johnny", "stella"} {
				if err := presence.Beat(ctx, st, n, lastBeat, ttl); err != nil {
					t.Fatalf("presence.Beat %s: %v", n, err)
				}
			}
		}
		now := lastBeat.Add(time.Second)

		sts, err := presence.Read(ctx, st, roster, now)
		if err != nil {
			t.Fatalf("presence.Read: %v", err)
		}
		want := []presence.State{presence.Up, presence.Up, presence.Away, presence.Never}
		for i, n := range roster {
			if sts[i].State != want[i] {
				t.Errorf("%s past emma's ttl: state %v; want %v", n, sts[i].State, want[i])
			}
		}
		if !sts[2].Dated || !sts[2].Last.Equal(t0) {
			t.Errorf("emma aged out without her last beat: dated=%v last=%v; want %v", sts[2].Dated, sts[2].Last, t0)
		}
		line := presence.Line(sts, now)
		if strings.Contains(line, "emma up") || !strings.Contains(line, "emma down") {
			t.Errorf("presence line %q still lists emma live after her ttl", line)
		}

		// No tombstone: nothing was written when emma aged out. Her
		// presence key never expires (#3878) and still holds the beat
		// of t0; the keys under her name are that hash and the untimed
		// memory Beat wrote at t0, byte for byte.
		if got := mr.HGet(presence.Key("emma"), presence.FieldAt); got != t0.Format(presence.Stamp) {
			t.Errorf("%s at = %q after ageing out; want the last beat %q, kept", presence.Key("emma"), got, t0.Format(presence.Stamp))
		}
		var emmaKeys []string
		for _, k := range mr.Keys() {
			if strings.HasPrefix(k, presence.Key("emma")) {
				emmaKeys = append(emmaKeys, k)
			}
		}
		sort.Strings(emmaKeys)
		if len(emmaKeys) != 2 || emmaKeys[0] != presence.Key("emma") || emmaKeys[1] != presence.LastKey("emma") {
			t.Errorf("keys left for emma after ageing out: %v; want %s and %s (no tombstone)", emmaKeys, presence.Key("emma"), presence.LastKey("emma"))
		}
		if v, _ := mr.Get(presence.LastKey("emma")); v != t0.Format(presence.Stamp) {
			t.Errorf("%s = %q after ageing out; want the last beat %q untouched", presence.LastKey("emma"), v, t0.Format(presence.Stamp))
		}
		if got := len(mr.Keys()); got != 2*len(live) {
			t.Errorf("store holds %d keys after emma aged out; want %d (two per friend who ever beat, no tombstone)", got, 2*len(live))
		}
	})
}
