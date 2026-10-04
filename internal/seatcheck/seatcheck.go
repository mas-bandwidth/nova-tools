// Package seatcheck is the seat check (docs/SPEC-SPRINT.md, "The seat check"):
// the coordinator's measure of the machinery under the sprint, run by
// nova-sprint machinery and first in coordinator and handover. The owner,
// 2026-10-03 11:21 PM ET: "as coordinator, you need to check these things
// are up when you start." That night the friends' beat agents never came back
// after a reboot and nothing at the seat takeover said so.
//
// The logic is here, apart from the transport: Probes measures (a round trip,
// a key, a beat's age, an HTTP status) and says nothing of up or down; Judge
// turns the measures into one line per thing, up or DOWN with the remedy, a
// command, on the DOWN line. Tests give Judge measures and read the lines.
package seatcheck

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The things, in the order they print.
const (
	Server     = "server"
	Store      = "store"
	Loop       = "loop"
	Fleet      = "fleet"
	Friends    = "friends"
	Readers    = "readers"
	Dashboard  = "dashboard"
	Bus        = "bus"
	Inbox      = "inbox"
	Queue      = "queue"
	MergeQueue = Queue
	Versions   = "versions"
)

// Token is the first word of every line.
const Token = "MACHINERY"

// ServerM is the sprint's server as measured: the address the environment
// names (NOVA_SPRINT_SERVER), Self when the measure ran in the server's own
// process (a served coordinator or handover), which measured nothing outside
// the store (the dashboard, the bus, the friends' agents: NotMeasured), the
// error of one verb round trip, its time, and the pid of a listener on the
// address when it is local (0 unknown).
type ServerM struct {
	Addr   string `json:"addr"`
	Self   bool   `json:"self,omitempty"`
	Err    string `json:"err,omitempty"`
	Millis int64  `json:"ms"`
	PID    int    `json:"pid,omitempty"`
}

// StoreM is the store as measured: the Redis the server uses, the error of a
// ping, DBSIZE (-1 when the store has none, the mem twin), the machine's state
// word, the epoch and the last tick.
type StoreM struct {
	Addr    string `json:"addr"`
	Err     string `json:"err,omitempty"`
	DBSize  int64  `json:"dbsize"`
	Machine string `json:"machine"`
	Epoch   uint64 `json:"epoch"`
	Tick    TickM  `json:"tick"`
}

// TickM is the run loop's own report of its last tick, the heartbeat record it
// writes: whether it ever ticked, how long since it was last seen (ticking, or
// looking at a STOPPED machine), how many ticks, and the last failure.
type TickM struct {
	Ticked   bool          `json:"ticked"`
	Age      time.Duration `json:"age_ns"`
	Ticks    int64         `json:"ticks"`
	Failures int           `json:"failures,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// MemberM is one fleet member: its status as the fleet table shows it and its
// last beat's age (Beaten false: never).
type MemberM struct {
	Name   string        `json:"name"`
	Status string        `json:"status"`
	Beaten bool          `json:"beaten"`
	Age    time.Duration `json:"age_ns"`
}

// FriendM is one friend: her status as the friends table shows it, her last
// beat's age, and, on a host with a launchd, whether her launchd agent is
// loaded there ("loaded", "not loaded"; "" where it was not measured).
type FriendM struct {
	Name   string        `json:"name"`
	Status string        `json:"status"`
	Beaten bool          `json:"beaten"`
	Age    time.Duration `json:"age_ns"`
	Loaded string        `json:"loaded,omitempty"`
}

// ReadersM is the readers table: how many readers, how many up, and how many
// reads are being read now.
type ReadersM struct {
	Total   int `json:"total"`
	Up      int `json:"up"`
	Reading int `json:"reading"`
}

// DashM is the local dashboard: the address, the HTTP status of GET /api/sprint
// (0 when the request failed, Err says how) and the build id the body carries.
type DashM struct {
	Addr   string `json:"addr"`
	Status int    `json:"status"`
	Err    string `json:"err,omitempty"`
	Build  string `json:"build,omitempty"`
}

// BusM is nova-bus2's store: the address NOVA_BUS_REDIS names ("" is not
// configured) and the error of a ping.
type BusM struct {
	Addr string `json:"addr,omitempty"`
	Err  string `json:"err,omitempty"`
}

// InboxM is the coordinator's inbox: the open judgments and the oldest wait.
type InboxM struct {
	Open   int           `json:"open"`
	Oldest time.Duration `json:"oldest_ns"`
}

// QueueM is the dev merge queue as measured: its entries, any pull request
// thrown out of the queue since the previous check, and the count of open green
// pull requests.
type QueueM struct {
	Entries   []string `json:"entries,omitempty"`
	Green     int      `json:"green,omitempty"`
	ThrownOut []string `json:"thrown_out,omitempty"`
}

// MachineVersionM is one machine's installed nova-tools version.
type MachineVersionM struct {
	Machine string `json:"machine"`
	Version string `json:"version"`
}

// VersionsM is each machine's installed nova-tools version against dev's tip.
type VersionsM struct {
	Dev      string            `json:"dev"`
	Machines []MachineVersionM `json:"machines,omitempty"`
}

// Measures is everything the probes measured, in one struct so a test can
// hand Judge any state of the machinery.
type Measures struct {
	Server    ServerM   `json:"server"`
	Store     StoreM    `json:"store"`
	Fleet     []MemberM `json:"fleet"`
	Friends   []FriendM `json:"friends"`
	Readers   ReadersM  `json:"readers"`
	Dashboard DashM     `json:"dashboard"`
	Bus       BusM      `json:"bus"`
	Inbox     InboxM    `json:"inbox"`
	Queue     QueueM    `json:"queue"`
	Versions  VersionsM `json:"versions"`
	// Host is the short host name the check ran on: the server's unit is
	// named by it.
	Host string `json:"host"`
	// Errs is each thing whose probe failed outright (a table that could not
	// be read), by thing: the line says so and is DOWN.
	Errs map[string]string `json:"errs,omitempty"`
}

// Line is one line of the check: the thing, up or DOWN, its facts in order,
// and the remedy, a command, on a DOWN line.
type Line struct {
	Thing  string   `json:"thing"`
	Up     bool     `json:"up"`
	Facts  []string `json:"facts"`
	Remedy string   `json:"remedy,omitempty"`
}

// Report is the check: the lines in order and how many are DOWN.
type Report struct {
	At    time.Time `json:"at"`
	Lines []Line    `json:"lines"`
	Down  int       `json:"down"`
	// Measures is what the lines were judged from, for --json.
	Measures Measures `json:"measures"`
}

// Bounds the judge applies; the store's own (internal/sprint/store, tick.go,
// and internal/sprint, presence.go) are repeated here by value so this
// package imports nothing of the store.
const (
	// LoopSilence is how long the run loop goes unseen (no heartbeat written,
	// ticking or looking) before it is DOWN: the store's MachineSilence.
	LoopSilence = 15 * time.Second
	// MemberDownAfter is how long a fleet member goes without a beat before it
	// is down by measurement, whatever the fleet table says (the table's status
	// is the tick's, and lags when the loop is down too): the sprint's
	// MissedBeatsDown windows of BeatDeadline.
	MemberDownAfter = 3 * 15 * time.Second
)

// memberDown says the member is down: the fleet table says so, or it is not
// held and its beat is older than MemberDownAfter (or never).
func memberDown(m MemberM) bool {
	return m.Status == "down" || (m.Status != "held" && (!m.Beaten || m.Age > MemberDownAfter))
}

// NotMeasured is what the dashboard, the bus and a friend's agent say when the
// check ran in the server (Server.Self): the server's step is single-threaded
// and waits on no outside probe; nova-sprint machinery, run where it is typed,
// measures them. None is DOWN for it.
const NotMeasured = "not measured: the check ran in the server; run nova-sprint machinery"

// Label is the launchd label that beats the friend on the friends' host today
// (com.nova.loop.friend-beat-<name>; nova-friend's agent later).
func Label(friend string) string { return "com.nova.loop.friend-beat-" + friend }

// Bootstrap is the line that loads the friend's agent.
func Bootstrap(friend string) string {
	return "launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/" + Label(friend) + ".plist"
}

// MemberLoop is the nova-config loop record that should beat the member.
func MemberLoop(member string) string { return "member-" + member }

// Judge turns the measures into the report: one line per thing in the order
// server, store, loop, fleet, friends, readers, dashboard, bus, inbox, and a
// DOWN line per friend down before the friends line. A thing whose probe
// failed is DOWN with the error.
func Judge(m Measures, now time.Time) Report {
	r := Report{At: now, Measures: m}
	add := func(l Line) {
		if !l.Up {
			r.Down++
		}
		r.Lines = append(r.Lines, l)
	}
	failed := func(thing string) bool {
		e, ok := m.Errs[thing]
		if ok {
			add(Line{Thing: thing, Facts: []string{"err=" + q(e)}, Remedy: "nova-sprint where"})
		}
		return ok
	}
	host := m.Host
	if host == "" {
		host = "<host>"
	}
	serverUnit := "com.nova.loop.sprint-server-" + host

	// 1. the server
	if !failed(Server) {
		s := m.Server
		switch {
		case s.Self:
			add(Line{Thing: Server, Up: true, Facts: []string{"addr=" + s.Addr, "self=true", "pid=" + fmt.Sprint(s.PID)}})
		case s.Addr == "":
			add(Line{Thing: Server, Facts: []string{"addr=none", "why=" + q("NOVA_SPRINT_SERVER is not set")},
				Remedy: "export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-" + host + " | sed -n 's/.*--listen [^:]*:\\([0-9]*\\).*/\\1/p')"})
		case s.Err != "":
			add(Line{Thing: Server, Facts: []string{"addr=" + s.Addr, "why=" + q(s.Err)},
				Remedy: "launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/" + serverUnit + ".plist"})
		default:
			f := []string{"addr=" + s.Addr, "ms=" + fmt.Sprint(s.Millis)}
			if s.PID > 0 {
				f = append(f, "pid="+fmt.Sprint(s.PID))
			}
			add(Line{Thing: Server, Up: true, Facts: f})
		}
	}

	// 2. the store
	st := m.Store
	if !failed(Store) {
		if st.Err != "" {
			add(Line{Thing: Store, Facts: []string{"redis=" + st.Addr, "why=" + q(st.Err)}, Remedy: "redis-cli -h " + hostOf(st.Addr) + " -p " + portOf(st.Addr) + " ping"})
		} else {
			size := "-"
			if st.DBSize >= 0 {
				size = fmt.Sprint(st.DBSize)
			}
			add(Line{Thing: Store, Up: true, Facts: []string{"redis=" + st.Addr, "dbsize=" + size, "machine=" + st.Machine, "epoch=" + fmt.Sprint(st.Epoch)}})
		}
	}

	// 3. the run loop, from the heartbeat the loop writes
	if !failed(Loop) {
		t := st.Tick
		kick := "launchctl kickstart -k gui/$(id -u)/" + serverUnit
		switch {
		case st.Err != "":
			add(Line{Thing: Loop, Facts: []string{"why=" + q("the store did not answer: the heartbeat was not read")}, Remedy: kick})
		case !t.Ticked:
			add(Line{Thing: Loop, Facts: []string{"tick=never", "why=" + q("no heartbeat: run --listen has not ticked this store")}, Remedy: kick})
		case t.Age > LoopSilence:
			add(Line{Thing: Loop, Facts: []string{"tick_age=" + age(t.Age), "ticks=" + fmt.Sprint(t.Ticks), "why=" + q("silent past "+age(LoopSilence))}, Remedy: kick})
		case t.Failures > 0:
			add(Line{Thing: Loop, Facts: []string{"tick_age=" + age(t.Age), "ticks=" + fmt.Sprint(t.Ticks), "failures=" + fmt.Sprint(t.Failures), "why=" + q(t.Error)}, Remedy: "nova-sprint log --max 20"})
		default:
			add(Line{Thing: Loop, Up: true, Facts: []string{"tick_age=" + age(t.Age), "ticks=" + fmt.Sprint(t.Ticks)}})
		}
	}

	// 4. the fleet
	if !failed(Fleet) {
		var up, held int
		var down, remedies []string
		for _, mm := range m.Fleet {
			switch {
			case memberDown(mm):
				down = append(down, mm.Name+":"+beatAge(mm.Beaten, mm.Age))
				remedies = append(remedies, "nova-config loop show "+MemberLoop(mm.Name))
			case mm.Status == "held":
				held++
			default:
				up++
			}
		}
		f := []string{"up=" + fmt.Sprint(up), "held=" + fmt.Sprint(held)}
		if len(down) > 0 {
			add(Line{Thing: Fleet, Facts: append(f, "down="+strings.Join(down, ",")), Remedy: strings.Join(remedies, "; ")})
		} else {
			add(Line{Thing: Fleet, Up: true, Facts: append(f, "down=0")})
		}
	}

	// 5. the friends: a DOWN line per friend down, then the count
	if !failed(Friends) {
		var up, held, down int
		for _, fr := range m.Friends {
			switch fr.Status {
			case "down":
				down++
				f := []string{"friend=" + fr.Name, "beat_age=" + beatAge(fr.Beaten, fr.Age), "label=" + Label(fr.Name)}
				switch {
				case fr.Loaded != "":
					f = append(f, "agent="+q(fr.Loaded))
				case m.Server.Self:
					f = append(f, "agent="+q(NotMeasured))
				default:
					f = append(f, "agent="+q("not measured: no launchd on "+host))
				}
				add(Line{Thing: Friends, Facts: f, Remedy: Bootstrap(fr.Name)})
			case "held":
				held++
			default:
				up++
			}
		}
		add(Line{Thing: Friends, Up: down == 0, Facts: []string{"up=" + fmt.Sprint(up), "held=" + fmt.Sprint(held), "down=" + fmt.Sprint(down)},
			Remedy: ifDown(down > 0, "nova-sprint where")})
	}

	// 6. the readers
	if !failed(Readers) {
		rd := m.Readers
		f := []string{"total=" + fmt.Sprint(rd.Total), "up=" + fmt.Sprint(rd.Up), "reading=" + fmt.Sprint(rd.Reading)}
		if rd.Total > 0 && rd.Up == 0 {
			add(Line{Thing: Readers, Facts: append(f, "why="+q("no reader is up")), Remedy: "nova-config loop list | grep reader-"})
		} else {
			add(Line{Thing: Readers, Up: true, Facts: f})
		}
	}

	// 7. the dashboard
	if !failed(Dashboard) {
		d := m.Dashboard
		remedy := "nova-sprint dashboard --listen " + d.Addr
		switch {
		case m.Server.Self:
			add(Line{Thing: Dashboard, Up: true, Facts: []string{"addr=" + d.Addr, "note=" + q(NotMeasured)}})
		case d.Err != "":
			add(Line{Thing: Dashboard, Facts: []string{"addr=" + d.Addr, "why=" + q(d.Err)}, Remedy: remedy})
		case d.Status != 200:
			add(Line{Thing: Dashboard, Facts: []string{"addr=" + d.Addr, "status=" + fmt.Sprint(d.Status)}, Remedy: remedy})
		case d.Build == "":
			add(Line{Thing: Dashboard, Facts: []string{"addr=" + d.Addr, "status=200", "why=" + q("/api/sprint carries no build id")}, Remedy: remedy})
		default:
			add(Line{Thing: Dashboard, Up: true, Facts: []string{"addr=" + d.Addr, "status=200", "build=" + d.Build}})
		}
	}

	// 8. the bus
	if !failed(Bus) {
		b := m.Bus
		switch {
		case b.Addr == "":
			add(Line{Thing: Bus, Up: true, Facts: []string{"redis=none", "note=" + q("not configured: NOVA_BUS_REDIS is not set")}})
		case m.Server.Self:
			add(Line{Thing: Bus, Up: true, Facts: []string{"redis=" + b.Addr, "note=" + q(NotMeasured)}})
		case b.Err != "":
			add(Line{Thing: Bus, Facts: []string{"redis=" + b.Addr, "why=" + q(b.Err)}, Remedy: "redis-cli -h " + hostOf(b.Addr) + " -p " + portOf(b.Addr) + " ping"})
		default:
			add(Line{Thing: Bus, Up: true, Facts: []string{"redis=" + b.Addr}})
		}
	}

	// 9. the inbox: never DOWN; a count over zero is the first thing to read
	if !failed(Inbox) {
		in := m.Inbox
		f := []string{"open=" + fmt.Sprint(in.Open)}
		if in.Open > 0 {
			f = append(f, "oldest="+age(in.Oldest), "next="+q("nova-sprint inbox"))
		}
		add(Line{Thing: Inbox, Up: true, Facts: f})
	}

	// 10. the dev merge queue
	if !failed(Queue) {
		qLine := m.Queue
		entriesStr := "0"
		if len(qLine.Entries) > 0 {
			entriesStr = strings.Join(qLine.Entries, ",")
		}
		switch {
		case m.Server.Self:
			add(Line{Thing: Queue, Up: true, Facts: []string{"note=" + q(NotMeasured)}})
		case len(qLine.ThrownOut) > 0:
			remedy := "gh pr view " + qLine.ThrownOut[0]
			add(Line{
				Thing:  Queue,
				Facts:  []string{"entries=" + entriesStr, "thrown=" + strings.Join(qLine.ThrownOut, ","), "why=" + q("pull request "+strings.Join(qLine.ThrownOut, ",")+" thrown out")},
				Remedy: remedy,
			})
		case len(qLine.Entries) == 0 && qLine.Green > 0:
			add(Line{
				Thing:  Queue,
				Facts:  []string{"entries=0", fmt.Sprintf("green=%d", qLine.Green), "why=" + q("empty queue with open green PRs")},
				Remedy: "gh pr list",
			})
		default:
			facts := []string{"entries=" + entriesStr}
			if qLine.Green > 0 {
				facts = append(facts, fmt.Sprintf("green=%d", qLine.Green))
			}
			add(Line{Thing: Queue, Up: true, Facts: facts})
		}
	}

	// 11. each machine's installed nova-tools version against dev's tip
	if !failed(Versions) {
		v := m.Versions
		switch {
		case m.Server.Self:
			add(Line{Thing: Versions, Up: true, Facts: []string{"note=" + q(NotMeasured)}})
		default:
			var stale []MachineVersionM
			for _, mv := range v.Machines {
				if v.Dev != "" && mv.Version != v.Dev {
					stale = append(stale, mv)
				}
			}
			if len(stale) > 0 {
				var facts []string
				var remedies []string
				for _, sm := range stale {
					remedies = append(remedies, "nova-update apply "+sm.Machine)
				}
				if len(stale) == 1 {
					facts = []string{
						"machine=" + stale[0].Machine,
						"version=" + stale[0].Version,
						"dev=" + v.Dev,
					}
				} else {
					var names []string
					for _, sm := range stale {
						names = append(names, sm.Machine+":"+sm.Version)
					}
					facts = []string{
						"stale=" + strings.Join(names, ","),
						"dev=" + v.Dev,
					}
				}
				add(Line{
					Thing:  Versions,
					Facts:  facts,
					Remedy: strings.Join(remedies, "; "),
				})
			} else {
				dev := v.Dev
				if dev == "" {
					dev = "none"
				}
				facts := []string{"dev=" + dev}
				if len(v.Machines) > 0 {
					facts = append(facts, fmt.Sprintf("fresh=%d", len(v.Machines)))
				}
				add(Line{Thing: Versions, Up: true, Facts: facts})
			}
		}
	}
	return r
}

// Text is the report as printed: one line per Line, then the summary,
// `MACHINERY OK n=<lines>` or `MACHINERY DOWN n=<down> of=<lines>`.
func (r Report) Text() string {
	var b strings.Builder
	for _, l := range r.Lines {
		b.WriteString(l.String())
		b.WriteByte('\n')
	}
	b.WriteString(r.Summary())
	b.WriteByte('\n')
	return b.String()
}

// Summary is the last line.
func (r Report) Summary() string {
	if r.Down == 0 {
		return fmt.Sprintf("%s OK n=%d", Token, len(r.Lines))
	}
	return fmt.Sprintf("%s DOWN n=%d of=%d", Token, r.Down, len(r.Lines))
}

// JSON is the report as --json prints it.
func (r Report) JSON() string {
	if r.Lines == nil {
		r.Lines = []Line{}
	}
	b, _ := json.Marshal(r) // ignored: strings, numbers and times always encode
	return string(b)
}

// String is the line as printed: MACHINERY <thing> OK|DOWN fact... [remedy="<command>"].
func (l Line) String() string {
	s := Token + " " + l.Thing + " "
	if l.Up {
		s += "OK"
	} else {
		s += "DOWN"
	}
	if len(l.Facts) > 0 {
		s += " " + strings.Join(l.Facts, " ")
	}
	if l.Remedy != "" {
		s += " remedy=" + q(l.Remedy)
	}
	return s
}

func ifDown(down bool, remedy string) string {
	if down {
		return remedy
	}
	return ""
}

// q quotes a value with spaces for a one-line fact.
func q(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), `"`, `'`)
	return `"` + s + `"`
}

// age is a duration as the lines say it: whole seconds under a minute, else
// Go's short form without the seconds' fraction.
func age(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}

func beatAge(beaten bool, d time.Duration) string {
	if !beaten {
		return "never"
	}
	return age(d)
}

func hostOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return "6379"
}
