// This file is the fleet gatherer and `nova-sprint preflight --fleet`
// (#3188, #3646): it reads the bench registry and every bench's beat,
// desired hash and leases from Redis in one pipelined exchange, fills the
// FleetInput that FleetChecks (#3004) reads, and renders one PASS/FAIL row
// per bench plus one fleet line. The bench facts it gates on (harness
// versions, mirrors, free disk) ride the bench's own beat (ns_bench_beat,
// written by `nova-sprint bench beat` on the bench), so the rows never ssh.
// The declared standard is fleet/group_vars/all.yml in rowan-tools, read from
// the path the caller names.
package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// DefaultDiskFloorGiB is the free-space floor every bench keeps (Glenn
// 2026-09-24, rowan-tools #326: disk-guard refuses writers under 200 GiB).
// all.yml's disk_floor_gib overrides it once declared there.
const DefaultDiskFloorGiB = 200

// Standard is the declared fleet standard a bench row is held to.
type Standard struct {
	Build        string   // nova_build's short sha suffix
	Harness      string   // harness_version
	Mirrors      []string // mirrors: the declared git mirrors, in file order
	DiskFloorGiB int
}

// ReadStandard reads nova_build, harness_version, mirrors and disk_floor_gib
// from all.yml. Like ReadDeclared it reads top-level scalar lines only; the
// mirrors list is the one-line flow list the file declares.
func ReadStandard(path string) (Standard, error) {
	d, err := ReadDeclared(path)
	if err != nil {
		return Standard{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Standard{}, err
	}
	s := Standard{Build: d.Build, DiskFloorGiB: DefaultDiskFloorGiB}
	for _, l := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			continue
		}
		if i := strings.Index(v, "#"); i >= 0 {
			v = v[:i]
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "harness_version":
			s.Harness = v
		case "mirrors":
			s.Mirrors = flowList(v)
		case "disk_floor_gib":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return Standard{}, fmt.Errorf("%s: disk_floor_gib %q is not a positive whole number", path, v)
			}
			s.DiskFloorGiB = n
		}
	}
	var missing []string
	for _, m := range [][2]string{{"nova_build", s.Build}, {"harness_version", s.Harness}} {
		if m[1] == "" {
			missing = append(missing, m[0])
		}
	}
	if len(s.Mirrors) == 0 {
		missing = append(missing, "mirrors")
	}
	if len(missing) > 0 {
		return Standard{}, fmt.Errorf("%s declares no %s", path, strings.Join(missing, ", "))
	}
	return s, nil
}

func flowList(v string) []string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil
	}
	var out []string
	for _, item := range strings.Split(v[1:len(v)-1], ",") {
		if item = strings.Trim(strings.TrimSpace(item), `"'`); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// BenchFacts is one registered bench as the fleet store holds it.
type BenchFacts struct {
	BenchState
	BeatAt  bool     // the beat carries a readable at
	Build   string   // the beat's nova-sprint build, its short sha
	Harness []string // harness versions present on the bench
	Mirrors []string // git mirrors present on the bench
	DiskGiB int      // free GiB under the bench root
	DiskOK  bool     // the beat carried a readable disk fact
	Slots   int      // bench:<b>:desired slots
	Leased  int      // ZCARD bench:<b>:cards:working, the one lease ledger (#3998)
}

// Free is the bench's free slots now.
func (b BenchFacts) Free() int {
	if f := b.Slots - b.Leased; f > 0 {
		return f
	}
	return 0
}

// Fleet is one read of the fleet store.
type Fleet struct {
	Now     time.Time
	Benches []BenchFacts
	Open    int    // sprints in the sprints set whose status is open
	Sprint  string // the sprint asked about; empty means none
	Pitstop string // sprint:<S>:pitstop, empty when none is set
}

// Input is the FleetInput the gather loaded: the bench registry and every
// bench's beat. The other collections are gathered elsewhere and stay unread,
// so their checks say MISSING.
func (f Fleet) Input() FleetInput {
	in := FleetInput{Loaded: Loaded{Benches: true}}
	for _, b := range f.Benches {
		in.Benches = append(in.Benches, b.BenchState)
	}
	return in
}

// GatherFleet reads the fleet in two pipelined exchanges, never SCAN or KEYS:
// the store clock, the bench registry, the sprints set and the sprint's pit
// stop; then per bench its beat, desired hash and lease counts, and per
// sprint its status.
func GatherFleet(ctx context.Context, c *redis.Client, sprint string) (Fleet, error) {
	pipe := c.Pipeline()
	clock := pipe.Time(ctx)
	names := pipe.SMembers(ctx, "benches")
	sprints := pipe.SMembers(ctx, "sprints")
	var pit *redis.StringCmd
	if sprint != "" {
		pit = pipe.Get(ctx, "sprint:"+sprint+":pitstop")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Fleet{}, err
	}
	f := Fleet{Now: clock.Val(), Sprint: sprint}
	if pit != nil {
		f.Pitstop = pit.Val()
	}
	benches := names.Val()
	sort.Strings(benches)
	type cmds struct {
		beat, desired *redis.MapStringStringCmd
		working       *redis.IntCmd
	}
	cs := make([]cmds, len(benches))
	status := make([]*redis.StringCmd, len(sprints.Val()))
	pipe = c.Pipeline()
	for i, b := range benches {
		cs[i] = cmds{
			beat:    pipe.HGetAll(ctx, "bench:"+b+":beat"),
			desired: pipe.HGetAll(ctx, "bench:"+b+":desired"),
			working: pipe.ZCard(ctx, "bench:"+b+":cards:working"),
		}
	}
	for i, s := range sprints.Val() {
		status[i] = pipe.HGet(ctx, "s:"+s, "status")
	}
	if len(benches)+len(status) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return Fleet{}, err
		}
	}
	for _, s := range status {
		if s.Val() == "open" {
			f.Open++
		}
	}
	for i, name := range benches {
		beat, desired := cs[i].beat.Val(), cs[i].desired.Val()
		b := BenchFacts{BenchState: BenchState{Name: name, Launcher: beat["launcher"]}}
		b.Paused = desired["paused"] == "1" || desired["paused"] == "true"
		b.Slots, _ = strconv.Atoi(desired["slots"])
		b.Desired = b.Slots
		b.Leased = int(cs[i].working.Val())
		if len(beat) > 0 {
			b.BeatPresent = true
			if at, ok := parseStamp(beat["at"]); ok {
				b.BeatAt = true
				if b.BeatAge = f.Now.Sub(at); b.BeatAge < 0 {
					b.BeatAge = 0
				}
			}
		}
		b.Build = shortBuild(beat["build"])
		b.Harness = splitList(beat["harness"])
		b.Mirrors = splitList(beat["mirrors"])
		if n, err := strconv.Atoi(beat["disk_gib"]); err == nil && n >= 0 {
			b.DiskGiB, b.DiskOK = n, true
		}
		f.Benches = append(f.Benches, b)
	}
	return f, nil
}

// shortBuild is the short sha of a version line's build identity
// ("nova-sprint v0.16.0-dev.3403baa7 darwin/arm64 go1.26.6" -> 3403baa7),
// or "" when the line does not parse as one.
func shortBuild(line string) string {
	fields, ok := buildinfo.Parse(line)
	if !ok {
		return ""
	}
	v := fields.Version
	return v[strings.LastIndex(v, ".")+1:]
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Library is the loaded nova_sprint function library against the binary's.
type Library struct {
	Have string // sha256 of the loaded code, 8 hex; "" when not loaded
	Want string // sha256 of the binary's library, 8 hex
	Err  error  // the read or the build failed
}

// ReadLibrary reads the loaded library with FUNCTION LIST, which the bench
// seat is refused by design: a NOPERM names the seat preflight needs.
func ReadLibrary(ctx context.Context, c *redis.Client) Library {
	var lib Library
	want, err := fn.Source()
	if err != nil {
		lib.Err = fmt.Errorf("the binary's library does not build: %w", err)
		return lib
	}
	lib.Want = sha8(want)
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library, WithCode: true}).Result()
	switch {
	case isNoPerm(err):
		lib.Err = fmt.Errorf("seat %s may not run FUNCTION LIST (%s); %s", seatOf(c), firstLine(err.Error()), SeatHint)
	case err != nil:
		lib.Err = fmt.Errorf("cannot list functions: %s", firstLine(err.Error()))
	default:
		if l, ok := library(libs, fn.Library); ok {
			lib.Have = sha8(l.Code)
		}
	}
	return lib
}

func sha8(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])[:8]
}

// Row is one bench's verdict and the fields that failed it. The fail= tail
// is last on the line and read by a person; every field before it is one
// whitespace-free token.
type Row struct {
	Bench string
	Fail  []string // "<field>(<why>)", in row order
	Text  string
}

// BenchRow holds one bench to the standard. A paused bench is out of the
// deal: its row reports the fields and never fails.
func BenchRow(b BenchFacts, s Standard) Row {
	var fail []string
	bad := func(field, why string) { fail = append(fail, field+"("+why+")") }

	harness := strings.Join(b.Harness, ",")
	switch {
	case len(b.Harness) == 0:
		harness = "MISSING"
		bad("harness", "not on the beat")
	case !contains(b.Harness, s.Harness):
		bad("harness", "want "+s.Harness)
	}
	build := b.Build
	switch {
	case build == "":
		build = "MISSING"
		bad("build", "not on the beat")
	case build != s.Build:
		bad("build", "want "+s.Build)
	}
	beat := "MISSING"
	switch {
	case !b.BeatPresent:
		bad("beat", "no beat")
	case !b.BeatAt:
		bad("beat", "no at")
	default:
		beat = wholeSecs(b.BeatAge)
		if b.BeatAge > BeatFresh {
			bad("beat", beat+" old, fresh is under "+wholeSecs(BeatFresh))
		}
	}
	var absent []string
	for _, m := range s.Mirrors {
		if !contains(b.Mirrors, m) {
			absent = append(absent, m)
		}
	}
	if len(absent) > 0 {
		bad("mirrors", "absent "+strings.Join(absent, ","))
	}
	if b.Slots <= 0 {
		bad("slots", "no desired slots")
	}
	disk := "MISSING"
	switch {
	case !b.DiskOK:
		bad("disk", "not on the beat")
	default:
		disk = strconv.Itoa(b.DiskGiB)
		if b.DiskGiB < s.DiskFloorGiB {
			bad("disk", fmt.Sprintf("%d GiB under the %d GiB floor", b.DiskGiB, s.DiskFloorGiB))
		}
	}
	state := "PASS"
	if b.Paused {
		fail = nil
	} else if len(fail) > 0 {
		state = "FAIL"
	}
	text := fmt.Sprintf("%s %s harness=%s build=%s beat=%s mirrors=%d/%d slots=%d/%d disk=%sGiB",
		oneline.Field(b.Name), state, oneline.Field(harness), oneline.Field(build), beat,
		len(s.Mirrors)-len(absent), len(s.Mirrors), b.Free(), b.Slots, disk)
	if b.Paused {
		text += " paused=1"
	}
	if len(fail) > 0 {
		text += " fail=" + oneline.Escape(strings.Join(fail, ", "))
	}
	return Row{Bench: b.Name, Fail: fail, Text: text}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// FleetRows renders every bench row, then the fleet line: the benches that
// passed, the open sprints, the pit stop, the function library and the
// FleetChecks lines the gather loaded (7.4 and 7.5; the rest need inputs this
// gather does not read). It returns the lines and the exit code: 1 when any
// row or the fleet line fails.
func FleetRows(ctx context.Context, f Fleet, s Standard, lib Library) ([]string, int) {
	var lines, fleetFail []string
	passed := 0
	for _, b := range f.Benches {
		r := BenchRow(b, s)
		lines = append(lines, r.Text)
		if len(r.Fail) == 0 {
			passed++
		}
	}
	if len(f.Benches) == 0 {
		fleetFail = append(fleetFail, "benches(none registered)")
	} else if passed < len(f.Benches) {
		fleetFail = append(fleetFail, fmt.Sprintf("benches(%d failing)", len(f.Benches)-passed))
	}

	pit := "unasked"
	if f.Sprint != "" {
		pit = "none"
		if f.Pitstop != "" {
			pit = f.Pitstop
			fleetFail = append(fleetFail, "pitstop(set on "+f.Sprint+")")
		}
	}
	fnlib := lib.Have
	switch {
	case lib.Err != nil:
		fnlib = "MISSING"
		fleetFail = append(fleetFail, "fnlib("+lib.Err.Error()+")")
	case lib.Have == "":
		fnlib = "MISSING"
		fleetFail = append(fleetFail, "fnlib("+fn.Library+" not loaded)")
	case lib.Have != lib.Want:
		fleetFail = append(fleetFail, "fnlib(want "+lib.Want+", the binary's library)")
	}
	var checks []string
	for _, l := range FleetChecks(ctx, f.Input()) {
		if l.Check != "7.4" && l.Check != "7.5" {
			continue
		}
		state := "GREEN"
		if l.Red {
			state = "RED"
			fleetFail = append(fleetFail, l.Check+"("+l.What+")")
		}
		checks = append(checks, l.Check+"="+state)
	}
	state := "PASS"
	if len(fleetFail) > 0 {
		state = "FAIL"
	}
	summary := fmt.Sprintf("fleet %s benches=%d/%d open=%d pitstop=%s fnlib=%s %s",
		state, passed, len(f.Benches), f.Open, oneline.Field(pit), fnlib, strings.Join(checks, " "))
	if len(fleetFail) > 0 {
		summary += " fail=" + oneline.Escape(strings.Join(fleetFail, "; "))
	}
	lines = append(lines, summary)
	if state == "FAIL" {
		return lines, 1
	}
	return lines, 0
}
