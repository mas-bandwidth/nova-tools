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
// loaded, each clone is behind=0 against its remote's real tip and the beat is
// within its TTL, and repairs what it can: bootstrap an unloaded unit (twice at
// most in one tick), fetch the declared remote and branch and fast-forward to
// exactly the fetched id. What it cannot repair it prints as `wake: down` in
// red. The registry is friends:declared and friend:<f>:wakepath, written only
// by `friend declare` (declare.go); `friend wake-health` reads it, and with
// --repair (and every 10th `bench beat`) runs WakeTick for the friends
// declared on this host.

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/beat"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
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
	FindingFetchFailed   = "fetch-failed" // the bounded fetch failed or timed out: behind is unknown
	FindingBusDiverged   = "bus-diverged" // local ahead of or diverged from the remote: not rewritten
	FindingBeatStale     = "beat-stale"
	FindingUndeclared    = "undeclared" // neither a unit nor human + notify
)

// wakeLoadAttempts is how many bootstraps one tick tries before `wake: down`.
const wakeLoadAttempts = 2

// FetchBound bounds one clone's fetch of its declared remote.
const FetchBound = 20 * time.Second

// red is the table's down colour.
const red, reset = "\x1b[31m", "\x1b[0m"

// WakeDecl is one friend's declared wake path.
type WakeDecl struct {
	Friend       string
	Mode         string // WakeUnit or WakeHuman; anything else is undeclared
	Unit         string // supervisor name of the wake server, e.g. com.nova.loop.wake-serve-johnny
	UnitFile     string // the plist / unit file the bootstrap loads
	Bus          string // the wake server's own bus clone
	BusRemote    string // the remote and branch the bus clone is held to
	BusBranch    string
	KeeperUnit   string // optional: the friend's bus keeper
	KeeperFile   string
	KeeperBus    string // optional: the keeper's checkout
	KeeperRemote string
	KeeperBranch string
	Notify       string // WakeHuman: the owner's channel
	Host         string // the host whose bench repairs this friend
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
	// FetchFF fetches branch from remote into dir within bound, counts the
	// commits HEAD lacks against the fetched id (never @{u}), fast-forwards to
	// exactly that id when behind, and returns the id with behind before and
	// after. A failed or timed-out fetch wraps ErrFetchFailed; a checkout ahead
	// of or diverged from the fetched id wraps ErrBusDiverged and is untouched.
	FetchFF(ctx context.Context, dir, remote, branch string, bound time.Duration) (FetchFF, error)
}

// FetchFF is one clone's fetch: the fetched commit id and HEAD's behind count
// against it before and after the fast-forward.
type FetchFF struct {
	Fetched       string
	Before, After int
}

// The FetchFF failures a tick reports rather than repairs.
var (
	ErrFetchFailed = errors.New("fetch failed")
	ErrBusDiverged = errors.New("checkout ahead of or diverged from the remote")
)

// BeatReader reports whether a friend's beat is within its TTL.
type BeatReader func(ctx context.Context, friend string) (bool, error)

// RedisBeats reads the presence beat: up is its at under beat.Window old
// (#4233: the beat has no TTL, so its existence says nothing).
func RedisBeats(st *store.Store) BeatReader {
	return func(ctx context.Context, friend string) (bool, error) {
		at, err := beat.Read(ctx, st.Client(), friend).Result()
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return beat.Up(at, time.Now()), nil
	}
}

// WakeRepair is one repair call the tick made.
type WakeRepair struct {
	Action string // bootstrap | fetch-ff
	Target string // unit or clone
	Err    string // "" when the call succeeded
}

// WakeHealth is one friend's checked wake path after one tick.
type WakeHealth struct {
	Friend        string
	Mode          string
	Unit          string
	Notify        string
	State         string
	Findings      []string
	Repairs       []WakeRepair
	BehindBefore  int
	Behind        int
	BehindUnknown bool   // a fetch failed: behind prints ?, never 0
	FetchedBus    string // the ids the behind counts were measured against
	FetchedKeeper string
	Attempts      int // bootstrap calls this tick
	BeatLive      bool
	Detail        string
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

	// No repair for a stale beat (#3048 rev 3): a loaded wake unit whose beat
	// is older than its TTL gets no bootstrap, no fetch and no kickstart. It is
	// reported, not guessed at.
	repair := true
	if !h.BeatLive {
		if loaded, err := host.UnitLoaded(ctx, d.Unit); err == nil && loaded {
			repair = false
		}
	}
	if repair {
		h.ensureUnit(ctx, host, d.Unit, d.UnitFile, FindingUnitMissing, FindingNoUnitFile)
		if d.KeeperUnit != "" {
			h.ensureUnit(ctx, host, d.KeeperUnit, d.KeeperFile, FindingKeeperMissing, FindingKeeperNoFile)
		}
		if d.Bus != "" {
			h.FetchedBus = h.ensureCurrent(ctx, host, d.Bus, d.BusRemote, d.BusBranch)
		}
		if d.KeeperBus != "" {
			h.FetchedKeeper = h.ensureCurrent(ctx, host, d.KeeperBus, d.KeeperRemote, d.KeeperBranch)
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
		h.Attempts++
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

// ensureCurrent fetches a clone's declared remote and branch and
// fast-forwards it to exactly the fetched id. It returns the fetched id ("" when
// the fetch failed).
func (h *WakeHealth) ensureCurrent(ctx context.Context, host WakeHost, dir, remote, branch string) string {
	r, err := host.FetchFF(ctx, dir, remote, branch, FetchBound)
	switch {
	case errors.Is(err, ErrFetchFailed):
		h.BehindUnknown = true
		h.down(FindingFetchFailed, dir+": "+err.Error())
		return ""
	case errors.Is(err, ErrBusDiverged):
		h.down(FindingBusDiverged, dir+": "+err.Error())
		return r.Fetched
	}
	h.BehindBefore += r.Before
	if r.Before == 0 && err == nil {
		return r.Fetched
	}
	if r.Before > 0 {
		h.Findings = append(h.Findings, FindingBusBehind)
	}
	rep := WakeRepair{Action: "fetch-ff", Target: dir}
	if err != nil {
		rep.Err = err.Error()
	}
	h.Repairs = append(h.Repairs, rep)
	if err != nil || r.After != 0 {
		h.Behind += r.After
		h.down("", fmt.Sprintf("%s still behind=%d after fetch-ff to %s: %v", dir, r.After, short(r.Fetched), err))
		return r.Fetched
	}
	h.raise(WakeRepaired)
	return r.Fetched
}

// short is a commit id's first eight characters.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
		b.WriteString(" " + h.behindCell())
		if h.BehindBefore > 0 {
			var ids []string
			for _, id := range []string{h.FetchedBus, h.FetchedKeeper} {
				if id != "" {
					ids = append(ids, short(id))
				}
			}
			if len(ids) > 0 {
				b.WriteString(" fetched=" + strings.Join(ids, ","))
			}
		}
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

// behindCell is behind=? when a fetch failed, behind=<before>-><after> after a
// fast-forward, and behind=<n> otherwise.
func (h WakeHealth) behindCell() string {
	switch {
	case h.BehindUnknown:
		return "behind=?"
	case h.BehindBefore != h.Behind:
		return "behind=" + strconv.Itoa(h.BehindBefore) + "->" + strconv.Itoa(h.Behind)
	}
	return "behind=" + strconv.Itoa(h.Behind)
}

// WakeHealthKey is the hash the table reads a friend's wake cell from.
func WakeHealthKey(friend string) string { return "friend:" + friend + ":wakehealth" }

// WakeRecord is one friend's tick result and the state its wakehealth held
// before the tick, so a down that was already down writes no second receipt.
type WakeRecord struct {
	Health WakeHealth
	Prev   string
}

// RecordWake writes one friend's wake cell (see RecordWakes).
func RecordWake(ctx context.Context, st *store.Store, h WakeHealth, prev, actor, idem string) error {
	return RecordWakes(ctx, st, []WakeRecord{{Health: h, Prev: prev}}, actor, idem)
}

// RecordWakes writes every friend's wake cell and one cap:log receipt per
// repair call (kind wake-repair) and per transition into down (kind
// wake-down), in one MULTI. Down on tick after tick writes nothing more.
func RecordWakes(ctx context.Context, st *store.Store, recs []WakeRecord, actor, idem string) error {
	if st == nil {
		return fmt.Errorf("record wake: store is required")
	}
	for _, r := range recs {
		if r.Health.Friend == "" {
			return fmt.Errorf("record wake: friend is required")
		}
	}
	if len(recs) == 0 {
		return nil
	}
	at := strconv.FormatInt(time.Now().UnixMilli(), 10)
	_, err := st.Client().TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, r := range recs {
			h := r.Health
			behind := strconv.Itoa(h.Behind)
			if h.BehindUnknown {
				behind = "?"
			}
			beat := "stale"
			if h.BeatLive {
				beat = "live"
			}
			p.HSet(ctx, WakeHealthKey(h.Friend),
				"state", h.State, "row", h.Row(), "findings", strings.Join(h.Findings, ","),
				"behind", behind, "fetched_bus", h.FetchedBus, "fetched_keeper", h.FetchedKeeper,
				"beat", beat, "attempts", h.Attempts, "at", at)
			receipt := func(kind, reason string) {
				p.XAdd(ctx, &redis.XAddArgs{Stream: "cap:log", MaxLen: 100000, Approx: true,
					Values: []any{"kind", kind, "subject", h.Friend, "reason", reason,
						"actor", actor, "idem", idem, "at", at}})
			}
			for _, rep := range h.Repairs {
				reason := rep.Action + " " + rep.Target
				if rep.Err != "" {
					reason += ": " + rep.Err
				}
				receipt("wake-repair", reason)
			}
			if h.State == WakeDown && r.Prev != WakeDown {
				receipt("wake-down", h.Detail)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record wake: %w", err)
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
	testguard.RefuseHosts(name, args...)
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

// FetchFF is `git fetch --no-tags <remote> <branch>` bounded at bound, then
// behind against FETCH_HEAD's commit id, then `git merge --ff-only <id>` (the
// exact id, never @{u}), then a check that HEAD is that id.
func (e ExecWakeHost) FetchFF(ctx context.Context, dir, remote, branch string, bound time.Duration) (FetchFF, error) {
	var r FetchFF
	if remote == "" || branch == "" {
		return r, fmt.Errorf("%w: %s declares no remote and branch", ErrFetchFailed, dir)
	}
	fctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	if _, err := e.run(fctx, "git", "-C", dir, "fetch", "--quiet", "--no-tags", remote, branch); err != nil {
		return r, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	out, err := e.run(ctx, "git", "-C", dir, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return r, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	r.Fetched = strings.TrimSpace(out)
	count := func(rng string) (int, error) {
		out, err := e.run(ctx, "git", "-C", dir, "rev-list", "--count", rng)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(out))
	}
	if r.Before, err = count("HEAD.." + r.Fetched); err != nil {
		return r, err
	}
	if ahead, err := count(r.Fetched + "..HEAD"); err != nil {
		return r, err
	} else if ahead > 0 {
		r.After = r.Before
		return r, fmt.Errorf("%w: HEAD has %d commits %s lacks", ErrBusDiverged, ahead, short(r.Fetched))
	}
	if r.Before > 0 {
		if _, err := e.run(ctx, "git", "-C", dir, "merge", "--ff-only", "--quiet", r.Fetched); err != nil {
			r.After = r.Before
			return r, err
		}
	}
	head, err := e.run(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return r, err
	}
	if strings.TrimSpace(head) != r.Fetched {
		r.After, _ = count("HEAD.." + r.Fetched)
		return r, fmt.Errorf("HEAD %s is not the fetched %s", short(strings.TrimSpace(head)), short(r.Fetched))
	}
	return r, nil
}

// WakeStaleAfter is how old a wakehealth may be before the read prints
// `wake: ?` rather than carry the old value over.
const WakeStaleAfter = 30 * time.Second

// WakeRepairEvery is how many bench beats pass between two repair ticks (10 s
// at the 1 s beat).
const WakeRepairEvery = 10

// wakeLockMS is the one repairer's lock on friend:<f>:wakerepair.
const wakeLockMS = 30000

// Function names registered by friend_declare.lua.
const (
	FunctionWakeLock   = "ns_friend_wakelock"
	FunctionWakeUnlock = "ns_friend_wakeunlock"
)

// StateUndeclared is a registered friend with no declared wake path.
const StateUndeclared = "undeclared"

// FriendWake is one friend as the registry and its wake cell hold it.
type FriendWake struct {
	Name     string
	Declared bool              // a member of friends:declared
	Path     map[string]string // friend:<f>:wakepath
	Health   map[string]string // friend:<f>:wakehealth
	BeatLive bool              // friend:<f>:beat exists (its TTL is the liveness)
}

// WakeSnapshot is one read of the registry: friends ∪ friends:declared, each
// friend's wakepath, wakehealth and beat, and the store clock.
type WakeSnapshot struct {
	Now         time.Time
	DeclPresent bool // friends:decl exists
	DeclRev     string
	Friends     []FriendWake
}

// ReadWake reads the registry in two pipelined round trips (the names, then
// every friend's three keys); it never scans and never touches a host.
func ReadWake(ctx context.Context, c redis.Cmdable) (WakeSnapshot, error) {
	p := c.Pipeline()
	clock := p.Time(ctx)
	decl := p.HGetAll(ctx, DeclKey)
	reg := p.SMembers(ctx, FriendsKey)
	declared := p.SMembers(ctx, DeclaredKey)
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return WakeSnapshot{}, fmt.Errorf("read wake registry: %w", err)
	}
	s := WakeSnapshot{Now: clock.Val(), DeclPresent: len(decl.Val()) > 0, DeclRev: decl.Val()["rev"]}
	isDecl := map[string]bool{}
	for _, n := range declared.Val() {
		isDecl[n] = true
	}
	seen := map[string]bool{}
	var names []string
	for _, n := range append(reg.Val(), declared.Val()...) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return s, nil
	}
	type cmds struct {
		path, health *redis.MapStringStringCmd
		beat         *redis.StringCmd
	}
	cs := make([]cmds, len(names))
	p = c.Pipeline()
	for i, n := range names {
		cs[i] = cmds{p.HGetAll(ctx, WakePathKey(n)), p.HGetAll(ctx, WakeHealthKey(n)), beat.Read(ctx, p, n)}
	}
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return WakeSnapshot{}, fmt.Errorf("read wake paths: %w", err)
	}
	for i, n := range names {
		s.Friends = append(s.Friends, FriendWake{Name: n, Declared: isDecl[n],
			Path: cs[i].path.Val(), Health: cs[i].health.Val(), BeatLive: beat.UpCmd(cs[i].beat, time.Now())})
	}
	return s, nil
}

// WakeRow is one friend's gate line: the row, and whether the gate names it.
type WakeRow struct {
	Friend string
	Row    string
	Named  bool
	Why    string // down | undeclared | stale, when named
}

// Rows is the read-only view: undeclared friends print `wake: undeclared`,
// human ones `wake: human notify=<ch>` from the registry, and unit ones their
// stored row, or `wake: ?` when no tick wrote one in the last 30 s. The gate
// names down, undeclared and stale.
func (s WakeSnapshot) Rows() []WakeRow {
	out := make([]WakeRow, 0, len(s.Friends))
	for _, f := range s.Friends {
		r := WakeRow{Friend: f.Name}
		at, _ := strconv.ParseInt(f.Health["at"], 10, 64)
		switch {
		case !f.Declared || len(f.Path) == 0:
			r.Row, r.Named, r.Why = red+"wake: undeclared"+reset, true, StateUndeclared
		case f.Path["mode"] == WakeHuman:
			r.Row = "wake: human notify=" + f.Path["notify"]
		case at == 0 || s.Now.Sub(time.UnixMilli(at)) > WakeStaleAfter:
			r.Row, r.Named, r.Why = red+"wake: ?"+reset, true, "stale"
		default:
			r.Row = f.Health["row"]
			if f.Health["state"] == WakeDown {
				r.Named, r.Why = true, WakeDown
			}
		}
		out = append(out, r)
	}
	return out
}

// DeclOf is the WakeDecl a friend's wakepath declares, with the unit names
// and files this host's supervisor uses when the registry leaves them out.
func (f FriendWake) DeclOf(goos, home string) WakeDecl {
	p := f.Path
	d := WakeDecl{Friend: f.Name, Mode: p["mode"], Unit: p["unit"], UnitFile: p["unit_file"],
		Bus: p["bus"], BusRemote: p["bus_remote"], BusBranch: p["bus_branch"],
		KeeperUnit: p["keeper_unit"], KeeperFile: p["keeper_file"], KeeperBus: p["keeper_bus"],
		KeeperRemote: p["keeper_remote"], KeeperBranch: p["keeper_branch"],
		Notify: p["notify"], Host: p["host"]}
	if d.Mode == WakeUnit && d.Unit == "" {
		d.Unit = WakeServeUnit(f.Name, goos)
	}
	if d.Unit != "" && d.UnitFile == "" {
		d.UnitFile = UnitFile(d.Unit, goos, home)
	}
	if d.KeeperUnit != "" && d.KeeperFile == "" {
		d.KeeperFile = UnitFile(d.KeeperUnit, goos, home)
	}
	return d
}

// UnitFile is where fleet/loops.yml writes a unit: a LaunchAgent plist on
// darwin, a systemd user unit elsewhere.
func UnitFile(unit, goos, home string) string {
	if goos == "darwin" {
		return home + "/Library/LaunchAgents/" + unit + ".plist"
	}
	if !strings.HasSuffix(unit, ".service") {
		unit += ".service"
	}
	return home + "/.config/systemd/user/" + unit
}

// ErrOtherHost refuses a repair of a friend declared on another host.
var ErrOtherHost = errors.New("declared on another host")

// RepairRequest is one repair tick on one host.
type RepairRequest struct {
	Host    string // this host; only friends declared here are repaired
	Only    string // one friend, or "" for every friend declared here
	Session string // the repairer's lock identity
	GOOS    string // "" is runtime.GOOS
	Home    string // "" is os.UserHomeDir
	Actor   string
}

// RepairResult is one repair tick's outcome.
type RepairResult struct {
	Checked []WakeHealth
	Held    map[string]string // friend -> the other session holding its lock
}

// RepairWake runs WakeTick for the friends declared on req.Host: it reads the
// registry, takes each friend's repair lock (a friend another session holds
// is skipped), checks and repairs through host, writes every wake cell in one
// MULTI and releases the locks. A named friend declared elsewhere is refused.
func RepairWake(ctx context.Context, st *store.Store, host WakeHost, req RepairRequest) (RepairResult, error) {
	res := RepairResult{Held: map[string]string{}}
	if req.Host == "" || req.Session == "" {
		return res, fmt.Errorf("wake repair: host and session are required")
	}
	goos, home := req.GOOS, req.Home
	if goos == "" {
		goos = runtime.GOOS
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	snap, err := ReadWake(ctx, st.Client())
	if err != nil {
		return res, err
	}
	var mine []FriendWake
	for _, f := range snap.Friends {
		if req.Only != "" && f.Name != req.Only {
			continue
		}
		if !f.Declared || f.Path["mode"] != WakeUnit {
			continue
		}
		if f.Path["host"] != req.Host {
			if req.Only != "" {
				return res, fmt.Errorf("%w: %s is declared on %s, not %s; run the repair there", ErrOtherHost, f.Name, f.Path["host"], req.Host)
			}
			continue
		}
		mine = append(mine, f)
	}
	if len(mine) == 0 {
		return res, nil
	}
	p := st.Client().Pipeline()
	locks := make([]*redis.Cmd, len(mine))
	for i, f := range mine {
		locks[i] = p.FCall(ctx, FunctionWakeLock, nil, f.Name, req.Session, wakeLockMS)
	}
	if _, err := p.Exec(ctx); err != nil {
		return res, fmt.Errorf("wake repair lock: %w", err)
	}
	var recs []WakeRecord
	var held []string
	for i, f := range mine {
		v, err := locks[i].StringSlice()
		if err != nil || len(v) < 2 {
			return res, fmt.Errorf("wake repair lock %s: %v %q", f.Name, err, v)
		}
		if v[0] != "OK" {
			res.Held[f.Name] = v[1]
			continue
		}
		held = append(held, f.Name)
		live := f.BeatLive
		h := CheckWake(ctx, host, func(context.Context, string) (bool, error) { return live, nil }, f.DeclOf(goos, home))
		res.Checked = append(res.Checked, h)
		recs = append(recs, WakeRecord{Health: h, Prev: f.Health["state"]})
	}
	err = RecordWakes(ctx, st, recs, req.Actor, "")
	p = st.Client().Pipeline()
	for _, n := range held {
		p.FCall(ctx, FunctionWakeUnlock, nil, n, req.Session)
	}
	if len(held) > 0 {
		if _, uerr := p.Exec(ctx); uerr != nil && err == nil {
			err = fmt.Errorf("wake repair unlock: %w", uerr)
		}
	}
	return res, err
}
