package life

// Wake-unit health (#3048, #3033 mechanism 2): the friend's bus tie is a
// checked and repaired unit. On 2026-09-23 com.nova.loop.wake-serve-johnny's
// plist existed on the Studio but was not loaded for about six hours, and
// nothing noticed; Stella idled 11 h with no wake path at all. So every friend
// declares one (rowan-tools fleet/group_vars/all.yml `friends:`):
//
//	wake: unit   a supervised wake server (launchd label / systemd unit), its
//	             own bus clone, optionally a bus keeper and its checkout
//	wake: human  no wake server exists for the harness; Notify is the owner's
//	             channel for waking it by hand
//
// and one sprint tick, before it prints a friend's row, verifies the unit is
// loaded, each clone is behind=0 and the beat is within its TTL, and repairs
// what it can: bootstrap an unloaded unit (twice at most in one tick), pull a
// clone that is behind. What it cannot repair it prints as `wake: down` in red.
// rowan-tools bin/bench-conform holds the same registry to the same rule and
// exits 1 on a friend whose unit is not loaded or who declares neither mode.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Declared wake modes.
const (
	WakeUnit  = "unit"
	WakeHuman = "human"
)

// Wake states, worst last: a friend's state is the worst of its checks.
const (
	WakeOK         = "ok"
	WakeHumanState = "human"
	WakeRepaired   = "repaired"
	WakeDown       = "down"
)

// Findings, printed on the row as wake:<finding>.
const (
	FindingUnitMissing   = "unit-missing"   // unit file present, supervisor does not have it
	FindingNoUnitFile    = "no-unit-file"   // nothing to bootstrap from
	FindingKeeperMissing = "keeper-missing" // keeper unit file present, not loaded
	FindingKeeperNoFile  = "keeper-no-unit-file"
	FindingBusBehind     = "bus-behind"
	FindingBusUnknown    = "bus-unknown" // behind could not be measured
	FindingBeatStale     = "beat-stale"
	FindingUndeclared    = "undeclared" // neither a unit nor human + notify
)

// wakeLoadAttempts is how many bootstraps one tick tries before `wake: down`.
const wakeLoadAttempts = 2

// red is the table's down colour.
const red, reset = "\x1b[31m", "\x1b[0m"

// WakeDecl is one friend's declared wake path.
type WakeDecl struct {
	Friend     string
	Mode       string // WakeUnit or WakeHuman; anything else is undeclared
	Unit       string // supervisor name of the wake server, e.g. com.nova.loop.wake-serve-johnny
	UnitFile   string // the plist / unit file the bootstrap loads
	Bus        string // the wake server's own bus clone
	KeeperUnit string // optional: the friend's bus keeper
	KeeperFile string
	KeeperBus  string // optional: the keeper's checkout
	Notify     string // WakeHuman: the owner's channel
}

// WakeServeUnit is the declared supervisor name of a friend's wake server:
// the launchd label on darwin, the systemd unit elsewhere (rowan-tools
// fleet/loops.yml renders both from the same registry row).
func WakeServeUnit(friend, goos string) string {
	if goos == "darwin" {
		return "com.nova.loop.wake-serve-" + friend
	}
	return "nova-loop-wake-serve-" + friend + ".service"
}

// WakeHost is the seam to the bench's supervisor and git. ExecWakeHost is
// the production one; controls pass a fake.
type WakeHost interface {
	UnitLoaded(ctx context.Context, unit string) (bool, error)
	UnitFileExists(ctx context.Context, file string) (bool, error)
	LoadUnit(ctx context.Context, unit, file string) error
	Behind(ctx context.Context, dir string) (int, error)
	Pull(ctx context.Context, dir string) error
}

// BeatReader reports whether a friend's beat is within its TTL.
type BeatReader func(ctx context.Context, friend string) (bool, error)

// RedisBeats reads the presence beat presence.lua keeps with a TTL: the key
// exists exactly while the beat is within it.
func RedisBeats(st *store.Store) BeatReader {
	return func(ctx context.Context, friend string) (bool, error) {
		n, err := st.Client().Exists(ctx, "friend:"+friend+":beat").Result()
		return n == 1, err
	}
}

// WakeRepair is one repair call the tick made.
type WakeRepair struct {
	Action string // bootstrap | pull
	Target string // unit or clone
	Err    string // "" when the call succeeded
}

// WakeHealth is one friend's checked wake path after one tick.
type WakeHealth struct {
	Friend       string
	Mode         string
	Unit         string
	Notify       string
	State        string
	Findings     []string
	Repairs      []WakeRepair
	BehindBefore int
	Behind       int
	BeatLive     bool
	Detail       string
}

// WakeTick checks and repairs every declared friend once, in order.
func WakeTick(ctx context.Context, host WakeHost, beats BeatReader, decls []WakeDecl) []WakeHealth {
	out := make([]WakeHealth, 0, len(decls))
	for _, d := range decls {
		out = append(out, CheckWake(ctx, host, beats, d))
	}
	return out
}

// CheckWake checks one friend's wake path and repairs what it can.
func CheckWake(ctx context.Context, host WakeHost, beats BeatReader, d WakeDecl) WakeHealth {
	h := WakeHealth{Friend: d.Friend, Mode: d.Mode, Unit: d.Unit, Notify: d.Notify, State: WakeOK}
	if beats != nil {
		live, err := beats(ctx, d.Friend)
		h.BeatLive = err == nil && live
	}
	if !h.BeatLive {
		h.Findings = append(h.Findings, FindingBeatStale)
	}
	switch {
	case d.Mode == WakeHuman && d.Notify != "":
		h.State = WakeHumanState
		return h
	case d.Mode == WakeHuman:
		h.down(FindingUndeclared, "wake: human with no notify channel")
		return h
	case d.Mode != WakeUnit || d.Unit == "":
		h.down(FindingUndeclared, "neither a wake unit nor wake: human + notify")
		return h
	}

	h.ensureUnit(ctx, host, d.Unit, d.UnitFile, FindingUnitMissing, FindingNoUnitFile)
	if d.KeeperUnit != "" {
		h.ensureUnit(ctx, host, d.KeeperUnit, d.KeeperFile, FindingKeeperMissing, FindingKeeperNoFile)
	}
	for _, dir := range []string{d.Bus, d.KeeperBus} {
		if dir != "" {
			h.ensureCurrent(ctx, host, dir)
		}
	}
	// A beat older than its TTL is a dead wake path even with the unit loaded
	// and every clone current (#3134 hold, Stella): the server is not beating.
	// The one exception is a wake unit this tick just bootstrapped, whose
	// server cannot have beaten yet; the next tick holds it to the TTL.
	if !h.BeatLive && !h.bootstrapped(d.Unit) {
		h.down("", "beat older than its TTL")
	}
	return h
}

// bootstrapped reports whether this tick loaded unit cleanly.
func (h *WakeHealth) bootstrapped(unit string) bool {
	for _, r := range h.Repairs {
		if r.Action == "bootstrap" && r.Target == unit && r.Err == "" {
			return true
		}
	}
	return false
}

// ensureUnit bootstraps a unit the supervisor does not have, at most
// wakeLoadAttempts times, and re-reads the supervisor after each attempt.
func (h *WakeHealth) ensureUnit(ctx context.Context, host WakeHost, unit, file, missing, nofile string) {
	if loaded, err := host.UnitLoaded(ctx, unit); err == nil && loaded {
		return
	}
	if ok, _ := host.UnitFileExists(ctx, file); !ok {
		h.down(nofile, unit+": no unit file at "+file)
		return
	}
	h.Findings = append(h.Findings, missing)
	var last error
	for i := 0; i < wakeLoadAttempts; i++ {
		last = host.LoadUnit(ctx, unit, file)
		r := WakeRepair{Action: "bootstrap", Target: unit}
		if last == nil {
			if loaded, err := host.UnitLoaded(ctx, unit); err != nil || !loaded {
				last = fmt.Errorf("bootstrap returned but the supervisor does not have %s", unit)
			}
		}
		if last != nil {
			r.Err = last.Error()
		}
		h.Repairs = append(h.Repairs, r)
		if last == nil {
			h.raise(WakeRepaired)
			return
		}
	}
	h.down("", fmt.Sprintf("%s failed to load %d times: %v", unit, wakeLoadAttempts, last))
}

// ensureCurrent pulls a clone that is behind its upstream.
func (h *WakeHealth) ensureCurrent(ctx context.Context, host WakeHost, dir string) {
	n, err := host.Behind(ctx, dir)
	if err != nil {
		h.down(FindingBusUnknown, dir+": "+err.Error())
		return
	}
	h.BehindBefore += n
	if n == 0 {
		return
	}
	h.Findings = append(h.Findings, FindingBusBehind)
	err = host.Pull(ctx, dir)
	r := WakeRepair{Action: "pull", Target: dir}
	if err != nil {
		r.Err = err.Error()
	}
	h.Repairs = append(h.Repairs, r)
	after, aerr := host.Behind(ctx, dir)
	if err != nil || aerr != nil || after != 0 {
		h.Behind += after
		h.down("", fmt.Sprintf("%s still behind=%d after pull: %v", dir, after, errors.Join(err, aerr)))
		return
	}
	h.raise(WakeRepaired)
}

func (h *WakeHealth) raise(state string) {
	rank := map[string]int{WakeOK: 0, WakeHumanState: 1, WakeRepaired: 2, WakeDown: 3}
	if rank[state] > rank[h.State] {
		h.State = state
	}
}

func (h *WakeHealth) down(finding, detail string) {
	if finding != "" {
		h.Findings = append(h.Findings, finding)
	}
	if detail != "" {
		if h.Detail != "" {
			h.Detail += "; "
		}
		h.Detail += detail
	}
	h.raise(WakeDown)
}

// Row is the friend's wake cell on the sprint table: the state first
// (`wake: down` in red), then each finding as wake:<finding>, the clones'
// behind count and the beat.
func (h WakeHealth) Row() string {
	var b strings.Builder
	switch h.State {
	case WakeOK:
		b.WriteString("wake: ok " + h.Unit)
	case WakeHumanState:
		b.WriteString("wake: human notify=" + h.Notify)
	case WakeRepaired:
		var targets []string
		for _, r := range h.Repairs {
			if r.Err == "" {
				targets = append(targets, r.Target)
			}
		}
		b.WriteString("wake: repaired " + strings.Join(targets, ","))
	default:
		b.WriteString(red + "wake: down" + reset)
		if h.Unit != "" {
			b.WriteString(" " + h.Unit)
		}
	}
	for _, f := range h.Findings {
		if f != FindingBeatStale {
			b.WriteString(" wake:" + f)
		}
	}
	if h.Mode == WakeUnit {
		b.WriteString(" behind=" + strconv.Itoa(h.Behind))
	}
	if h.BeatLive {
		b.WriteString(" beat=live")
	} else {
		b.WriteString(" beat=stale")
	}
	if h.Detail != "" {
		b.WriteString(" (" + h.Detail + ")")
	}
	return b.String()
}

// WakeHealthKey is the hash the table reads a friend's wake cell from.
func WakeHealthKey(friend string) string { return "friend:" + friend + ":wakehealth" }

// RecordWake writes the friend's wake cell and one cap:log receipt per repair
// call (kind wake-repair) and per down (kind wake-down), in one MULTI.
func RecordWake(ctx context.Context, st *store.Store, h WakeHealth, actor, idem string) error {
	if st == nil || h.Friend == "" {
		return fmt.Errorf("record wake: store and friend are required")
	}
	at := strconv.FormatInt(time.Now().UnixMilli(), 10)
	_, err := st.Client().TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, WakeHealthKey(h.Friend),
			"state", h.State, "row", h.Row(), "findings", strings.Join(h.Findings, ","),
			"behind", h.Behind, "beat", strconv.FormatBool(h.BeatLive), "at", at)
		receipt := func(kind, reason string) {
			p.XAdd(ctx, &redis.XAddArgs{Stream: "cap:log", MaxLen: 100000, Approx: true,
				Values: []any{"kind", kind, "subject", h.Friend, "reason", reason,
					"actor", actor, "idem", idem, "at", at}})
		}
		for _, r := range h.Repairs {
			reason := r.Action + " " + r.Target
			if r.Err != "" {
				reason += ": " + r.Err
			}
			receipt("wake-repair", reason)
		}
		if h.State == WakeDown {
			receipt("wake-down", h.Detail)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record wake %s: %w", h.Friend, err)
	}
	return nil
}

// ExecWakeHost is the production WakeHost: launchctl on darwin, systemctl
// --user elsewhere, git for the clones. It runs only on the bench it checks.
type ExecWakeHost struct {
	GOOS string // "" is runtime.GOOS
	UID  int    // 0 is os.Getuid()
}

func (e ExecWakeHost) goos() string {
	if e.GOOS != "" {
		return e.GOOS
	}
	return runtime.GOOS
}

func (e ExecWakeHost) domain() string {
	uid := e.UID
	if uid == 0 {
		uid = os.Getuid()
	}
	return "gui/" + strconv.Itoa(uid)
}

func (e ExecWakeHost) run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// UnitLoaded asks the supervisor. A nonzero `launchctl print` / `systemctl
// is-active` is "not loaded", not an error.
func (e ExecWakeHost) UnitLoaded(ctx context.Context, unit string) (bool, error) {
	if e.goos() == "darwin" {
		if _, err := e.run(ctx, "launchctl", "print", e.domain()+"/"+unit); err == nil {
			return true, nil
		}
		_, err := e.run(ctx, "launchctl", "print", "system/"+unit)
		return err == nil, nil
	}
	_, err := e.run(ctx, "systemctl", "--user", "is-active", "--quiet", unit)
	return err == nil, nil
}

// UnitFileExists reports whether the unit file is on disk.
func (e ExecWakeHost) UnitFileExists(_ context.Context, file string) (bool, error) {
	if file == "" {
		return false, nil
	}
	_, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// LoadUnit bootstraps (darwin) or enables and starts (systemd) the unit.
func (e ExecWakeHost) LoadUnit(ctx context.Context, unit, file string) error {
	if e.goos() == "darwin" {
		_, err := e.run(ctx, "launchctl", "bootstrap", e.domain(), file)
		return err
	}
	if _, err := e.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	_, err := e.run(ctx, "systemctl", "--user", "enable", "--now", unit)
	return err
}

// Behind fetches the clone's upstream and counts the commits HEAD lacks.
func (e ExecWakeHost) Behind(ctx context.Context, dir string) (int, error) {
	if _, err := e.run(ctx, "git", "-C", dir, "fetch", "--quiet"); err != nil {
		return 0, err
	}
	out, err := e.run(ctx, "git", "-C", dir, "rev-list", "--count", "HEAD..@{u}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Pull fast-forwards the clone onto its upstream; it never merges or resets.
func (e ExecWakeHost) Pull(ctx context.Context, dir string) error {
	_, err := e.run(ctx, "git", "-C", dir, "merge", "--ff-only", "--quiet", "@{u}")
	return err
}
