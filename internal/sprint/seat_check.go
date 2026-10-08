// Package sprint implements the core coordination rules and data structures
// for nova-sprint (docs/SPEC-SPRINT.md).
package sprint

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The seat check (docs/SPEC-SPRINT.md, "The seat check"; the owner, 2026-10-04 1:52 PM:
// "what is manual needs a verb: nova-sprint seat check"):
// measures the machinery under the sprint and prints one line per check, up or
// DOWN with the remedy, a command, on the DOWN line.
// Checks: server, store, loop, beats (fleet and friends), readers, dashboard,
// installed versions, merge queue; exits 0 when all are OK, 1 on any red/DOWN check.

// The check names in order of output.
const (
	SeatCheckServer    = "server"
	SeatCheckStore     = "store"
	SeatCheckLoop      = "loop"
	SeatCheckFleet     = "fleet"
	SeatCheckFriends   = "friends"
	SeatCheckReaders   = "readers"
	SeatCheckDashboard = "dashboard"
	SeatCheckBus       = "bus"
	SeatCheckInbox     = "inbox"
	SeatCheckQueue     = "queue"
	SeatCheckVersions  = "versions"
	SeatCheckPush      = "push"
)

// SeatCheckToken is the first word of every line.
const SeatCheckToken = "MACHINERY"

// ServerM is the sprint's server as measured: the address the environment
// names (NOVA_SPRINT_SERVER), Self when the measure ran in the server's own
// process, the error of one verb round trip, its time, and the pid of a
// listener on the address when local (0 unknown).
type ServerM struct {
	Addr   string `json:"addr"`
	Self   bool   `json:"self,omitempty"`
	Err    string `json:"err,omitempty"`
	Millis int64  `json:"ms"`
	PID    int    `json:"pid,omitempty"`
}

// StoreM is the store as measured: the Redis the server uses, the error of a
// ping, DBSize (-1 when none, the mem twin), the machine's state word, the
// epoch and the last tick.
type StoreM struct {
	Addr    string `json:"addr"`
	Err     string `json:"err,omitempty"`
	DBSize  int64  `json:"dbsize"`
	Machine string `json:"machine"`
	Epoch   uint64 `json:"epoch"`
	Tick    TickM  `json:"tick"`
}

// TickM is the run loop's own report of its last tick, the heartbeat record it
// writes: whether it ever ticked, how long since it was last seen, how many
// ticks, and the last failure.
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

// FriendM is one friend: her status as the friends table shows it and her last
// beat's age.
type FriendM struct {
	Name   string        `json:"name"`
	Status string        `json:"status"`
	Beaten bool          `json:"beaten"`
	Age    time.Duration `json:"age_ns"`
}

// ReadersM is the readers table: how many readers, how many up, and how many
// reads are being read now.
type ReadersM struct {
	Total   int `json:"total"`
	Up      int `json:"up"`
	Reading int `json:"reading"`
}

// DashM is the local dashboard: address, HTTP status of GET /api/sprint
// (0 when request failed, Err says how) and build id the body carries.
type DashM struct {
	Addr   string `json:"addr"`
	Status int    `json:"status"`
	Err    string `json:"err,omitempty"`
	Build  string `json:"build,omitempty"`
}

// BusM is nova-bus2's store: address NOVA_BUS_REDIS names ("" is not
// configured) and error of a ping.
type BusM struct {
	Addr string `json:"addr,omitempty"`
	Err  string `json:"err,omitempty"`
}

// InboxM is the coordinator's inbox: open judgments and oldest wait.
type InboxM struct {
	Open   int           `json:"open"`
	Oldest time.Duration `json:"oldest_ns"`
}

// QueueM is the merge queue: entries, thrown out pull requests, and green PR count.
type QueueM struct {
	Measured  bool     `json:"measured,omitempty"`
	Entries   []string `json:"entries,omitempty"`
	ThrownOut []string `json:"thrown_out,omitempty"`
	Green     int      `json:"green,omitempty"`
}

// MachineVersionM is one machine's installed version of nova-tools.
type MachineVersionM struct {
	Machine string `json:"machine"`
	Version string `json:"version"`
}

// VersionsM is each machine's installed version against dev's tip.
type VersionsM struct {
	Measured bool              `json:"measured,omitempty"`
	Dev      string            `json:"dev,omitempty"`
	Machines []MachineVersionM `json:"machines,omitempty"`
}

// PushM is the seat's push proof (pushproof.go): the holder and its push
// record, as read; Measured false is a check that did not read it, which says
// no line.
type PushM struct {
	Measured bool       `json:"measured,omitempty"`
	Holder   string     `json:"holder,omitempty"`
	Record   PushRecord `json:"record,omitzero"`
	Recorded bool       `json:"recorded,omitempty"`
}

// SeatCheckMeasures is everything the probes measured, in one struct so a
// test or command can evaluate any state of the machinery.
type SeatCheckMeasures struct {
	Server    ServerM           `json:"server"`
	Store     StoreM            `json:"store"`
	Fleet     []MemberM         `json:"fleet"`
	Friends   []FriendM         `json:"friends"`
	Readers   ReadersM          `json:"readers"`
	Dashboard DashM             `json:"dashboard"`
	Bus       BusM              `json:"bus"`
	Inbox     InboxM            `json:"inbox"`
	Queue     QueueM            `json:"queue,omitempty"`
	Versions  VersionsM         `json:"versions,omitempty"`
	Push      PushM             `json:"push,omitzero"`
	Host      string            `json:"host"`
	Errs      map[string]string `json:"errs,omitempty"`
	// Policy is the policy numbers nova-config applied: loop_silence and member_down_after
	// (policy.go); nil is their defaults.
	Policy PolicyValues `json:"policy,omitempty"`
}

// SeatCheckLine is one line of the check: the thing, up or DOWN, its facts in
// order, and the remedy, a command, on a DOWN line.
type SeatCheckLine struct {
	Thing  string   `json:"thing"`
	Up     bool     `json:"up"`
	Facts  []string `json:"facts"`
	Remedy string   `json:"remedy,omitempty"`
}

// String is the line as printed: MACHINERY <thing> OK|DOWN fact... [remedy="<command>"].
func (l SeatCheckLine) String() string {
	s := SeatCheckToken + " " + l.Thing + " "
	if l.Up {
		s += "OK"
	} else {
		s += "DOWN"
	}
	if len(l.Facts) > 0 {
		s += " " + strings.Join(l.Facts, " ")
	}
	if l.Remedy != "" {
		s += " remedy=" + quoteSeatCheck(l.Remedy)
	}
	return s
}

// SeatCheckReport is the check report: the lines in order, how many are DOWN,
// and the process exit code (0 all up, 1 on any DOWN line).
type SeatCheckReport struct {
	At       time.Time         `json:"at"`
	Lines    []SeatCheckLine   `json:"lines"`
	Down     int               `json:"down"`
	ExitCode int               `json:"exit_code"`
	Measures SeatCheckMeasures `json:"measures"`
}

// Text is the report as printed: one line per check, then the summary line.
func (r SeatCheckReport) Text() string {
	var b strings.Builder
	for _, l := range r.Lines {
		b.WriteString(l.String())
		b.WriteByte('\n')
	}
	b.WriteString(r.Summary())
	b.WriteByte('\n')
	return b.String()
}

// Summary is the last line: MACHINERY OK n=<lines> or MACHINERY DOWN n=<down> of=<lines>.
func (r SeatCheckReport) Summary() string {
	if r.Down == 0 {
		return fmt.Sprintf("%s OK n=%d", SeatCheckToken, len(r.Lines))
	}
	return fmt.Sprintf("%s DOWN n=%d of=%d", SeatCheckToken, r.Down, len(r.Lines))
}

// JSON is the report as --json prints it.
func (r SeatCheckReport) JSON() string {
	// Measurements are public too; only the folder reveals the proof nonce.
	r.Measures.Push.Record = PublicPushRecord(r.Measures.Push.Record)
	if r.Lines == nil {
		r.Lines = []SeatCheckLine{}
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// LoopSilence is how long the run loop goes unseen before DOWN (docs/SPEC-SPRINT.md).
const LoopSilence = 15 * time.Second

// MemberDownAfter is how long a fleet member goes without a beat before DOWN.
const MemberDownAfter = 3 * 15 * time.Second

func memberIsDown(m MemberM, after time.Duration) bool {
	return m.Status == "down" || (m.Status != "held" && (!m.Beaten || m.Age > after))
}

// NotMeasured is what the dashboard, bus and friend agents say when the check
// ran inside the server (Server.Self).
const NotMeasured = "not measured: the check ran in the server; run nova-sprint seat check"

// FriendLabel is the launchd label that beats the friend.
func FriendLabel(friend string) string { return "com.nova.loop.friend-beat-" + friend }

// FriendBootstrap is the command that loads the friend's agent.
func FriendBootstrap(friend string) string {
	return "launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/" + FriendLabel(friend) + ".plist"
}

// MemberLoopRecord is the loop record that beats the member.
func MemberLoopRecord(member string) string { return "member-" + member }

// JudgeSeatCheck turns the measures into a SeatCheckReport according to docs/SPEC-SPRINT.md.
func JudgeSeatCheck(m SeatCheckMeasures, now time.Time) SeatCheckReport {
	r := SeatCheckReport{At: now, Measures: m}
	policy := &Snapshot{Policy: m.Policy}
	silence, downAfter := policy.PolicyDuration(PolicyLoopSilence), policy.PolicyDuration(PolicyMemberDownAfter)
	add := func(l SeatCheckLine) {
		if !l.Up {
			r.Down++
		}
		r.Lines = append(r.Lines, l)
	}
	failed := func(thing string) bool {
		e, ok := m.Errs[thing]
		if ok {
			add(SeatCheckLine{Thing: thing, Facts: []string{"err=" + quoteSeatCheck(e)}, Remedy: "nova-sprint where"})
		}
		return ok
	}
	host := m.Host
	if host == "" {
		host = "<host>"
	}
	serverUnit := "com.nova.loop.sprint-server-" + host

	// 1. the server
	if !failed(SeatCheckServer) {
		s := m.Server
		switch {
		case s.Self:
			add(SeatCheckLine{Thing: SeatCheckServer, Up: true, Facts: []string{"addr=" + s.Addr, "self=true", "pid=" + fmt.Sprint(s.PID)}})
		case s.Addr == "":
			add(SeatCheckLine{
				Thing:  SeatCheckServer,
				Facts:  []string{"addr=none", "why=" + quoteSeatCheck("NOVA_SPRINT_SERVER is not set")},
				Remedy: "export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-" + host + " | sed -n 's/.*--listen [^:]*:\\([0-9]*\\).*/\\1/p')",
			})
		case s.Err != "":
			add(SeatCheckLine{
				Thing:  SeatCheckServer,
				Facts:  []string{"addr=" + s.Addr, "why=" + quoteSeatCheck(s.Err)},
				Remedy: "launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/" + serverUnit + ".plist",
			})
		default:
			f := []string{"addr=" + s.Addr, "ms=" + fmt.Sprint(s.Millis)}
			if s.PID > 0 {
				f = append(f, "pid="+fmt.Sprint(s.PID))
			}
			add(SeatCheckLine{Thing: SeatCheckServer, Up: true, Facts: f})
		}
	}

	// 2. the store
	st := m.Store
	if !failed(SeatCheckStore) {
		if st.Err != "" {
			var remedy string
			if strings.HasPrefix(st.Addr, "mem:") || st.Addr == "mem" {
				remedy = "nova-sprint where --redis " + st.Addr
			} else {
				remedy = "redis-cli -h " + hostOf(st.Addr) + " -p " + portOf(st.Addr) + " ping"
			}
			add(SeatCheckLine{
				Thing:  SeatCheckStore,
				Facts:  []string{"redis=" + st.Addr, "why=" + quoteSeatCheck(st.Err)},
				Remedy: remedy,
			})
		} else {
			size := "-"
			if st.DBSize >= 0 {
				size = fmt.Sprint(st.DBSize)
			}
			add(SeatCheckLine{
				Thing: SeatCheckStore,
				Up:    true,
				Facts: []string{"redis=" + st.Addr, "dbsize=" + size, "machine=" + st.Machine, "epoch=" + fmt.Sprint(st.Epoch)},
			})
		}
	}

	// 3. the run loop
	if !failed(SeatCheckLoop) {
		t := st.Tick
		kick := "launchctl kickstart -k gui/$(id -u)/" + serverUnit
		switch {
		case st.Err != "":
			add(SeatCheckLine{Thing: SeatCheckLoop, Facts: []string{"why=" + quoteSeatCheck("the store did not answer: the heartbeat was not read")}, Remedy: kick})
		case !t.Ticked:
			add(SeatCheckLine{Thing: SeatCheckLoop, Facts: []string{"tick=never", "why=" + quoteSeatCheck("no heartbeat: run --listen has not ticked this store")}, Remedy: kick})
		case t.Age > silence:
			add(SeatCheckLine{Thing: SeatCheckLoop, Facts: []string{"tick_age=" + formatAge(t.Age), "ticks=" + fmt.Sprint(t.Ticks), "why=" + quoteSeatCheck("silent past "+formatAge(silence))}, Remedy: kick})
		case t.Failures > 0:
			add(SeatCheckLine{Thing: SeatCheckLoop, Facts: []string{"tick_age=" + formatAge(t.Age), "ticks=" + fmt.Sprint(t.Ticks), "failures=" + fmt.Sprint(t.Failures), "why=" + quoteSeatCheck(t.Error)}, Remedy: "nova-sprint log --max 20"})
		default:
			add(SeatCheckLine{Thing: SeatCheckLoop, Up: true, Facts: []string{"tick_age=" + formatAge(t.Age), "ticks=" + fmt.Sprint(t.Ticks)}})
		}
	}

	// 4. the fleet (beats)
	if !failed(SeatCheckFleet) {
		var up, held int
		var down, remedies []string
		for _, mm := range m.Fleet {
			switch {
			case memberIsDown(mm, downAfter):
				down = append(down, mm.Name+":"+formatBeatAge(mm.Beaten, mm.Age))
				remedies = append(remedies, "nova-config loop show "+MemberLoopRecord(mm.Name))
			case mm.Status == "held":
				held++
			default:
				up++
			}
		}
		f := []string{"up=" + fmt.Sprint(up), "held=" + fmt.Sprint(held)}
		if len(down) > 0 {
			add(SeatCheckLine{Thing: SeatCheckFleet, Facts: append(f, "down="+strings.Join(down, ",")), Remedy: strings.Join(remedies, "; ")})
		} else {
			add(SeatCheckLine{Thing: SeatCheckFleet, Up: true, Facts: append(f, "down=0")})
		}
	}

	// 5. friends (beats)
	if !failed(SeatCheckFriends) {
		var up, held int
		var down, remedies []string
		for _, fr := range m.Friends {
			switch fr.Status {
			case "down":
				down = append(down, fr.Name+":"+formatBeatAge(fr.Beaten, fr.Age))
				remedies = append(remedies, FriendBootstrap(fr.Name))
			case "held":
				held++
			default:
				up++
			}
		}
		f := []string{"up=" + fmt.Sprint(up), "held=" + fmt.Sprint(held)}
		if len(down) > 0 {
			add(SeatCheckLine{
				Thing:  SeatCheckFriends,
				Facts:  append(f, "down="+strings.Join(down, ",")),
				Remedy: strings.Join(remedies, "; "),
			})
		} else {
			add(SeatCheckLine{Thing: SeatCheckFriends, Up: true, Facts: append(f, "down=0")})
		}
	}

	// 6. readers
	if !failed(SeatCheckReaders) {
		rd := m.Readers
		f := []string{"total=" + fmt.Sprint(rd.Total), "up=" + fmt.Sprint(rd.Up), "reading=" + fmt.Sprint(rd.Reading)}
		if rd.Total > 0 && rd.Up == 0 {
			add(SeatCheckLine{Thing: SeatCheckReaders, Facts: append(f, "why="+quoteSeatCheck("no reader is up")), Remedy: "nova-config loop list | grep reader-"})
		} else {
			add(SeatCheckLine{Thing: SeatCheckReaders, Up: true, Facts: f})
		}
	}

	// 7. dashboard
	if !failed(SeatCheckDashboard) {
		d := m.Dashboard
		remedy := "nova-sprint dashboard --listen " + d.Addr
		switch {
		case m.Server.Self:
			add(SeatCheckLine{Thing: SeatCheckDashboard, Up: true, Facts: []string{"addr=" + d.Addr, "note=" + quoteSeatCheck(NotMeasured)}})
		case d.Err != "":
			add(SeatCheckLine{Thing: SeatCheckDashboard, Facts: []string{"addr=" + d.Addr, "why=" + quoteSeatCheck(d.Err)}, Remedy: remedy})
		case d.Status != 200:
			add(SeatCheckLine{Thing: SeatCheckDashboard, Facts: []string{"addr=" + d.Addr, "status=" + fmt.Sprint(d.Status)}, Remedy: remedy})
		case d.Build == "":
			add(SeatCheckLine{Thing: SeatCheckDashboard, Facts: []string{"addr=" + d.Addr, "status=200", "why=" + quoteSeatCheck("/api/sprint carries no build id")}, Remedy: remedy})
		default:
			add(SeatCheckLine{Thing: SeatCheckDashboard, Up: true, Facts: []string{"addr=" + d.Addr, "status=200", "build=" + d.Build}})
		}
	}

	// 8. bus
	if !failed(SeatCheckBus) {
		b := m.Bus
		switch {
		case b.Addr == "":
			add(SeatCheckLine{Thing: SeatCheckBus, Up: true, Facts: []string{"redis=none", "note=" + quoteSeatCheck("not configured: NOVA_BUS_REDIS is not set")}})
		case m.Server.Self:
			add(SeatCheckLine{Thing: SeatCheckBus, Up: true, Facts: []string{"redis=" + b.Addr, "note=" + quoteSeatCheck(NotMeasured)}})
		case b.Err != "":
			add(SeatCheckLine{Thing: SeatCheckBus, Facts: []string{"redis=" + b.Addr, "why=" + quoteSeatCheck(b.Err)}, Remedy: "redis-cli -h " + hostOf(b.Addr) + " -p " + portOf(b.Addr) + " ping"})
		default:
			add(SeatCheckLine{Thing: SeatCheckBus, Up: true, Facts: []string{"redis=" + b.Addr}})
		}
	}

	// 9. inbox
	if !failed(SeatCheckInbox) {
		in := m.Inbox
		f := []string{"open=" + fmt.Sprint(in.Open)}
		if in.Open > 0 {
			f = append(f, "oldest="+formatAge(in.Oldest), "next="+quoteSeatCheck("nova-sprint inbox"))
		}
		add(SeatCheckLine{Thing: SeatCheckInbox, Up: true, Facts: f})
	}

	// 10. merge queue
	if !failed(SeatCheckQueue) {
		q := m.Queue
		measured := q.Measured || len(q.Entries) > 0 || len(q.ThrownOut) > 0 || q.Green > 0
		entriesStr := "0"
		if len(q.Entries) > 0 {
			entriesStr = strings.Join(q.Entries, ",")
		}
		switch {
		case !measured:
			note := "not measured: probe not configured"
			if m.Server.Self {
				note = NotMeasured
			}
			add(SeatCheckLine{Thing: SeatCheckQueue, Up: true, Facts: []string{"note=" + quoteSeatCheck(note)}})
		case len(q.ThrownOut) > 0:
			remedy := "gh pr view " + q.ThrownOut[0]
			add(SeatCheckLine{
				Thing:  SeatCheckQueue,
				Facts:  []string{"entries=" + entriesStr, "thrown=" + strings.Join(q.ThrownOut, ","), "why=" + quoteSeatCheck("pull request "+strings.Join(q.ThrownOut, ",")+" thrown out")},
				Remedy: remedy,
			})
		case len(q.Entries) == 0 && q.Green > 0:
			add(SeatCheckLine{
				Thing:  SeatCheckQueue,
				Facts:  []string{"entries=0", fmt.Sprintf("green=%d", q.Green), "why=" + quoteSeatCheck("empty queue with open green PRs")},
				Remedy: "gh pr list",
			})
		default:
			facts := []string{"entries=" + entriesStr}
			if q.Green > 0 {
				facts = append(facts, fmt.Sprintf("green=%d", q.Green))
			}
			add(SeatCheckLine{Thing: SeatCheckQueue, Up: true, Facts: facts})
		}
	}

	// 11. installed versions
	if !failed(SeatCheckVersions) {
		v := m.Versions
		measured := v.Measured || v.Dev != "" || len(v.Machines) > 0
		if !measured {
			note := "not measured: probe not configured"
			if m.Server.Self {
				note = NotMeasured
			}
			add(SeatCheckLine{Thing: SeatCheckVersions, Up: true, Facts: []string{"note=" + quoteSeatCheck(note)}})
		} else {
			var stale []MachineVersionM
			for _, mv := range v.Machines {
				if v.Dev != "" && mv.Version != v.Dev {
					stale = append(stale, mv)
				}
			}
			if len(stale) > 0 {
				var facts []string
				if len(stale) == 1 {
					facts = []string{
						"machine=" + stale[0].Machine,
						"version=" + stale[0].Version,
						"dev=" + v.Dev,
					}
				} else {
					var names []string
					for _, mv := range stale {
						names = append(names, mv.Machine)
					}
					facts = []string{
						"stale=" + strings.Join(names, ","),
						"dev=" + v.Dev,
					}
				}
				add(SeatCheckLine{
					Thing:  SeatCheckVersions,
					Facts:  facts,
					Remedy: "nova-update apply",
				})
			} else {
				facts := []string{}
				if v.Dev != "" {
					facts = append(facts, "dev="+v.Dev)
				}
				facts = append(facts, fmt.Sprintf("machines=%d", len(v.Machines)))
				add(SeatCheckLine{Thing: SeatCheckVersions, Up: true, Facts: facts})
			}
		}
	}

	// 12. the push proof: PUSH DOWN with the setup while the seat has none live
	if p := m.Push; p.Measured && !failed(SeatCheckPush) {
		facts := []string{"holder=" + orDash(p.Holder), "harness=" + orDash(p.Record.Harness), "adapter=" + p.Record.AdapterName()}
		if why := PushWhy(p.Holder, p.Record, p.Recorded, now); why != "" {
			remedy := PushSetup(p.Holder, p.Record, p.Recorded)
			if p.Recorded && p.Record.Adapter == AdapterFolder {
				remedy += "; then, " + FolderSteps(p.Record)
			}
			add(SeatCheckLine{Thing: SeatCheckPush, Facts: append(facts, "proven=-", "why="+quoteSeatCheck(why)), Remedy: remedy})
		} else {
			add(SeatCheckLine{Thing: SeatCheckPush, Up: true, Facts: append(facts, "proven="+formatAge(now.Sub(p.Record.Proven))+" ago")})
		}
	}

	if r.Down > 0 {
		r.ExitCode = 1
	}
	return r
}

func quoteSeatCheck(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), `"`, `'`)
	return `"` + s + `"`
}

func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}

func formatBeatAge(beaten bool, d time.Duration) string {
	if !beaten {
		return "never"
	}
	return formatAge(d)
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
