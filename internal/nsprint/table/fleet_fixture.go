package table

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// Fleet fixture sizes (#3893): the shape of the fleet store on 2026-09-25,
// rounded up, so the wide table is proven to render at the size it refused.
const (
	FleetCards       = 1000
	FleetStreams     = 13
	FleetFriends     = 4
	FleetBenches     = 7
	FleetOpenSprints = 9
)

// fleetWhere is the card model's sets, in order: every card is in one.
var fleetWhere = []string{"waiting", "ready", "working", "done"}

// fleetState is the sprint index each where maps to in the wide pipeline.
var fleetState = map[string]string{
	"waiting": "queued",
	"ready":   "queued",
	"working": "running",
	"done":    "landed",
}

// FleetFixture seeds a fleet-shaped keyspace: FleetCards cards spread over
// FleetStreams streams and FleetOpenSprints open sprints, FleetBenches
// benches, FleetFriends friends, and a sprint registry that also holds closed
// sprints and a control sprint, 13 names in all (the registry bound of 4
// refused the fleet at 9). Each card is a card:<id> record whose where names
// the one ws:<stream>:<where> set that holds it, scored by created_at; the
// wide table reads the per-sprint card indexes and the bench queues beside
// them. Commands are variadic so the whole seed is a few hundred calls.
func FleetFixture() [][]string { return FleetFixtureOf(FleetCards) }

// FleetFixtureOf is FleetFixture with n cards in place of FleetCards, so a
// test can show the table's cost does not grow with the cards.
func FleetFixtureOf(n int) [][]string {
	var cmds [][]string
	sprints := []string{"SADD", "sprints"}
	for s := 1; s <= FleetOpenSprints; s++ {
		name := fmt.Sprintf("fleet-%02d", s)
		sprints = append(sprints, name)
		cmds = append(cmds, []string{"HSET", "s:" + name, "status", "open"})
	}
	for _, name := range []string{"old-1", "old-2", "old-3"} {
		sprints = append(sprints, name)
		cmds = append(cmds, []string{"HSET", "s:" + name, "status", "closed"})
	}
	sprints = append(sprints, "control-fleet")
	cmds = append(cmds, []string{"HSET", "s:control-fleet", "status", "open"})
	cmds = append(cmds, sprints)

	benches := []string{"SADD", "benches"}
	for b := 1; b <= FleetBenches; b++ {
		name := fmt.Sprintf("b%d", b)
		benches = append(benches, name)
		cmds = append(cmds,
			[]string{"HSET", "bench:" + name + ":desired", "slots", "8"},
			[]string{"HSET", "bench:" + name + ":beat", "at", "1"})
	}
	cmds = append(cmds, benches)
	friends := []string{"SADD", "friends"}
	for _, name := range []string{"emma", "johnny", "rowan", "stella"}[:FleetFriends] {
		friends = append(friends, name)
		cmds = append(cmds,
			[]string{"HSET", "friend:" + name + ":desired", "slots", "32"},
			[]string{"HSET", "friend:" + name + ":beat", "at", "1"})
	}
	cmds = append(cmds, friends)

	order := []string{"ZADD", "ws:order"}
	sets := map[string][]string{}
	var keys []string
	add := func(key string, members ...string) {
		if _, ok := sets[key]; !ok {
			keys = append(keys, key)
		}
		sets[key] = append(sets[key], members...)
	}
	for st := 1; st <= FleetStreams; st++ {
		order = append(order, fmt.Sprint(st), fmt.Sprintf("st%02d", st))
	}
	cmds = append(cmds, order)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%04d", i)
		stream := fmt.Sprintf("st%02d", i%FleetStreams+1)
		sprint := fmt.Sprintf("fleet-%02d", i%FleetOpenSprints+1)
		where := fleetWhere[i%len(fleetWhere)]
		created := fmt.Sprint(1790000000000 + int64(i)*1000)
		bench := fmt.Sprintf("b%d", i%FleetBenches+1)
		cmds = append(cmds, []string{"HSET", "card:" + id,
			"origin", "https://github.com/mas-bandwidth/nova-tools/issues/" + fmt.Sprint(3000+i),
			"stream", stream, "where", where, "created_at", created, "where_at", created,
			"bench", bench})
		add("Z "+ws.KeyAt(0, stream, where), created, id)
		add("Z sprint:"+sprint+":cards", created, id)
		add("S s:"+sprint+":idx:card:"+fleetState[where], id)
		switch where {
		case "ready":
			add("Z "+ws.SprintListAt(0, sprint, "pool"), created, id)
		case "working":
			add("Z s:"+sprint+":bench:"+bench+":queue", created, id)
		case "done":
			add("S s:"+sprint+":bench:"+bench+":ended", id)
		}
	}
	for _, key := range keys {
		verb := "ZADD"
		if key[0] == 'S' {
			verb = "SADD"
		}
		cmds = append(cmds, append([]string{verb, key[2:]}, sets[key]...))
	}
	return cmds
}
