package fleet

// The process sample on the bench beat (#4338): `nova-sprint fleet ps` reads
// what runs on every bench from the beats instead of a coordinator ssh-ing
// round the fleet with ps and top (rowan-tools probe-fleet.py, which found the
// deal-status watch, the two-day monitor, the jev-loop and the grok heartbeat
// by hand). The bench beat takes one ps read (at most one per PSEvery), reads
// its nova unit files, and writes one PSSample as JSON in the beat's ps field;
// this file is the sample's shape, how it is taken from ps and the unit files,
// and how the verb prints it. Nothing here runs a process or opens a socket.
//
// A nova unit is a launchd job com.nova.<x>.plist (~/Library/LaunchAgents,
// /Library/LaunchDaemons) or a systemd unit nova-<x>.service
// (~/.config/systemd/user, /etc/systemd/system). It is declared when its file
// names the fleet play that wrote it (fleet/loops.yml, fleet/runners.yml,
// fleet/monitoring.yml, ...: every rowan-tools template carries the line), and
// undeclared otherwise: a unit written by hand is a stray.
//
// A process belongs to a declared unit when its command line is the unit's
// command (or the command after a `--`, which nova-loop and nova-secrets exec
// both exec into), allowing an interpreter in front (`bash <script> ...`);
// every descendant of such a process belongs to it too. The Old list is the
// bench user's processes that belong to no declared unit, oldest first; the
// verb's --stray prints the ones older than the last play.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The sample's bounds: the beat carries at most these many entries, so its
// size is bounded however many processes a bench runs.
const (
	PSTopMax  = 5   // processes by CPU
	PSOldMax  = 12  // oldest processes outside every declared unit
	PSUnitMax = 48  // nova units
	PSCmdMax  = 120 // bytes of one command line
)

// PSEvery is how often the beat reads ps; between reads it carries the last
// sample (its At says when it was taken).
const PSEvery = 10 * time.Second

// Unit states.
const (
	UnitDeclared   = "declared"
	UnitUndeclared = "undeclared"
)

// PSProc is one process on the beat.
type PSProc struct {
	PID   int     `json:"pid"`
	CPU   float64 `json:"cpu"`
	User  string  `json:"user,omitempty"`
	Start int64   `json:"start"` // unix seconds: the sample's At minus the process's age
	Cmd   string  `json:"cmd"`   // the command line, capped at PSCmdMax bytes
}

// PSUnit is one nova unit file on the bench.
type PSUnit struct {
	Name  string `json:"name"`
	State string `json:"state"` // UnitDeclared or UnitUndeclared
}

// PSSample is the beat's ps field.
type PSSample struct {
	At    int64    `json:"at"` // unix seconds of the ps read
	Top   []PSProc `json:"top"`
	Old   []PSProc `json:"old"`
	OldN  int      `json:"old_n"` // Old before the PSOldMax cut
	Units []PSUnit `json:"units"`
	UnitN int      `json:"unit_n"` // Units before the PSUnitMax cut
	// Err is why the bench could not take the sample (no ps, a line it
	// cannot read); the verb prints it and never reads the bench as clean.
	Err string `json:"err,omitempty"`
}

// Encode is the sample as the beat's ps field.
func (s PSSample) Encode() string {
	b, err := json.Marshal(s)
	if err != nil {
		return `{"err":` + strconv.Quote("encode: "+err.Error()) + `}`
	}
	return string(b)
}

// DecodePS reads a beat's ps field.
func DecodePS(field string) (PSSample, error) {
	var s PSSample
	if err := json.Unmarshal([]byte(field), &s); err != nil {
		return PSSample{}, fmt.Errorf("ps field: %v", err)
	}
	return s, nil
}

// PSFullCommand is the beat's one ps line for an OS: pid, ppid, age, CPU
// percent, uid and the whole command line, no header, no width limit.
func PSFullCommand(goos string) ([]string, error) {
	switch goos {
	case "linux":
		return []string{"ps", "-eww", "-o", "pid=,ppid=,etimes=,pcpu=,uid=,args="}, nil
	case "darwin":
		return []string{"ps", "-Aww", "-o", "pid=,ppid=,etime=,pcpu=,uid=,args="}, nil
	}
	return nil, fmt.Errorf("no ps sample for %s (linux and darwin)", goos)
}

// PSRow is one line of PSFullCommand's output.
type PSRow struct {
	PID, PPID, Age, UID int
	CPU                 float64
	Args                string // fields joined by one space
}

// ParsePSRows reads PSFullCommand(goos)'s output; age is etimes seconds on
// linux and etime [[dd-]hh:]mm:ss on darwin.
func ParsePSRows(goos, out string) ([]PSRow, error) {
	var rows []PSRow
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 6 {
			return nil, fmt.Errorf("ps line %d %q: want pid, ppid, age, cpu, uid, args", i+1, line)
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		uid, e3 := strconv.Atoi(f[4])
		cpu, e4 := strconv.ParseFloat(f[3], 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			return nil, fmt.Errorf("ps line %d %q: pid, ppid, cpu and uid are numbers", i+1, line)
		}
		var age int
		var err error
		if goos == "darwin" {
			age, err = ParseEtime(f[2])
		} else {
			age, err = strconv.Atoi(f[2])
		}
		if err != nil {
			return nil, fmt.Errorf("ps line %d: %v", i+1, err)
		}
		rows = append(rows, PSRow{PID: pid, PPID: ppid, Age: age, UID: uid, CPU: cpu, Args: strings.Join(f[5:], " ")})
	}
	return rows, nil
}

// UnitFile is one nova unit file as the bench read it.
type UnitFile struct {
	File string // base name: com.nova.loop.x.plist or nova-loop-x.service
	Body string
}

// IsNovaUnit reports whether a unit directory entry is a nova unit: the
// play's backup copies (x.plist.<pid>.<date>~) are not.
func IsNovaUnit(file string) bool {
	return (strings.HasPrefix(file, "com.nova.") && strings.HasSuffix(file, ".plist")) ||
		(strings.HasPrefix(file, "nova-") && strings.HasSuffix(file, ".service"))
}

// playRef is a fleet play named in a unit file: every rowan-tools template
// writes one (`managed by Ansible (fleet/loops.yml)`, fleet/group_vars/all.yml).
var playRef = regexp.MustCompile(`\bfleet/[A-Za-z0-9_./-]+\.yml\b`)

// UnitIsDeclared reports whether a unit file names the fleet play that wrote it.
func UnitIsDeclared(body string) bool { return playRef.MatchString(body) }

// UnitName is a unit file's name without its extension.
func UnitName(file string) string {
	return strings.TrimSuffix(strings.TrimSuffix(file, ".plist"), ".service")
}

// UnitCommand is the argv a unit runs: a plist's ProgramArguments (else
// Program), a service's first ExecStart= with its prefix characters dropped.
func UnitCommand(u UnitFile) []string {
	if strings.HasSuffix(u.File, ".service") {
		for _, line := range strings.Split(u.Body, "\n") {
			v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart=")
			if !ok {
				continue
			}
			return strings.Fields(strings.TrimLeft(v, "-@:+!"))
		}
		return nil
	}
	return plistProgram(u.Body)
}

// plistProgram walks a plist for <key>ProgramArguments</key><array> of
// <string>, or <key>Program</key><string>. Comments are cut first: the play's
// headers hold "--", which encoding/xml refuses inside a comment.
func plistProgram(body string) []string {
	d := xml.NewDecoder(strings.NewReader(stripXMLComments(body)))
	d.Strict = false
	var args []string
	program := ""
	lastKey, inArray := "", false
	var text strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			text.Reset()
			if t.Name.Local == "array" && lastKey == "ProgramArguments" {
				inArray = true
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			switch t.Name.Local {
			case "key":
				lastKey = strings.TrimSpace(text.String())
				continue
			case "string":
				if inArray {
					args = append(args, strings.TrimSpace(text.String()))
				} else if lastKey == "Program" {
					program = strings.TrimSpace(text.String())
				}
			case "array":
				if inArray {
					return args
				}
			}
			if !inArray {
				lastKey = ""
			}
		}
	}
	if len(args) > 0 {
		return args
	}
	if program != "" {
		return []string{program}
	}
	return nil
}

// stripXMLComments drops every <!-- ... --> (an unclosed one to the end).
func stripXMLComments(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i+4:], "-->")
		if j < 0 {
			return b.String()
		}
		s = s[i+4+j+3:]
	}
}

// unitCommands are the command lines a process of the unit may carry: the
// whole argv, and the argv after each `--` (nova-loop <name> -- <cmd> and
// nova-secrets exec ... -- <cmd> exec into what follows).
func unitCommands(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	out := []string{strings.Join(argv, " ")}
	for i, a := range argv {
		if a == "--" && i+1 < len(argv) {
			out = append(out, strings.Join(argv[i+1:], " "))
		}
	}
	return out
}

// runsCommand reports whether a process's args are cmd, or cmd behind an
// interpreter (`bash /path/script --loop` for the unit's `/path/script --loop`).
func runsCommand(args, cmd string) bool {
	return cmd != "" && (args == cmd || strings.HasSuffix(args, " "+cmd))
}

// systemArgs are command lines the Old list never holds: the OS's own
// daemons and apps, login shells and the ssh session the user is in.
func systemArgs(args string) bool {
	for _, p := range []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/sbin/", "/usr/lib/", "/lib/systemd/",
		"/Library/Apple/", "/Applications/", "/usr/bin/ssh-agent", "(sd-pam)", "sshd", "-"} {
		if strings.HasPrefix(args, p) {
			return true
		}
	}
	return strings.Contains(args, ".app/Contents/")
}

// PSInput is everything one sample reads.
type PSInput struct {
	GOOS  string
	Out   string    // PSFullCommand's output
	At    time.Time // when ps ran
	UID   int       // the bench user
	Self  int       // the beat's pid: it, its ancestors and its ps are left out of Old
	Units []UnitFile
	User  func(uid int) string // uid to name; nil prints the number
}

// SamplePS reads one PSInput into the beat's sample. A ps output it cannot
// read is a sample with Err set, never an empty one.
func SamplePS(in PSInput) PSSample {
	s := PSSample{At: in.At.Unix(), Top: []PSProc{}, Old: []PSProc{}, Units: []PSUnit{}}
	rows, err := ParsePSRows(in.GOOS, in.Out)
	if err != nil {
		s.Err = err.Error()
		return s
	}
	user := in.User
	if user == nil {
		user = func(uid int) string { return strconv.Itoa(uid) }
	}
	proc := func(r PSRow) PSProc {
		return PSProc{PID: r.PID, CPU: r.CPU, User: user(r.UID), Start: s.At - int64(r.Age), Cmd: oneline.Cap(r.Args, PSCmdMax)}
	}

	// The declared units' command lines, and every unit's state.
	var declared []string
	for _, u := range in.Units {
		st := UnitUndeclared
		if UnitIsDeclared(u.Body) {
			st = UnitDeclared
			declared = append(declared, unitCommands(UnitCommand(u))...)
		}
		s.Units = append(s.Units, PSUnit{Name: UnitName(u.File), State: st})
	}
	sort.Slice(s.Units, func(i, j int) bool {
		if s.Units[i].State != s.Units[j].State {
			return s.Units[i].State == UnitUndeclared
		}
		return s.Units[i].Name < s.Units[j].Name
	})
	s.UnitN = len(s.Units)
	if len(s.Units) > PSUnitMax {
		s.Units = s.Units[:PSUnitMax]
	}

	// Which processes belong to a declared unit: a root whose args are a
	// declared command, then every descendant.
	parent := map[int]int{}
	for _, r := range rows {
		parent[r.PID] = r.PPID
	}
	root := map[int]bool{}
	for _, r := range rows {
		for _, c := range declared {
			if runsCommand(r.Args, c) {
				root[r.PID] = true
				break
			}
		}
	}
	owned := func(pid int) bool {
		for seen := 0; pid > 1 && seen < 64; seen++ {
			if root[pid] {
				return true
			}
			pid = parent[pid]
		}
		return false
	}
	self := map[int]bool{}
	for pid, seen := in.Self, 0; pid > 1 && seen < 64; seen++ {
		self[pid] = true
		pid = parent[pid]
	}

	var top, old []PSRow
	for _, r := range rows {
		if r.PPID == in.Self && strings.HasPrefix(r.Args, "ps ") {
			continue // the sample's own ps
		}
		top = append(top, r)
		if r.UID == in.UID && !self[r.PID] && !systemArgs(r.Args) && !owned(r.PID) {
			old = append(old, r)
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].CPU != top[j].CPU {
			return top[i].CPU > top[j].CPU
		}
		return top[i].PID < top[j].PID
	})
	sort.SliceStable(old, func(i, j int) bool {
		if old[i].Age != old[j].Age {
			return old[i].Age > old[j].Age
		}
		return old[i].PID < old[j].PID
	})
	for i := 0; i < len(top) && i < PSTopMax; i++ {
		s.Top = append(s.Top, proc(top[i]))
	}
	s.OldN = len(old)
	for i := 0; i < len(old) && i < PSOldMax; i++ {
		s.Old = append(s.Old, proc(old[i]))
	}
	return s
}

// PSBench is one bench as the verb reads it: its beat's fields and the
// last play (bench:<b> build_at, or --since).
type PSBench struct {
	Bench string
	Beat  map[string]string // bench:<b>:beat; empty when it does not beat
	Play  time.Time         // zero when unknown
	// PlayRaw is build_at as stored, printed when it does not parse.
	PlayRaw string
}

// PSAge is a duration in its two largest units: 3d4h, 2h5m, 7m3s, 12s.
func PSAge(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d, h, m, s := sec/86400, sec/3600%24, sec/60%60, sec%60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// sample reads a bench's sample, or the one line that says why it has none.
func (b PSBench) sample(verb string) (PSSample, string) {
	if len(b.Beat) == 0 {
		return PSSample{}, fmt.Sprintf("%s %s NOBEAT bench:%s:beat is absent", verb, b.Bench, b.Bench)
	}
	field, ok := b.Beat["ps"]
	if !ok || field == "" {
		return PSSample{}, fmt.Sprintf("%s %s NOSAMPLE the beat carries no ps (build %s)", verb, b.Bench, oneline.Field(b.Beat["build"]))
	}
	s, err := DecodePS(field)
	if err != nil {
		return PSSample{}, fmt.Sprintf("%s %s FAIL %s", verb, b.Bench, oneline.Escape(err.Error()))
	}
	if s.Err != "" {
		return PSSample{}, fmt.Sprintf("%s %s FAIL %s", verb, b.Bench, oneline.Escape(s.Err))
	}
	return s, ""
}

func procLine(bench, kind string, p PSProc, now int64) string {
	return fmt.Sprintf("%s %s pid=%d cpu=%.1f user=%s age=%s cmd=%s", bench, kind, p.PID, p.CPU,
		oneline.Field(p.User), PSAge(now-p.Start), oneline.Escape(p.Cmd))
}

// PSLines is `fleet ps` for one bench: the load line, the top processes, the
// undeclared units, and PS <bench> last. ok is false when the bench has no
// sample to read.
func PSLines(b PSBench, now time.Time) (lines []string, ok bool) {
	s, why := b.sample("PS")
	if why != "" {
		return []string{why}, false
	}
	cpu := b.Beat["cpu"]
	if cpu == "" {
		cpu = "?"
	}
	lines = append(lines, fmt.Sprintf("%s load1=%s ncpu=%s cpu=%s sample=%s", b.Bench,
		oneline.Field(orQ(b.Beat["load1"])), oneline.Field(orQ(b.Beat["ncpu"])), oneline.Field(cpu), PSAge(now.Unix()-s.At)))
	for _, p := range s.Top {
		lines = append(lines, procLine(b.Bench, "top", p, s.At))
	}
	undeclared := 0
	for _, u := range s.Units {
		if u.State == UnitUndeclared {
			undeclared++
			lines = append(lines, fmt.Sprintf("%s unit %s undeclared", b.Bench, oneline.Field(u.Name)))
		}
	}
	lines = append(lines, fmt.Sprintf("PS %s top=%d units=%d undeclared=%d%s", b.Bench, len(s.Top), s.UnitN, undeclared, cut(s.UnitN, len(s.Units), "units")))
	return lines, true
}

// StrayLines is `fleet ps --stray` for one bench: its undeclared units, its
// processes outside every declared unit that started before the last play,
// and STRAY <bench> last. strays counts both; ok is false when the bench has
// no sample or no last play, so nothing about it can be called clean.
func StrayLines(b PSBench) (lines []string, strays int, ok bool) {
	s, why := b.sample("STRAY")
	if why != "" {
		return []string{why}, 0, false
	}
	units := 0
	for _, u := range s.Units {
		if u.State == UnitUndeclared {
			units++
			lines = append(lines, fmt.Sprintf("%s unit %s undeclared", b.Bench, oneline.Field(u.Name)))
		}
	}
	if b.Play.IsZero() {
		why := "is empty"
		if b.PlayRaw != "" {
			why = "is " + oneline.Field(b.PlayRaw) + ", not RFC 3339"
		}
		lines = append(lines, fmt.Sprintf("STRAY %s units=%d old=? no last play: bench:%s build_at %s; pass --since", b.Bench, units, b.Bench, why))
		return lines, units, false
	}
	play := b.Play.Unix()
	old := 0
	for _, p := range s.Old {
		if p.Start < play {
			old++
			lines = append(lines, procLine(b.Bench, "old", p, s.At))
		}
	}
	more := ""
	if old == len(s.Old) && s.OldN > len(s.Old) {
		// every carried process predates the play and the beat cut the rest
		more = fmt.Sprintf(" (the beat carries the oldest %d of %d)", len(s.Old), s.OldN)
	}
	lines = append(lines, fmt.Sprintf("STRAY %s units=%d old=%d play=%s%s%s", b.Bench, units, old,
		b.Play.UTC().Format(time.RFC3339), more, cut(s.UnitN, len(s.Units), "units")))
	return lines, units + old, true
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// cut says the beat dropped entries past its bound.
func cut(n, kept int, what string) string {
	if n > kept {
		return fmt.Sprintf(" (the beat carries %d of %d %s)", kept, n, what)
	}
	return ""
}
