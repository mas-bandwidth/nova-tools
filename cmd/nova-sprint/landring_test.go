package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fnv64a is the hash the ring is built on, computed here and not in the code under test.
func fnv64a(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

// The ring over n benches starts at FNV-1a 64-bit of the key modulo n and goes round
// once: every host exactly once, in the fleet's order from that slot; the same key on the
// same ring gives the same order; and no hosts is no ring.
func TestTheBenchRingStartsAtTheKeysSlotAndGoesRoundOnce(t *testing.T) {
	t.Parallel()
	keys := []string{"s1", "s2", "mechanical", "fix", "tla", "a-rework-is-priority-fix-bb", ""}
	for _, n := range []int{1, 2, 3, 5} {
		hosts := make([]string, n)
		for i := range hosts {
			hosts[i] = fmt.Sprintf("bench-%d", i)
		}
		for _, key := range keys {
			t.Run(fmt.Sprintf("n=%d key=%q", n, key), func(t *testing.T) {
				t.Parallel()
				slot := int(fnv64a(key) % uint64(n))
				assert.Equal(t, slot, ringSlot(key, n), "the slot is the hash modulo n")
				ring := benchRing(key, hosts)
				require.Len(t, ring, n, "every host once")
				for i, h := range ring {
					assert.Equal(t, hosts[(slot+i)%n], h, "ring[%d] is hosts[(slot+%d) %% n]", i, i)
				}
				assert.ElementsMatch(t, hosts, ring, "a rotation, nothing added or lost")
				assert.Equal(t, ring, benchRing(key, hosts), "the same key and ring give the same order")
				assert.Equal(t, hosts, append([]string(nil), hosts...), "the fleet's order is not changed")
			})
		}
	}
	assert.Nil(t, benchRing("s1", nil), "no hosts is no ring")
	assert.Nil(t, benchRing("s1", []string{}), "no hosts is no ring")
	assert.Equal(t, 0, ringSlot("s1", 0), "a ring of none has one slot to name")
}

// Twenty streams on a ring of two or more spread: at least two distinct first choices,
// so the gates no longer all land on the first up member.
func TestTheBenchRingSpreadsStreamsOverTheBenches(t *testing.T) {
	t.Parallel()
	for _, n := range []int{2, 3, 5} {
		hosts := make([]string, n)
		for i := range hosts {
			hosts[i] = fmt.Sprintf("bench-%d", i)
		}
		first := map[string]bool{}
		for i := range 20 {
			first[benchRing(fmt.Sprintf("stream-%d", i), hosts)[0]] = true
		}
		assert.GreaterOrEqual(t, len(first), 2, "n=%d: the first choices of 20 streams: %v", n, first)
	}
}

// Through the land loop's bench seam: with two benches up and the Go lane of the slot the
// stream hashes to held by someone else, the gate steps to the next slot's bench, runs
// there, and the LAND line names the bench, the ring and the slot. The held lane is left
// as it was, and the lander waits in no queue after.
func TestAHeldFirstSlotLaneSendsTheGateToTheNextBench(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"})}
	r.queued(heads, "s1-1")
	for _, m := range []string{"vision", "space"} {
		r.ok("fleet beat " + m + " --load 1 --cores 8")
		r.ok("fleet up " + m)
	}
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && name != "space" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	hosts := s.UpMembers()
	require.ElementsMatch(t, []string{"vision", "space"}, hosts, "the two benches up, in the fleet's order")
	ring := benchRing("s1", hosts)
	slot := int(fnv64a("s1") % 2)
	require.Equal(t, hosts[slot], ring[0], "the stream's slot on the ring")
	held, next := ring[0], ring[1]
	r.ok("lane take go --machine " + held + " --as other")

	var asked sync.Mutex
	var benches []string
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.landMore = []string{"--repo-dir", r.clone, "--base", "main"}
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked.Lock()
		benches = append(benches, host)
		asked.Unlock()
		return "", 0, nil
	}
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out beatBuf
	deadline := time.Now().Add(2 * time.Minute)
	r.a.sleep = func(time.Duration) {
		text := out.String()
		if strings.Contains(text, "LAND REFUSED") || strings.Contains(text, "LAND OK") || strings.Contains(text, "LAND FAILED") || time.Now().After(deadline) {
			cancel()
			return
		}
		for time.Now().Before(deadline) {
			b.mu.Lock()
			f := b.flight
			b.mu.Unlock()
			if f == nil {
				return
			}
			f.mu.Lock()
			done := f.done
			f.mu.Unlock()
			if done {
				return
			}
			runtime.Gosched()
		}
	}
	r.a.landLoop(ctx, "mem:0", &out)
	text := out.String()

	asked.Lock()
	got := append([]string(nil), benches...)
	asked.Unlock()
	require.NotEmpty(t, got, "the gate went to a bench:\n%s", text)
	for _, h := range got {
		assert.Equal(t, next, h, "the held slot %s is skipped for the next, %s: %v", held, next, got)
	}
	assert.Regexp(t, regexp.MustCompile(`LAND OK stream=s1 .* bench=`+next+` wall=\d+\.\ds ring=2 slot=`+fmt.Sprint(slot)+`\b`), text)
	lanes := r.ok("lane list")
	assert.Contains(t, lanes, "machine="+held+" held=other", "the held lane is left as it was: %s", lanes)
	assert.NotContains(t, lanes, "lander", "the lander holds and waits on no lane after: %s", lanes)
	r.clean()
}
