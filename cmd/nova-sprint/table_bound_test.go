//go:build functional

package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// TestWideTableRendersTheFleet is #3893's DONE-WHEN, first half: `nova-sprint
// table --redis` over a fleet-shaped store (1,000 cards, 13 streams, 4
// friends, 7 benches, 13 sprints in the registry) renders every row in under
// one second. On the base it printed only `RED snapshot: bound exceeded`: the
// registry of every sprint ever made was bounded at 4.
//
// Under one second is proven by events, not the clock (internal/ci refuses a
// wall-clock assertion): the render is one FCALL_RO, the reads it makes inside
// the server are counted from INFO commandstats and are the same at 1,000 and
// 2,000 cards, and they stay under fleetReadCeiling O(1) reads (SCARD, ZCARD,
// ZCOUNT, HGET, EXISTS on small registries), microseconds each. The measured
// time is logged.
func TestWideTableRendersTheFleet(t *testing.T) {
	t.Parallel()

	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	var reads [2]int64
	for i, cards := range []int{table.FleetCards, 2 * table.FleetCards} {
		if err := client.FlushAll(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		seed(t, addr, table.FleetFixtureOf(cards))
		if err := client.ConfigResetStat(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		code, stdout, stderr := runSprint("table", "--redis", addr, "--once")
		t.Logf("fleet table of %d cards rendered in %s", cards, time.Since(start))
		if code != 0 || strings.Contains(stdout, "RED ") {
			t.Fatalf("exit %d stderr %q; the fleet table refused:\n%s", code, stderr, stdout)
		}
		calls, fcalls := serverCalls(t, client)
		if fcalls != 1 {
			t.Fatalf("%d cards: %d FCALL_RO, want one per rendered table", cards, fcalls)
		}
		reads[i] = calls
		checkFleetRows(t, stdout, cards)
	}
	if reads[0] != reads[1] {
		t.Errorf("reads grew with the cards: %d at %d cards, %d at %d", reads[0], table.FleetCards, reads[1], 2*table.FleetCards)
	}
	if reads[0] > fleetReadCeiling {
		t.Errorf("the fleet table made %d reads, over %d", reads[0], fleetReadCeiling)
	}
	t.Logf("fleet table: one FCALL_RO, %d reads inside it", reads[0])
}

// fleetReadCeiling bounds the O(1) reads one fleet render makes inside
// ns_snapshot; a server does hundreds of thousands a second.
const fleetReadCeiling = 1000

// serverCalls returns the commands the server ran since CONFIG RESETSTAT,
// the calls a function makes included, less the FCALL_RO itself and the
// connection's own setup; and the FCALL_RO count.
func serverCalls(t *testing.T, client *redis.Client) (int64, int64) {
	t.Helper()
	info, err := client.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	var calls, fcalls int64
	stat := regexp.MustCompile(`^cmdstat_([a-z_|]+):calls=(\d+)`)
	for _, line := range strings.Split(info, "\n") {
		m := stat.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.ParseInt(m[2], 10, 64)
		switch {
		case m[1] == "fcall_ro":
			fcalls += n
		case m[1] == "hello", m[1] == "ping", m[1] == "info", m[1] == "config|resetstat",
			strings.HasPrefix(m[1], "client"):
		default:
			calls += n
		}
	}
	return calls, fcalls
}

func checkFleetRows(t *testing.T, stdout string, cards int) {
	t.Helper()
	count := func(prefix string) int {
		n := 0
		for _, line := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(line, prefix) {
				n++
			}
		}
		return n
	}
	if got := count("bench:"); got != table.FleetBenches {
		t.Errorf("bench rows=%d, want %d", got, table.FleetBenches)
	}
	if got := count("friend:"); got != table.FleetFriends {
		t.Errorf("friend rows=%d, want %d", got, table.FleetFriends)
	}
	if got := count("pipeline "); got != table.FleetOpenSprints {
		t.Errorf("pipeline rows=%d, want %d (closed and control sprints hidden)", got, table.FleetOpenSprints)
	}
	sum := 0
	cell := regexp.MustCompile(`\b(queued|running|landed)=(\d+)`)
	for _, m := range cell.FindAllStringSubmatch(stdout, -1) {
		n, _ := strconv.Atoi(m[2])
		sum += n
	}
	if sum != cards {
		t.Errorf("pipeline cells count %d cards, want %d:\n%s", sum, cards, stdout)
	}
}

// TestWideTableRefusalNamesTheBound is #3893's DONE-WHEN, second half: past
// any bound the one refusal line names bound=<name> value=<n> limit=<n>
// remedy=<flag>, and the command exits 1.
func TestWideTableRefusalNamesTheBound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		bound string
		limit int
		grow  func(n int) [][]string
	}{
		{"sprints", table.MaxSprints, func(n int) [][]string {
			cmd := []string{"SADD", "sprints"}
			for i := 0; i < n; i++ {
				cmd = append(cmd, fmt.Sprintf("closed-%04d", i))
			}
			return [][]string{cmd}
		}},
		{"open_sprints", table.MaxOpenSprints, func(n int) [][]string {
			cmd := []string{"SADD", "sprints"}
			var out [][]string
			for i := 0; i < n; i++ {
				name := fmt.Sprintf("open-%04d", i)
				cmd = append(cmd, name)
				out = append(out, []string{"HSET", "s:" + name, "status", "open"})
			}
			return append(out, cmd)
		}},
		{"benches", table.MaxBenches, func(n int) [][]string {
			cmd := []string{"SADD", "benches"}
			for i := 0; i < n; i++ {
				cmd = append(cmd, fmt.Sprintf("bench-%04d", i))
			}
			return [][]string{cmd}
		}},
		{"friends", table.MaxFriends, func(n int) [][]string {
			cmd := []string{"SADD", "friends"}
			for i := 0; i < n; i++ {
				cmd = append(cmd, fmt.Sprintf("friend-%04d", i))
			}
			return [][]string{cmd}
		}},
	}
	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	for _, c := range cases {
		if err := client.FlushAll(context.Background()).Err(); err != nil {
			t.Fatal(err)
		}
		// At the limit the table renders; one past it the refusal names it.
		seed(t, addr, c.grow(c.limit))
		if code, stdout, _ := runSprint("table", "--redis", addr, "--once"); code != 0 || strings.Contains(stdout, "RED ") {
			t.Errorf("%s at its limit %d: exit %d\n%s", c.bound, c.limit, code, stdout)
		}
		seed(t, addr, c.grow(c.limit+1))
		code, stdout, _ := runSprint("table", "--redis", addr, "--once")
		want := fmt.Sprintf("RED snapshot: bound exceeded: bound=%s value=%d limit=%d remedy=%s\n",
			c.bound, c.limit+1, c.limit, table.BoundRemedy)
		if code != 1 || !strings.Contains(stdout, want) {
			t.Errorf("%s past its limit: exit %d, want 1 and %q in:\n%s", c.bound, code, want, stdout)
		}
	}
}
