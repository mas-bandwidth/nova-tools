// `nova-sprint doctor [--redis <addr>] [--bench <name>]` (nova-tools #4352
// item L): "seat, Redis reachable, Lua library sha versus the binary (fn
// check), binary version versus dev tip, runners registered, webhook ingest
// alive, pit stop state; one line each with the exact fix (today those were
// five hand checks)". It prints one line per check,
//
//	DOCTOR <check> OK key=value...
//	DOCTOR <check> FIX key=value... remedy="<the command that fixes it>"
//	DOCTOR <check> SKIP needs=<check>
//
// in the order seat, redis, fn, version, runners, ingest, pitstop, sprint
// (SKIP when the check it needs is not OK; a SKIP is not a fix), then one
// summary line, `DOCTOR OK checks=8 trips=<n> ms=<n>` or `DOCTOR FIX
// fixes=<n> skipped=<n> checks=8 trips=<n> ms=<n>`. Exit 0 all OK, 1 any fix,
// 2 usage.
//
// What each check reads, and nothing else: no ssh, no GitHub, no model, no
// file but the seat's own (read in this process through internal/seatcred,
// as every verb's --seat is):
//
//	seat     --seat (else NOVA_SPRINT_SEAT, then NOVA_SEAT) resolved: its
//	         seats.tsv row when it has one (#4330), the key, the store file
//	         and the Redis password;
//	         with no seat, the environment's login, or the default user when
//	         the store lets it in
//	redis    PING as that login
//	fn       FUNCTION LIST nova_sprint WITHCODE judged by fn.Judge, fn check's
//	         own verdict, then FCALL ns_ping only when the code is ours
//	version  this binary's build identity against fleet:release commit, the
//	         dev tip every landing into dev writes (#4050); the fix is self
//	         update on the coordinator's machine, fleet build on a bench
//	runners  this machine (--bench, else the short hostname) in the benches
//	         registry, its desired role, CI legs and hold, and its beat's age
//	         against preflight.BeatFresh
//	ingest   ev:github, the stream the webhook receiver writes: its last
//	         entry's age (older than ingestQuiet is a dead receiver) and the
//	         ci-github group's lag
//	pitstop  every sprint not closed: its s:<S>:pitstop (or the legacy key)
//	sprint   the one open sprint (control-* sprints aside) and sprint:epoch
//
// All of it is one pipeline, plus a second only when there are sprints to
// read or the library to ping, so trips= is at most 2.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

func init() {
	register(Verb{
		Name:    "doctor",
		Summary: "seat, redis, fn library, version vs dev tip, runners, webhook ingest, pit stop and the open sprint: one line each, OK or the exact fix",
		Run:     runDoctor,
	})
}

// doctorChecks is the order the lines print in; the summary counts them.
var doctorChecks = []string{"seat", "redis", "fn", "version", "runners", "ingest", "pitstop", "sprint"}

// ingestQuiet is how old ev:github's last entry may be before the receiver
// reads as dead. The org hook carries every push, PR, check run and workflow
// run of three repos, so a working day's quiet hour is the receiver, not
// GitHub.
const ingestQuiet = 30 * time.Minute

// doctorTimeout bounds the whole call: a store that does not answer is a FIX
// line in under this, never a hang.
const doctorTimeout = 3 * time.Second

// doctorControlPrefix marks a control sprint (internal/nsprint/table), which
// is never the open sprint.
const doctorControlPrefix = "control-"

// doctorSeat is what the seat check knows before Redis is dialled.
type doctorSeat struct {
	Name   string   // --seat / NOVA_SPRINT_SEAT / NOVA_SEAT; "" none
	User   string   // the Redis user the login names; "" the default user
	Key    string   // the seat file's password key
	Err    error    // the seat (or the environment login) did not resolve
	Remedy string   // the command that fixes Err
	Held   []string // seats whose age key this machine holds
}

// doctorSprint is one sprint that is not closed.
type doctorSprint struct {
	Name    string
	Status  string
	Stop    pitstop.Stop
	StopErr error
	Legacy  bool
}

// doctorFacts is everything the checks judge: the gather fills it, the
// judge (doctorLines) reads only it, so every branch is a unit test.
type doctorFacts struct {
	Addr, Machine, GOOS string
	UID                 int
	Me                  string // who a remedy names as --by
	Have                string // this binary's build identity
	Seat                doctorSeat
	OpenErr             error // store.Open refused (no address, the login)
	PingErr             error
	Now                 time.Time // the store's TIME

	Lib    fn.State
	LibErr error

	Release    map[string]string
	ReleaseErr error

	Registered bool
	RegErr     error
	Desired    map[string]string // role, legs, paused
	BeatAt     string
	BeatErr    error

	Groups    []redis.XInfoGroup
	GroupsErr error
	LastID    string
	LastErr   error

	Sprints   []doctorSprint // not closed, in name order
	SprintErr error
	Epoch     string
	EpochErr  error

	Trips int64
	MS    int64
}

type doctorLine struct {
	Check, State string
	Words        []string
	Remedy       string
}

func (l doctorLine) String() string {
	s := "DOCTOR " + l.Check + " " + l.State
	for _, w := range l.Words {
		s += " " + w
	}
	if l.Remedy != "" {
		s += " remedy=" + oneline.Quote(l.Remedy)
	}
	return s
}

func runDoctor(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("doctor")
	addrFlag := fs.String("redis", "", "the store to check (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR)")
	bench := fs.String("bench", "", "this machine's registry name (default: the short hostname)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "doctor", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "doctor", "takes flags, not positional arguments; nova-sprint doctor [--redis <addr>] [--bench <name>]")
	}
	start := time.Now()
	f := doctorFacts{
		Addr: doctorAddr(*addrFlag), Machine: doctorMachine(*bench), GOOS: runtime.GOOS, UID: os.Getuid(),
		Have: buildinfo.Version(version), Seat: doctorSeatNow(os.Getenv),
	}
	f.Me = doctorMe(f.Seat.Name)
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	doctorGather(ctx, &f)
	f.MS = time.Since(start).Milliseconds()
	lines := doctorLines(f)
	fixes := 0
	for _, l := range lines {
		fmt.Fprintln(out, l.String())
		if l.State == "FIX" {
			fixes++
		}
	}
	fmt.Fprintln(out, doctorSummary(lines, f.Trips, f.MS))
	if fixes > 0 {
		return 1
	}
	return 0
}

// doctorSummary is the last line: OK, or FIX with the count of fixes.
func doctorSummary(lines []doctorLine, trips, ms int64) string {
	fixes, skipped := 0, 0
	for _, l := range lines {
		switch l.State {
		case "FIX":
			fixes++
		case "SKIP":
			skipped++
		}
	}
	if fixes == 0 && skipped == 0 {
		return fmt.Sprintf("DOCTOR OK checks=%d trips=%d ms=%d", len(lines), trips, ms)
	}
	return fmt.Sprintf("DOCTOR FIX fixes=%d skipped=%d checks=%d trips=%d ms=%d", fixes, skipped, len(lines), trips, ms)
}

// doctorAddr is --redis, else the variables a seat row sets (taskAddr), else
// the selected seat's own address, the order every store dial uses.
func doctorAddr(flag string) string {
	if a := taskAddr(flag); a != "" {
		return a
	}
	return seatcred.Addr()
}

// doctorMachine is --bench, else the short hostname, lower case.
func doctorMachine(flag string) string {
	if flag != "" {
		return flag
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	h, _, _ = strings.Cut(h, ".")
	return strings.ToLower(h)
}

// doctorMe is who a remedy names as --by: NOVA_FRIEND, else the seat.
func doctorMe(seat string) string {
	if v := os.Getenv(seatEnv); v != "" {
		return v
	}
	if seat != "" {
		return seat
	}
	return "<you>"
}

// doctorSeatNow resolves the seat the way every verb's store.Open will, and
// names the fix when it cannot: the seat's own seal line when its file lacks
// the password, else the nova-secrets check line that names what is wrong.
func doctorSeatNow(getenv func(string) string) doctorSeat {
	s := doctorSeat{Held: heldSeats(getenv)}
	c, ok, err := seatcred.Active()
	if ok {
		s.Name = seatcred.Selected()
		if err != nil {
			s.Err, s.Remedy = err, seatRemedy(s.Name, err, getenv)
			return s
		}
		s.User, s.Key = c.User, c.Key
		return s
	}
	user, _, err := redisauth.Auth("", "")
	if err != nil {
		s.Err, s.Remedy = err, exportSeatRemedy(s.Held, "")
		return s
	}
	s.User = user
	return s
}

// heldSeats are the seats whose age key sits in this machine's key directory.
func heldSeats(getenv func(string) string) []string {
	home := getenv("HOME")
	if home == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(home, filepath.FromSlash(seatcred.DefaultKeyDir)))
	if err != nil {
		return nil
	}
	var seats []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".key"); ok && name != "" && !e.IsDir() {
			seats = append(seats, name)
		}
	}
	sort.Strings(seats)
	return seats
}

func seatRemedy(seat string, err error, getenv func(string) string) string {
	if _, seal, ok := strings.Cut(err.Error(), "seal it with "); ok {
		return seal
	}
	p, perr := seatcred.PathsFor(seat, getenv)
	if perr != nil {
		return perr.Error()
	}
	return fmt.Sprintf("nova-secrets check --store %s --as %s --key %s --sops %s", p.Store, seat, p.Key, p.Sops)
}

// exportSeatRemedy picks the seat to export: the one named for this machine
// when its key is here, else the first held, else a keygen for the machine.
func exportSeatRemedy(held []string, machine string) string {
	for _, s := range held {
		if s == machine {
			return "export NOVA_SPRINT_SEAT=" + s
		}
	}
	if len(held) > 0 {
		return "export NOVA_SPRINT_SEAT=" + held[0]
	}
	if machine == "" {
		machine = "<seat>"
	}
	return fmt.Sprintf("nova-secrets keygen --as %s --key ~/%s/%s.key --age-keygen $(command -v age-keygen) --store ~/%s",
		machine, seatcred.DefaultKeyDir, machine, seatcred.DefaultStore)
}

// doctorQuiet drops go-redis's own pool lines: a store doctor cannot reach
// is its redis line, never five library lines ahead of it. SetLogger writes a
// package variable, so it is written once per process (nova-merge #1609).
type doctorQuiet struct{}

func (doctorQuiet) Printf(context.Context, string, ...interface{}) {}

var doctorQuietOnce sync.Once

// doctorGather fills f from the store: round one is every read that needs no
// answer first; round two, only when needed, reads each sprint that is not
// closed and pings the library when its code is ours.
func doctorGather(ctx context.Context, f *doctorFacts) {
	if f.Addr == "" || (f.Seat.Name != "" && f.Seat.Err != nil) {
		return
	}
	doctorQuietOnce.Do(func() { redis.SetLogger(doctorQuiet{}) })
	st, err := store.OpenProbe(ctx, f.Addr)
	if err != nil {
		f.OpenErr = err
		return
	}
	defer st.Close()
	trips := st.CountTrips()
	defer func() { f.Trips = trips.N() }()

	b := "bench:" + f.Machine
	pipe := st.Client().Pipeline()
	ping := pipe.Ping(ctx)
	clock := pipe.Time(ctx)
	lib := pipe.FunctionList(ctx, fn.ListQuery)
	rel := pipe.HGetAll(ctx, fleetbuild.ConfigKey)
	reg := pipe.SIsMember(ctx, fleetbuild.BenchesKey, f.Machine)
	desired := pipe.HMGet(ctx, b+":desired", "role", "legs", "paused")
	beat := pipe.HGet(ctx, b+":beat", "at")
	groups := pipe.XInfoGroups(ctx, ghevent.Stream)
	last := pipe.XRevRangeN(ctx, ghevent.Stream, "+", "-", 1)
	members := pipe.SMembers(ctx, "sprints")
	order := pipe.ZRange(ctx, "sprint:order", 0, -1)
	epoch := pipe.Get(ctx, "sprint:epoch")
	_, _ = pipe.Exec(ctx)
	if err := ping.Err(); err != nil {
		f.PingErr = err
		return
	}
	f.Now = clock.Val()

	var ours bool
	if libs, err := lib.Result(); err != nil {
		f.LibErr = err
	} else {
		code, found := fn.FromList(libs)
		f.Lib, ours, f.LibErr = fn.Judge(code, found)
	}
	f.Release, f.ReleaseErr = rel.Result()
	f.Registered, f.RegErr = reg.Result()
	f.Desired = map[string]string{}
	if vals, err := desired.Result(); err != nil {
		f.RegErr = errors.Join(f.RegErr, err)
	} else {
		for i, k := range []string{"role", "legs", "paused"} {
			if s, ok := vals[i].(string); ok {
				f.Desired[k] = s
			}
		}
	}
	f.BeatAt, f.BeatErr = beat.Result()
	if errors.Is(f.BeatErr, redis.Nil) {
		f.BeatAt, f.BeatErr = "", nil
	}
	f.Groups, f.GroupsErr = groups.Result()
	if msgs, err := last.Result(); err != nil {
		f.LastErr = err
	} else if len(msgs) > 0 {
		f.LastID = msgs[0].ID
	}
	f.Epoch, f.EpochErr = epoch.Result()
	if errors.Is(f.EpochErr, redis.Nil) {
		f.Epoch, f.EpochErr = "", nil
	}
	names, err := sprintNames(members, order)
	if err != nil {
		f.SprintErr = err
	}

	if len(names) == 0 && !ours {
		return
	}
	type sprintCmds struct {
		status *redis.StringCmd
		stop   *redis.MapStringStringCmd
		legacy *redis.IntCmd
	}
	pipe = st.Client().Pipeline()
	cs := make([]sprintCmds, len(names))
	for i, s := range names {
		cs[i] = sprintCmds{
			status: pipe.HGet(ctx, "s:"+s, "status"),
			stop:   pipe.HGetAll(ctx, pitstop.Key(s)),
			legacy: pipe.Exists(ctx, pitstop.LegacyKey(s)),
		}
	}
	var pong *redis.Cmd
	if ours {
		pong = pipe.FCall(ctx, "ns_ping", nil)
	}
	_, _ = pipe.Exec(ctx)
	if pong != nil {
		f.Lib.Ping = fn.PingReply(pong.Result())
	}
	for i, s := range names {
		status, err := cs[i].status.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			f.SprintErr = errors.Join(f.SprintErr, err)
			continue
		}
		if status == "closed" {
			continue
		}
		sp := doctorSprint{Name: s, Status: status, Legacy: cs[i].legacy.Val() > 0}
		if h, err := cs[i].stop.Result(); err != nil {
			sp.StopErr = err
		} else {
			sp.Stop = pitstop.FromHash(s, h)
		}
		f.Sprints = append(f.Sprints, sp)
	}
}

// sprintNames is the sprints set and sprint:order, once each, sorted.
func sprintNames(members *redis.StringSliceCmd, order *redis.StringSliceCmd) ([]string, error) {
	var err error
	seen := map[string]bool{}
	var names []string
	for _, c := range []*redis.StringSliceCmd{members, order} {
		vals, e := c.Result()
		if e != nil && !errors.Is(e, redis.Nil) {
			err = errors.Join(err, e)
			continue
		}
		for _, s := range vals {
			if s != "" && !seen[s] {
				seen[s] = true
				names = append(names, s)
			}
		}
	}
	sort.Strings(names)
	return names, err
}

// doctorLines judges the facts: one line per check, in doctorChecks order.
func doctorLines(f doctorFacts) []doctorLine {
	seat := seatLine(f)
	redisL := redisLine(f, seat)
	lines := []doctorLine{seat, redisL}
	if redisL.State != "OK" {
		for _, c := range doctorChecks[2:] {
			lines = append(lines, doctorLine{Check: c, State: "SKIP", Words: []string{"needs=redis"}})
		}
		return lines
	}
	ver := versionLine(f)
	return append(lines, fnLine(f, ver), ver, runnersLine(f), ingestLine(f), pitstopLine(f), sprintLine(f))
}

func seatLine(f doctorFacts) doctorLine {
	s := f.Seat
	name := s.Name
	if name == "" {
		name = "none"
	}
	l := doctorLine{Check: "seat", Words: []string{"seat=" + oneline.Field(name)}}
	switch {
	case s.Err != nil:
		l.State, l.Remedy = "FIX", s.Remedy
		if s.Name == "" {
			l.Remedy = exportSeatRemedy(s.Held, f.Machine)
		}
		l.Words = append(l.Words, "err="+oneline.Quote(s.Err.Error()))
	case s.Name == "" && f.PingErr != nil && authRefused(f.PingErr):
		// No seat and the store wants a login: the seat is the fix.
		l.State, l.Remedy = "FIX", exportSeatRemedy(s.Held, f.Machine)
		l.Words = append(l.Words, "err="+oneline.Quote(f.PingErr.Error()))
	default:
		l.State = "OK"
		user := s.User
		if user == "" {
			user = "default"
		}
		l.Words = append(l.Words, "user="+oneline.Field(user))
		if s.Key != "" {
			l.Words = append(l.Words, "key="+oneline.Field(s.Key))
		}
	}
	if len(s.Held) > 0 && l.State == "FIX" {
		l.Words = append(l.Words, "held="+oneline.Field(strings.Join(s.Held, ",")))
	}
	return l
}

func authRefused(err error) bool {
	s := err.Error()
	return strings.Contains(s, "NOAUTH") || strings.Contains(s, "WRONGPASS") || strings.Contains(s, "failed to authenticate")
}

func redisLine(f doctorFacts, seat doctorLine) doctorLine {
	l := doctorLine{Check: "redis"}
	switch {
	case f.Addr == "":
		l.State, l.Words = "FIX", []string{"addr=none"}
		l.Remedy = "export NOVA_SPRINT_REDIS=<host:port> (or nova-sprint doctor --redis <host:port>)"
		return l
	case seat.State != "OK":
		l.State, l.Words = "SKIP", []string{"needs=seat"}
		return l
	}
	l.Words = []string{"addr=" + oneline.Field(f.Addr)}
	err := f.OpenErr
	if err == nil {
		err = f.PingErr
	}
	switch {
	case err == nil:
		l.State = "OK"
		user := f.Seat.User
		if user == "" {
			user = "default"
		}
		l.Words = append(l.Words, "user="+oneline.Field(user))
	case authRefused(err) && f.Seat.Name != "":
		l.State = "FIX"
		l.Words = append(l.Words, "err="+oneline.Quote(err.Error()))
		l.Remedy = fmt.Sprintf("nova-secrets seal --as %s --name %s (the seat's password is not the store's)", f.Seat.Name, f.Seat.Key)
	default:
		l.State = "FIX"
		l.Words = append(l.Words, "err="+oneline.Quote(err.Error()))
		l.Remedy = "nova-sprint doctor --redis <host:port> with the store's address; if it is right, make -C fleet store (rowan-tools) brings the store up"
	}
	return l
}

// versionRemedy installs the dev tip on this machine: the coordinator's
// (fleet:release self) through self update (#4337, "the fleet play does the
// benches, this verb does the coordinator's own machine"), a bench through
// fleet build.
func versionRemedy(f doctorFacts) string {
	commit := f.Release["commit"]
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if f.Machine != "" && f.Machine == f.Release["self"] {
		return "nova-sprint self update --sha " + commit
	}
	return "nova-sprint fleet build --bench " + f.Machine + " --redis " + f.Addr
}

func versionLine(f doctorFacts) doctorLine {
	l := doctorLine{Check: "version", Words: []string{"have=" + oneline.Field(f.Have)}}
	commit := f.Release["commit"]
	switch {
	case f.ReleaseErr != nil:
		l.State = "FIX"
		l.Words = append(l.Words, "err="+oneline.Quote(f.ReleaseErr.Error()))
		l.Remedy = "nova-sprint fleet build --dry-run --redis " + f.Addr + " (reads fleet:release and names what it cannot)"
		return l
	case commit == "":
		l.State = "FIX"
		l.Words = append(l.Words, "tip=none")
		l.Remedy = "nova-sprint fleet build set version=v0.16.0-dev.<sha8> commit=<sha40> --redis " + f.Addr + " (a landing into dev writes both)"
		return l
	}
	tip := commit
	if len(tip) > 12 {
		tip = tip[:12]
	}
	l.Words = append(l.Words, "tip="+oneline.Field(tip))
	rev, dirty, ok := buildRevision(f.Have)
	switch {
	case !ok:
		l.State, l.Remedy = "FIX", versionRemedy(f)
		l.Words = append(l.Words, "why=no-revision")
	case dirty:
		l.State, l.Remedy = "FIX", versionRemedy(f)
		l.Words = append(l.Words, "why=dirty")
	case !strings.HasPrefix(commit, rev):
		l.State, l.Remedy = "FIX", versionRemedy(f)
		l.Words = append(l.Words, "why=behind-or-ahead")
	default:
		l.State = "OK"
	}
	return l
}

// buildRevision is the commit a build identity names (buildinfo): the
// trailing 12 hex of a vcs stamp or a pseudo-version
// (<utc>-<rev12>[-dirty], v<x>.<y>.<z>-dev.<sha8>.0.<utc>-<rev12>), else the
// <sha8> of a release stamp (v<x>.<y>.<z>-dev.<sha8>). ok is false for devel
// or anything else that names no commit.
func buildRevision(v string) (rev string, dirty, ok bool) {
	v, dirty = strings.CutSuffix(v, "-dirty")
	if i := strings.LastIndexByte(v, '-'); i >= 0 && isHex(v[i+1:], 12) {
		return v[i+1:], dirty, true
	}
	if i := strings.LastIndex(v, "-dev."); i >= 0 && isHex(v[i+5:], 8) {
		return v[i+5:], dirty, true
	}
	return "", dirty, false
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func fnLine(f doctorFacts, ver doctorLine) doctorLine {
	l := doctorLine{Check: "fn"}
	deploy := "NOVA_SPRINT_REDIS_USER=admin NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN nova-sprint fn deploy --redis " + f.Addr
	if ver.State != "OK" {
		// A binary that is not the dev tip embeds another library: deploying
		// it would roll the store back, so the fix is the binary first.
		deploy = ver.Remedy
	}
	s := f.Lib
	switch {
	case f.LibErr != nil:
		l.State = "FIX"
		l.Words = []string{"err=" + oneline.Quote(f.LibErr.Error())}
		l.Remedy = "nova-sprint fn check --redis " + f.Addr
	case s.Missing:
		l.State, l.Remedy = "FIX", deploy
		l.Words = []string{"MISSING", "want=" + s.Want}
	case s.Loaded != s.Want:
		l.State, l.Remedy = "FIX", deploy
		l.Words = []string{"STALE", "loaded=" + s.Loaded, "want=" + s.Want}
	case !s.OK():
		l.State, l.Remedy = "FIX", deploy
		l.Words = []string{"NOPING", "sha=" + s.Want, "ping=" + oneline.Quote(s.Ping)}
	default:
		l.State = "OK"
		l.Words = []string{"sha=" + s.Want, "ping=PONG"}
	}
	return l
}

func beatRemedy(goos string, uid int) string {
	if goos == "darwin" {
		return "launchctl kickstart -k gui/" + strconv.Itoa(uid) + "/com.nova.loop.nova-sprint-bench-beat"
	}
	return "systemctl --user restart nova-loop-nova-sprint-bench-beat.service"
}

// ageWords is an age as a person reads it: 45s, 3m, 2h, 4d.
func ageWords(d time.Duration) string {
	switch {
	case d < 0:
		d = 0
		fallthrough
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

func runnersLine(f doctorFacts) doctorLine {
	l := doctorLine{Check: "runners", Words: []string{"bench=" + oneline.Field(f.Machine)}}
	if f.RegErr != nil {
		l.State, l.Remedy = "FIX", "nova-sprint bench ls --redis "+f.Addr
		l.Words = append(l.Words, "err="+oneline.Quote(f.RegErr.Error()))
		return l
	}
	if !f.Registered {
		l.State = "FIX"
		l.Words = append(l.Words, "registered=no")
		l.Remedy = "make -C fleet bench-register (rowan-tools); if this machine has another registry name, nova-sprint doctor --bench <name>"
		return l
	}
	role := f.Desired["role"]
	if role == "" {
		role = "fleet"
	}
	legs := f.Desired["legs"]
	if legs == "" {
		legs = "-"
	}
	l.Words = append(l.Words, "role="+oneline.Field(role), "legs="+oneline.Field(legs))
	at, _ := strconv.ParseInt(f.BeatAt, 10, 64)
	switch {
	case f.BeatErr != nil:
		l.State, l.Remedy = "FIX", beatRemedy(f.GOOS, f.UID)
		l.Words = append(l.Words, "err="+oneline.Quote(f.BeatErr.Error()))
	case at <= 0:
		l.State, l.Remedy = "FIX", beatRemedy(f.GOOS, f.UID)
		l.Words = append(l.Words, "beat=none")
	case f.Now.Sub(time.UnixMilli(at)) > preflight.BeatFresh:
		l.State, l.Remedy = "FIX", beatRemedy(f.GOOS, f.UID)
		l.Words = append(l.Words, "beat="+ageWords(f.Now.Sub(time.UnixMilli(at))))
	case f.Desired["paused"] == "1":
		l.State, l.Remedy = "FIX", "nova-sprint fleet release --bench "+f.Machine+" --redis "+f.Addr
		l.Words = append(l.Words, "beat="+ageWords(f.Now.Sub(time.UnixMilli(at))), "paused=1")
	default:
		l.State = "OK"
		l.Words = append(l.Words, "beat="+ageWords(f.Now.Sub(time.UnixMilli(at))))
	}
	return l
}

// streamIDTime is the ms time an XADD id carries (<ms>-<seq>).
func streamIDTime(id string) (time.Time, bool) {
	ms, _, _ := strings.Cut(id, "-")
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(n), true
}

func ingestLine(f doctorFacts) doctorLine {
	l := doctorLine{Check: "ingest", Words: []string{"stream=" + ghevent.Stream}}
	receiver := "make -C fleet hook (rowan-tools), then nova-post hook probe --pr <n>"
	drain := "nova-sprint ci github --redis " + f.Addr + " --once"
	if f.GroupsErr != nil {
		l.State = "FIX"
		if strings.Contains(f.GroupsErr.Error(), "no such key") {
			l.Words, l.Remedy = append(l.Words, "last=none"), receiver
			return l
		}
		l.Words = append(l.Words, "err="+oneline.Quote(f.GroupsErr.Error()))
		l.Remedy = "nova-sprint redis-cli --redis " + f.Addr + " -- XINFO GROUPS " + ghevent.Stream
		return l
	}
	if f.LastErr != nil {
		l.State, l.Remedy = "FIX", receiver
		l.Words = append(l.Words, "err="+oneline.Quote(f.LastErr.Error()))
		return l
	}
	at, ok := streamIDTime(f.LastID)
	if !ok {
		l.State, l.Remedy = "FIX", receiver
		l.Words = append(l.Words, "last=none")
		return l
	}
	age := f.Now.Sub(at)
	l.Words = append(l.Words, "last="+ageWords(age))
	var group *redis.XInfoGroup
	for i := range f.Groups {
		if f.Groups[i].Name == webhook.Group {
			group = &f.Groups[i]
		}
	}
	switch {
	case age > ingestQuiet:
		l.State, l.Remedy = "FIX", receiver
		l.Words = append(l.Words, "quiet="+ageWords(ingestQuiet))
	case group == nil:
		l.State, l.Remedy = "FIX", drain
		l.Words = append(l.Words, "group=none")
	case group.Lag > 0:
		l.State, l.Remedy = "FIX", drain
		l.Words = append(l.Words, "group="+webhook.Group, "lag="+strconv.FormatInt(group.Lag, 10), "pending="+strconv.FormatInt(group.Pending, 10))
	default:
		l.State = "OK"
		l.Words = append(l.Words, "group="+webhook.Group, "lag="+strconv.FormatInt(max(group.Lag, 0), 10), "pending="+strconv.FormatInt(group.Pending, 10))
	}
	return l
}

func pitstopLine(f doctorFacts) doctorLine {
	l := doctorLine{Check: "pitstop"}
	if f.SprintErr != nil {
		l.State, l.Remedy = "FIX", "nova-sprint sprint status --redis "+f.Addr
		l.Words = []string{"err=" + oneline.Quote(f.SprintErr.Error())}
		return l
	}
	var held []doctorSprint
	for _, s := range f.Sprints {
		if s.StopErr != nil || s.Stop.Set || s.Legacy {
			held = append(held, s)
		}
	}
	if len(held) == 0 {
		l.State = "OK"
		l.Words = []string{"none", "sprints=" + strconv.Itoa(len(f.Sprints))}
		return l
	}
	h := held[0]
	S := oneline.Field(h.Name)
	l.State = "FIX"
	l.Remedy = "nova-sprint pitstop clear --sprint " + S + " --by " + oneline.Field(f.Me) + " --redis " + f.Addr
	switch {
	case h.StopErr != nil:
		l.Words = []string{"sprint=" + S, "by=wrongtype", "err=" + oneline.Quote(h.StopErr.Error())}
	case h.Stop.Set:
		l.Words = []string{"sprint=" + S, "by=" + oneline.Field(h.Stop.By),
			"age=" + ageWords(f.Now.Sub(time.UnixMilli(h.Stop.At))), "why=" + oneline.Quote(h.Stop.Why)}
		if h.Stop.Streams != nil {
			l.Words = append(l.Words, "streams="+oneline.Field(strings.Join(h.Stop.Streams, ",")))
		}
	default:
		l.Words = []string{"sprint=" + S, "by=legacy", "key=" + pitstop.LegacyKey(h.Name)}
	}
	l.Words = append(l.Words, "held="+strconv.Itoa(len(held)))
	return l
}

func sprintLine(f doctorFacts) doctorLine {
	l := doctorLine{Check: "sprint"}
	if f.SprintErr != nil {
		l.State, l.Remedy = "FIX", "nova-sprint sprint status --redis "+f.Addr
		l.Words = []string{"err=" + oneline.Quote(f.SprintErr.Error())}
		return l
	}
	var open []string
	for _, s := range f.Sprints {
		if s.Status == "open" && !strings.HasPrefix(s.Name, doctorControlPrefix) {
			open = append(open, s.Name)
		}
	}
	epoch := f.Epoch
	if epoch == "" {
		epoch = "0"
	}
	switch {
	case f.EpochErr != nil:
		l.State = "FIX"
		l.Words = []string{"err=" + oneline.Quote(f.EpochErr.Error())}
		l.Remedy = "nova-sprint redis-cli --redis " + f.Addr + " -- TYPE sprint:epoch"
	case len(open) == 0:
		l.State = "FIX"
		l.Words = []string{"open=0"}
		l.Remedy = "nova-sprint sprint open --sprint <name> --from <work-set.lisp> --redis " + f.Addr
	case len(open) > 1:
		// One active sprint, many streams (Glenn 2026-09-26 8:50 AM ET).
		l.State = "FIX"
		l.Words = []string{"open=" + strconv.Itoa(len(open)), "sprints=" + oneline.Field(strings.Join(open, ","))}
		l.Remedy = "nova-sprint sprint close --sprint " + oneline.Field(open[1]) + " --redis " + f.Addr
	default:
		l.State = "OK"
		l.Words = []string{"sprint=" + oneline.Field(open[0]), "epoch=" + oneline.Field(epoch)}
	}
	return l
}
