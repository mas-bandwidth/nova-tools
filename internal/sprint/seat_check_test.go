package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seat check (docs/SPEC-SPRINT.md, "The seat check"; the owner, 2026-10-04 1:52 PM:
// "what is manual needs a verb: nova-sprint seat check"): checks server, store,
// loop, beats, readers, dashboard, installed versions, merge queue; prints one
// line per check and an exit code (0 all up, 1 on any red/DOWN check).

func upMeasures() SeatCheckMeasures {
	return SeatCheckMeasures{
		Server:    ServerM{Addr: "127.0.0.1:6390", Millis: 12, PID: 4242},
		Store:     StoreM{Addr: "mem:sprint.twin", DBSize: -1, Machine: "running", Epoch: 7, Tick: TickM{Ticked: true, Age: 3 * time.Second, Ticks: 1200}},
		Fleet:     []MemberM{{Name: "m1", Status: "up", Beaten: true, Age: 3 * time.Second}, {Name: "m2", Status: "held"}},
		Friends:   []FriendM{{Name: "friend-a", Status: "up", Beaten: true, Age: 2 * time.Second}},
		Readers:   ReadersM{Total: 2, Up: 2, Reading: 1},
		Dashboard: DashM{Addr: "127.0.0.1:7390", Status: 200, Build: "3f2a"},
		Bus:       BusM{Addr: ""},
		Inbox:     InboxM{Open: 2, Oldest: 42 * time.Minute},
		Queue:     QueueM{Measured: true, Entries: []string{"#101"}},
		Versions:  VersionsM{Measured: true, Dev: "v1.2.3", Machines: []MachineVersionM{{Machine: "m1", Version: "v1.2.3"}}},
		Host:      "host-a",
	}
}

func TestSeatCheckPrintsOneLinePerCheckWithAnExitCode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 13, 52, 0, 0, time.UTC)

	// 1. Healthy twin store: all checks pass, exit code is 0, one line per check.
	t.Run("twin store all green", func(t *testing.T) {
		t.Parallel()
		m := upMeasures()
		r := JudgeSeatCheck(m, now)
		assert.Equal(t, 0, r.ExitCode, "all checks up must exit 0")

		text := r.Text()
		lines := strings.Split(strings.TrimSpace(text), "\n")
		// 1 line per check (server, store, loop, fleet, friends, readers, dashboard, bus, inbox, queue, versions) + summary line
		require.Len(t, lines, 12, "must have exactly 12 lines: 11 checks plus summary")

		expectedThings := []string{
			SeatCheckServer,
			SeatCheckStore,
			SeatCheckLoop,
			SeatCheckFleet,
			SeatCheckFriends,
			SeatCheckReaders,
			SeatCheckDashboard,
			SeatCheckBus,
			SeatCheckInbox,
			SeatCheckQueue,
			SeatCheckVersions,
		}

		thingsSeen := make(map[string]int)
		for i, l := range lines[:11] {
			parts := strings.SplitN(l, " ", 3)
			require.GreaterOrEqual(t, len(parts), 2, "line %d malformed: %s", i, l)
			assert.Equal(t, SeatCheckToken, parts[0])
			thing := parts[1]
			thingsSeen[thing]++
		}

		for _, thing := range expectedThings {
			assert.Equal(t, 1, thingsSeen[thing], "thing %s must appear exactly once", thing)
		}
		assert.Equal(t, len(expectedThings), len(thingsSeen), "exactly 11 distinct checks must appear")

		assert.Contains(t, text, "MACHINERY server OK addr=127.0.0.1:6390 ms=12 pid=4242\n")
		assert.Contains(t, text, "MACHINERY store OK redis=mem:sprint.twin dbsize=- machine=running epoch=7\n")
		assert.Contains(t, text, "MACHINERY loop OK tick_age=3s ticks=1200\n")
		assert.Contains(t, text, "MACHINERY fleet OK up=1 held=1 down=0\n")
		assert.Contains(t, text, "MACHINERY friends OK up=1 held=0 down=0\n")
		assert.Contains(t, text, "MACHINERY readers OK total=2 up=2 reading=1\n")
		assert.Contains(t, text, "MACHINERY dashboard OK addr=127.0.0.1:7390 status=200 build=3f2a\n")
		assert.Contains(t, text, "MACHINERY bus OK redis=none note=\"not configured: NOVA_BUS_REDIS is not set\"\n")
		assert.Contains(t, text, "MACHINERY inbox OK open=2 oldest=42m0s next=\"nova-sprint inbox\"\n")
		assert.Contains(t, text, "MACHINERY queue OK entries=#101\n")
		assert.Contains(t, text, "MACHINERY versions OK dev=v1.2.3 machines=1\n")
		assert.Equal(t, "MACHINERY OK n=11", lines[11])
	})

	// 2. Each check failing (DOWN) exits non-zero (1) and prints its DOWN line with a remedy command.
	cases := []struct {
		check string
		set   func(*SeatCheckMeasures)
		want  string
	}{
		{
			check: "server not named",
			set:   func(m *SeatCheckMeasures) { m.Server = ServerM{} },
			want:  `MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-host-a | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')"`,
		},
		{
			check: "server connection error",
			set:   func(m *SeatCheckMeasures) { m.Server = ServerM{Addr: "127.0.0.1:6390", Err: "connection refused"} },
			want:  `MACHINERY server DOWN addr=127.0.0.1:6390 why="connection refused" remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.sprint-server-host-a.plist"`,
		},
		{
			check: "store down twin",
			set:   func(m *SeatCheckMeasures) { m.Store.Err = "NOAUTH" },
			want:  `MACHINERY store DOWN redis=mem:sprint.twin why="NOAUTH" remedy="nova-sprint where --redis mem:sprint.twin"`,
		},
		{
			check: "store down network redis",
			set: func(m *SeatCheckMeasures) {
				m.Store.Addr = "127.0.0.1:6379"
				m.Store.Err = "connection refused"
			},
			want: `MACHINERY store DOWN redis=127.0.0.1:6379 why="connection refused" remedy="redis-cli -h 127.0.0.1 -p 6379 ping"`,
		},
		{
			check: "loop silent",
			set:   func(m *SeatCheckMeasures) { m.Store.Tick.Ticked = true; m.Store.Tick.Age = 5 * time.Minute },
			want:  `MACHINERY loop DOWN tick_age=5m0s ticks=1200 why="silent past 15s" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-host-a"`,
		},
		{
			check: "fleet member down",
			set: func(m *SeatCheckMeasures) {
				m.Fleet = append(m.Fleet, MemberM{Name: "m3", Status: "down", Beaten: true, Age: 3 * time.Hour})
			},
			want: `MACHINERY fleet DOWN up=1 held=1 down=m3:3h0m0s remedy="nova-config loop show member-m3"`,
		},
		{
			check: "friend down",
			set: func(m *SeatCheckMeasures) {
				m.Friends = []FriendM{{Name: "friend-b", Status: "down", Beaten: true, Age: 20 * time.Minute}}
			},
			want: `MACHINERY friends DOWN up=0 held=0 down=friend-b:20m0s remedy="launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nova.loop.friend-beat-friend-b.plist"`,
		},
		{
			check: "readers none up",
			set:   func(m *SeatCheckMeasures) { m.Readers = ReadersM{Total: 2, Up: 0} },
			want:  `MACHINERY readers DOWN total=2 up=0 reading=0 why="no reader is up" remedy="nova-config loop list | grep reader-"`,
		},
		{
			check: "dashboard error",
			set:   func(m *SeatCheckMeasures) { m.Dashboard = DashM{Addr: "127.0.0.1:7390", Err: "connection refused"} },
			want:  `MACHINERY dashboard DOWN addr=127.0.0.1:7390 why="connection refused" remedy="nova-sprint dashboard --listen 127.0.0.1:7390"`,
		},
		{
			check: "merge queue thrown out",
			set:   func(m *SeatCheckMeasures) { m.Queue.ThrownOut = []string{"#42"} },
			want:  `MACHINERY queue DOWN entries=#101 thrown=#42 why="pull request #42 thrown out" remedy="gh pr view #42"`,
		},
		{
			check: "installed versions stale",
			set: func(m *SeatCheckMeasures) {
				m.Versions = VersionsM{
					Dev: "v1.2.3",
					Machines: []MachineVersionM{
						{Machine: "m1", Version: "v1.2.0"},
					},
				}
			},
			want: `MACHINERY versions DOWN machine=m1 version=v1.2.0 dev=v1.2.3 remedy="nova-update apply"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.check, func(t *testing.T) {
			t.Parallel()
			m := upMeasures()
			tc.set(&m)
			report := JudgeSeatCheck(m, now)
			assert.Equal(t, 1, report.ExitCode, "a down check must exit non-zero (1)")
			text := report.Text()
			assert.Contains(t, text, tc.want+"\n", "must print the expected DOWN line:\n%s", text)
			assert.Contains(t, text, "MACHINERY DOWN n=", "summary must be DOWN:\n%s", text)
			require.Greater(t, report.Down, 0)
			for _, l := range report.Lines {
				if !l.Up {
					assert.NotEmpty(t, l.Remedy, "every DOWN line carries a remedy")
					assert.False(t, strings.HasSuffix(l.Remedy, "."), "remedy is a command, not prose: %s", l.Remedy)
				}
			}
		})
	}

	t.Run("unmeasured queue and versions", func(t *testing.T) {
		t.Parallel()
		m := upMeasures()
		m.Queue = QueueM{}
		m.Versions = VersionsM{}
		r := JudgeSeatCheck(m, now)
		assert.Equal(t, 0, r.ExitCode)
		text := r.Text()
		assert.Contains(t, text, `MACHINERY queue OK note="not measured: probe not configured"`+"\n")
		assert.NotContains(t, text, "entries=0")
		assert.Contains(t, text, `MACHINERY versions OK note="not measured: probe not configured"`+"\n")
		assert.NotContains(t, text, "machines=0")
	})
}
