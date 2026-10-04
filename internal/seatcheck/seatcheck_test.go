package seatcheck

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Date(2026, 10, 3, 23, 21, 0, 0, time.UTC)

// up is every measure up: the baseline each DOWN test changes one thing of.
func up() Measures {
	return Measures{
		Server:    ServerM{Addr: "127.0.0.1:6390", Millis: 12, PID: 4242},
		Store:     StoreM{Addr: "127.0.0.1:6380", DBSize: 1234, Machine: "running", Epoch: 7, Tick: TickM{Ticked: true, Age: 3 * time.Second, Ticks: 1200}},
		Fleet:     []MemberM{{Name: "m1", Status: "up", Beaten: true, Age: time.Second}, {Name: "m2", Status: "held", Beaten: true, Age: time.Second}},
		Friends:   []FriendM{{Name: "friend-c", Status: "up", Beaten: true, Age: time.Second}},
		Readers:   ReadersM{Total: 2, Up: 2, Reading: 1},
		Dashboard: DashM{Addr: "127.0.0.1:7390", Status: 200, Build: "3f2a"},
		Inbox:     InboxM{Open: 2, Oldest: 42 * time.Minute},
		Host:      "host-a",
	}
}

func lines(r Report) string { return r.Text() }

func TestEverythingUp(t *testing.T) {
	t.Parallel()
	r := Judge(up(), t0)
	assert.Equal(t, 0, r.Down)
	assert.Equal(t, strings.Join([]string{
		"MACHINERY server OK addr=127.0.0.1:6390 ms=12 pid=4242",
		"MACHINERY store OK redis=127.0.0.1:6380 dbsize=1234 machine=running epoch=7",
		"MACHINERY loop OK tick_age=3s ticks=1200",
		"MACHINERY fleet OK up=1 held=1 down=0",
		"MACHINERY friends OK up=1 held=0 down=0",
		"MACHINERY readers OK total=2 up=2 reading=1",
		"MACHINERY dashboard OK addr=127.0.0.1:7390 status=200 build=3f2a",
		`MACHINERY bus OK redis=none note="not configured: NOVA_BUS_REDIS is not set"`,
		`MACHINERY inbox OK open=2 oldest=42m0s next="nova-sprint inbox"`,
		"MACHINERY OK n=9",
	}, "\n")+"\n", lines(r))
}

// Every DOWN line, its exact text and its remedy, a command.
func TestEveryDownLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		set  func(m *Measures)
		want string
	}{
		{"server not named", func(m *Measures) { m.Server = ServerM{} },
			`MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-host-a | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')"`},
		{"server not answering", func(m *Measures) { m.Server = ServerM{Addr: "127.0.0.1:6390", Err: "dial tcp: connection refused"} },
			`MACHINERY server DOWN addr=127.0.0.1:6390 why="dial tcp: connection refused" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.sprint-server-host-a.plist"`},
		{"store not answering", func(m *Measures) { m.Store.Err = "NOAUTH" },
			`MACHINERY store DOWN redis=127.0.0.1:6380 why="NOAUTH" remedy="redis-cli -h 127.0.0.1 -p 6380 ping"`},
		{"loop with no store", func(m *Measures) { m.Store.Err = "NOAUTH" },
			`MACHINERY loop DOWN why="the store did not answer: the heartbeat was not read" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-host-a"`},
		{"loop never ticked", func(m *Measures) { m.Store.Tick = TickM{} },
			`MACHINERY loop DOWN tick=never why="no heartbeat: run --listen has not ticked this store" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-host-a"`},
		{"loop silent", func(m *Measures) { m.Store.Tick.Age = 4*time.Hour + 12*time.Minute },
			`MACHINERY loop DOWN tick_age=4h12m0s ticks=1200 why="silent past 15s" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-host-a"`},
		{"loop failing", func(m *Measures) { m.Store.Tick.Failures, m.Store.Tick.Error = 3, "deal: WRONGTYPE" },
			`MACHINERY loop DOWN tick_age=3s ticks=1200 failures=3 why="deal: WRONGTYPE" remedy="nova-sprint log --max 20"`},
		{"fleet member down", func(m *Measures) {
			m.Fleet = append(m.Fleet, MemberM{Name: "m3", Status: "down", Beaten: true, Age: 3 * time.Hour}, MemberM{Name: "m4", Status: "down"})
		}, `MACHINERY fleet DOWN up=1 held=1 down=m3:3h0m0s,m4:never remedy="nova-config loop show member-m3; nova-config loop show member-m4"`},
		{"fleet member the table calls up with a stale beat", func(m *Measures) {
			m.Fleet = []MemberM{{Name: "m1", Status: "up", Beaten: true, Age: 46 * time.Second}}
		}, `MACHINERY fleet DOWN up=0 held=0 down=m1:46s remedy="nova-config loop show member-m1"`},
		{"friend down, agent not loaded", func(m *Measures) {
			m.Friends = []FriendM{{Name: "friend-a", Status: "down", Beaten: true, Age: 80 * time.Minute, Loaded: "not loaded"}}
		},
			`MACHINERY friends DOWN friend=friend-a beat_age=1h20m0s label=com.nova.loop.friend-beat-friend-a agent="not loaded" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.friend-beat-friend-a.plist"` + "\n" +
				`MACHINERY friends DOWN up=0 held=0 down=1 remedy="nova-sprint where"`},
		{"friend down, no launchd here", func(m *Measures) {
			m.Host = "bench-a"
			m.Friends = []FriendM{{Name: "friend-b", Status: "down"}}
		}, `MACHINERY friends DOWN friend=friend-b beat_age=never label=com.nova.loop.friend-beat-friend-b agent="not measured: no launchd on bench-a" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.friend-beat-friend-b.plist"`},
		{"no reader up", func(m *Measures) { m.Readers = ReadersM{Total: 2} },
			`MACHINERY readers DOWN total=2 up=0 reading=0 why="no reader is up" remedy="nova-config loop list | grep reader-"`},
		{"dashboard not answering", func(m *Measures) { m.Dashboard = DashM{Addr: "127.0.0.1:7390", Err: "connection refused"} },
			`MACHINERY dashboard DOWN addr=127.0.0.1:7390 why="connection refused" remedy="nova-sprint dashboard --listen 127.0.0.1:7390"`},
		{"dashboard not 200", func(m *Measures) { m.Dashboard = DashM{Addr: "127.0.0.1:7390", Status: 503} },
			`MACHINERY dashboard DOWN addr=127.0.0.1:7390 status=503 remedy="nova-sprint dashboard --listen 127.0.0.1:7390"`},
		{"dashboard with no build", func(m *Measures) { m.Dashboard = DashM{Addr: "127.0.0.1:7390", Status: 200} },
			`MACHINERY dashboard DOWN addr=127.0.0.1:7390 status=200 why="/api/sprint carries no build id" remedy="nova-sprint dashboard --listen 127.0.0.1:7390"`},
		{"bus not answering", func(m *Measures) { m.Bus = BusM{Addr: "127.0.0.1:6381", Err: "connection refused"} },
			`MACHINERY bus DOWN redis=127.0.0.1:6381 why="connection refused" remedy="redis-cli -h 127.0.0.1 -p 6381 ping"`},
		{"a probe that failed", func(m *Measures) { m.Errs = map[string]string{Fleet: "the fleet table is not there"} },
			`MACHINERY fleet DOWN err="the fleet table is not there" remedy="nova-sprint where"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := up()
			tc.set(&m)
			r := Judge(m, t0)
			assert.Contains(t, lines(r), tc.want+"\n", lines(r))
			assert.Greater(t, r.Down, 0)
			assert.Contains(t, lines(r), "MACHINERY DOWN n=", lines(r))
			for _, l := range r.Lines {
				if !l.Up {
					assert.NotEmpty(t, l.Remedy, l.String())
					assert.False(t, strings.HasSuffix(l.Remedy, "."), "a remedy is a command, never prose: %s", l.Remedy)
				}
			}
		})
	}
}

// The bus configured and answering is OK; the inbox is never DOWN; the
// summary counts DOWN lines against all lines.
func TestSummaryAndNeverDown(t *testing.T) {
	t.Parallel()
	m := up()
	m.Bus = BusM{Addr: "127.0.0.1:6381"}
	m.Inbox = InboxM{Open: 0}
	m.Friends = []FriendM{{Name: "a", Status: "down"}, {Name: "b", Status: "down"}}
	r := Judge(m, t0)
	assert.Contains(t, lines(r), "MACHINERY bus OK redis=127.0.0.1:6381\n")
	assert.Contains(t, lines(r), "MACHINERY inbox OK open=0\n")
	assert.Equal(t, 3, r.Down)
	assert.Equal(t, "MACHINERY DOWN n=3 of=11", r.Summary())
	assert.Contains(t, r.JSON(), `"down":3`)
}

// A check that ran in the server's own process (Server.Self) measured nothing
// outside the store: the dashboard, the bus and a friend's agent say so, and
// are not DOWN for it.
func TestServedCheckSaysNotMeasured(t *testing.T) {
	t.Parallel()
	m := up()
	m.Server = ServerM{Addr: "127.0.0.1:6390", Self: true, PID: 77}
	m.Dashboard = DashM{Addr: "127.0.0.1:7390"}
	m.Bus = BusM{Addr: "127.0.0.1:6381"}
	m.Friends = []FriendM{{Name: "friend-a", Status: "down"}}
	r := Judge(m, t0)
	note := `"not measured: the check ran in the server; run nova-sprint machinery"`
	assert.Contains(t, lines(r), "MACHINERY server OK addr=127.0.0.1:6390 self=true pid=77\n")
	assert.Contains(t, lines(r), "MACHINERY friends DOWN friend=friend-a beat_age=never label=com.nova.loop.friend-beat-friend-a agent="+note+" remedy=")
	assert.Contains(t, lines(r), "MACHINERY dashboard OK addr=127.0.0.1:7390 note="+note+"\n")
	assert.Contains(t, lines(r), "MACHINERY bus OK redis=127.0.0.1:6381 note="+note+"\n")
	assert.Equal(t, 2, r.Down, lines(r))
	m.Bus = BusM{}
	assert.Contains(t, lines(Judge(m, t0)), `MACHINERY bus OK redis=none note="not configured: NOVA_BUS_REDIS is not set"`+"\n")
}
