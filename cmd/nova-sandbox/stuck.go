// The stuck verb: when a walled launch sits at exec with almost no CPU, say
// why. It is read-only -- it writes nothing, kills nothing and starts nothing --
// and it runs OUTSIDE the wall, on the host, because the launch it diagnoses is
// the one that cannot run inside it.
//
// The failure this closes was measured on the bench host on 2026-10-13: a walled
// opencode sat at exec with about 0 CPU and every take was idle-reaped, and
// nothing on the machine said which mach service the wall had refused. The verb
// finds the process, reads its stack and the window's sandbox denials, and names
// the denied mach-lookups in one line each.
//
// Everything here is data: the process table is `ps` text, the stack is
// /usr/bin/sample text and the denials are `log show` text, so the tests step
// the reader on recorded fixtures and run no process.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/mas-bandwidth/nova-tools/profiles"
)

// The defaults that select the bench host case: age 1-22:27:51 and CPU
// 0:00.01. A launch younger than min-age or busier than max-cpu is a launch
// that is working, not one that is stuck.
const (
	stuckMinAge = 30 * time.Second
	stuckMaxCPU = 1 * time.Second
	stuckWindow = 5 * time.Minute
)

// stuckOpts is one call's selection: which process, and the two bounds.
type stuckOpts struct {
	PID    int
	Match  string
	MinAge time.Duration
	MaxCPU time.Duration
}

// procRow is one row of the process table, in the five facts the verb prints
// plus the command.
type procRow struct {
	PID     int
	PPID    int
	ETime   time.Duration
	CPUTime time.Duration
	State   string
	Command string
}

// machDenial is one denied mach service grouped by name and process.
type machDenial struct {
	Name    string
	Process string
	Count   int
}

// stuckResult is the one value the verb builds: its line rendering and its JSON
// are the two renderings of this value.
type stuckResult struct {
	Unsupported    bool       `json:"unsupported"`
	Platform       string     `json:"platform"`
	ProductVersion string     `json:"product_version,omitempty"`
	Processes      []stuckPS  `json:"processes,omitempty"`
	Denied         []stuckDen `json:"denied,omitempty"`
	Verdicts       []string   `json:"verdicts,omitempty"`
}

// stuckPS is one process in the chain of a matching process, with
// the matching process's stack and verdict when it is the candidate.
type stuckPS struct {
	PID       int      `json:"pid"`
	PPID      int      `json:"ppid"`
	ETime     string   `json:"etime"`
	CPUTime   string   `json:"cputime"`
	State     string   `json:"state"`
	Command   string   `json:"command"`
	Candidate bool     `json:"candidate,omitempty"`
	Top       string   `json:"top,omitempty"`
	Stack     []string `json:"stack,omitempty"`
}

// stuckDen is one grouped denial.
type stuckDen struct {
	Name       string `json:"name"`
	Process    string `json:"process"`
	Count      int    `json:"count"`
	NotAllowed bool   `json:"not_allowed_by_profile"`
}

// sampleFrameRe pulls the symbol out of one /usr/bin/sample call-graph frame:
// the leading `+`, the sample count, then the symbol.
var sampleFrameRe = regexp.MustCompile(`^\s*\+\s*\d+\s+(\S+)`)

// sampleThreadRe matches a thread header in a call graph, which ends one
// thread's frames and begins the next.
var sampleThreadRe = regexp.MustCompile(`^\s*\d+\s+Thread_`)

// dupReportsRe reads the multiplier in `N duplicate reports for Sandbox: ...`.
var dupReportsRe = regexp.MustCompile(`(\d+)\s+duplicate reports for `)

// parsePSTime reads ps(1)'s `[[dd-]hh:]mm:ss[.ff]` into a duration. Both etime
// and time use it; time carries hundredths, which is why the seconds are parsed
// as a float.
func parsePSTime(s string) (time.Duration, error) {
	days := 0
	if i := strings.IndexByte(s, '-'); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, err
		}
		days = d
		s = s[i+1:]
	}
	parts := strings.Split(s, ":")
	scale := []float64{3600, 60, 1}
	if len(parts) > len(scale) {
		return 0, fmt.Errorf("too many fields in %q", s)
	}
	var secs float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, err
		}
		secs += v * scale[len(scale)-len(parts)+i]
	}
	return time.Duration((float64(days)*86400 + secs) * float64(time.Second)), nil
}

// parsePS reads the process table this verb asks ps(1) for:
// pid, ppid, etime, time, state and the rest as the command. A line that does
// not carry all of it is dropped, so a header or a stray line is harmless.
func parsePS(text string) []procRow {
	var out []procRow
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		etime, err := parsePSTime(f[2])
		if err != nil {
			continue
		}
		cpu, err := parsePSTime(f[3])
		if err != nil {
			continue
		}
		out = append(out, procRow{
			PID:     pid,
			PPID:    ppid,
			ETime:   etime,
			CPUTime: cpu,
			State:   f[4],
			Command: strings.Join(f[5:], " "),
		})
	}
	return out
}

// commandBase is the process name a denial is attributed to: the first word of
// the command, without its directory. A denial line names the process the way
// the kernel does, by its executable's base name.
func commandBase(command string) string {
	f := strings.Fields(command)
	if len(f) == 0 {
		return ""
	}
	return path.Base(f[0])
}

// isWrapper says whether a row is a nova-sandbox process: the parent chain of a
// walled launch holds one, and finding it is what an unfiltered search keys on.
func isWrapper(r procRow) bool { return strings.Contains(r.Command, "nova-sandbox") }

// selectStuck picks the rows a call asks for. With --pid it is that one row;
// with --match it is every row whose command contains the substring; with
// neither it is every row whose parent chain holds a nova-sandbox wrapper and
// which is not a wrapper itself. Every candidate is then held to the two bounds:
// age at least --min-age and cumulative CPU at most --max-cpu.
func selectStuck(rows []procRow, opts stuckOpts) []procRow {
	byPID := map[int]procRow{}
	for _, r := range rows {
		byPID[r.PID] = r
	}
	inWall := func(r procRow) bool {
		seen := map[int]bool{}
		for p := r.PPID; p != 0 && !seen[p]; {
			seen[p] = true
			parent, ok := byPID[p]
			if !ok {
				return false
			}
			if isWrapper(parent) {
				return true
			}
			p = parent.PPID
		}
		return false
	}
	var out []procRow
	for _, r := range rows {
		switch {
		case opts.PID != 0:
			if r.PID != opts.PID {
				continue
			}
		case opts.Match != "":
			if !strings.Contains(r.Command, opts.Match) {
				continue
			}
		default:
			if isWrapper(r) || !inWall(r) {
				continue
			}
		}
		if r.ETime < opts.MinAge || r.CPUTime > opts.MaxCPU {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// parseSample reads /usr/bin/sample's call graph: the top symbol is the DEEPEST
// frame of the main thread -- the call it is parked in, a mach_msg or
// bootstrap_look_up -- and the lines are the graph, bounded to forty.
func parseSample(text string) (top string, lines []string) {
	all := strings.Split(text, "\n")
	start := -1
	for i, l := range all {
		if strings.Contains(l, "Call graph:") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", nil
	}
	end := len(all)
	for i := start; i < len(all); i++ {
		if strings.HasPrefix(strings.TrimSpace(all[i]), "Total number in stack") {
			end = i
			break
		}
	}
	lines = all[start:end]
	if len(lines) > 40 {
		lines = lines[:40]
	}
	inMain := false
	for _, l := range all[start:end] {
		if sampleThreadRe.MatchString(l) {
			inMain = strings.Contains(l, "com.apple.main-thread")
			continue
		}
		if inMain {
			if m := sampleFrameRe.FindStringSubmatch(l); m != nil {
				top = m[1]
			}
		}
	}
	return top, lines
}

// parseMachDenials reads the mach-lookup denials out of `log show` text and
// groups them by name and process. The `N duplicate reports for` prefix is the
// kernel's own count and multiplies the group.
func parseMachDenials(logText string) []machDenial {
	counts := map[[2]string]int{}
	for _, line := range strings.Split(logText, "\n") {
		mult := 1
		if m := dupReportsRe.FindStringSubmatch(line); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				mult = n
			}
		}
		_, after, ok := strings.Cut(line, "Sandbox: ")
		if !ok {
			continue
		}
		name, rest, ok := strings.Cut(after, " deny(")
		if !ok {
			continue
		}
		_, rest, ok = strings.Cut(rest, ") ")
		if !ok {
			continue
		}
		op, svc, ok := strings.Cut(rest, " ")
		if !ok {
			continue
		}
		if strings.TrimSpace(op) != "mach-lookup" {
			continue
		}
		proc := denierName(name)
		if proc == "" {
			continue
		}
		counts[[2]string{strings.TrimSpace(svc), proc}] += mult
	}
	out := make([]machDenial, 0, len(counts))
	for key, n := range counts {
		out = append(out, machDenial{Name: key[0], Process: key[1], Count: n})
	}
	sortDenials(out)
	return out
}

// denierName reads the process name out of the `name(pid)` a sandbox line
// carries, which is the name a denial is attributed to.
func denierName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "("); i >= 0 {
		return strings.TrimSpace(name[:i])
	}
	return name
}

// sortDenials orders the grouped denials by name then process, so two runs over
// the same window print the same lines in the same order.
func sortDenials(d []machDenial) {
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].Name != d[j].Name {
			return d[i].Name < d[j].Name
		}
		return d[i].Process < d[j].Process
	})
}

// profileMachNames is the mach-lookup list the profile text embeds: every
// global-name in it, read from the text the binary carries rather than a copy.
// This is what marks a denied name `not-allowed-by-profile`.
func profileMachNames(template string) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(template, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ";;") {
			continue
		}
		i := strings.Index(line, "global-name")
		if i < 0 {
			continue
		}
		line = line[i:]
		open := strings.IndexByte(line, '"')
		if open < 0 {
			continue
		}
		rest := line[open+1:]
		close := strings.IndexByte(rest, '"')
		if close < 0 {
			continue
		}
		names[rest[:close]] = true
	}
	return names
}

// formatPSDuration prints a duration the way ps(1) does: `mm:ss.ff` under an
// hour, `hh:mm:ss` under a day, `dd-hh:mm:ss` above it.
func formatPSDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute
	d -= mins * time.Minute
	secs := d / time.Second
	centis := (d - secs*time.Second) / (10 * time.Millisecond)
	switch {
	case days > 0:
		return fmt.Sprintf("%d-%02d:%02d:%02d", days, hours, mins, secs)
	case hours > 0:
		return fmt.Sprintf("%d:%02d:%02d", hours, mins, secs)
	default:
		return fmt.Sprintf("%d:%02d.%02d", mins, secs, centis)
	}
}

// deniedFor picks the denial a verdict line names for a process: the first
// grouped denial attributed to the process's own base name, and none when the
// window held no denial for it (so the next place to look is the stack).
func deniedFor(p procRow, denied []machDenial) string {
	base := commandBase(p.Command)
	for _, d := range denied {
		if d.Process == base {
			return fmt.Sprintf("mach-lookup:%s x%d", d.Name, d.Count)
		}
	}
	return ""
}

// verdictTail is the tail of the verdict line: pid, state, cpu, age, the top
// stack symbol, and the denied operation. `denied=none` means no denial was in
// the window, so the stack is where to look next.
func verdictTail(p procRow, top string, denied []machDenial) string {
	tail := fmt.Sprintf("pid=%d state=%s cpu=%s age=%s top=%s",
		p.PID, p.State, formatPSDuration(p.CPUTime), formatPSDuration(p.ETime), top)
	if op := deniedFor(p, denied); op != "" {
		return tail + " denied=" + op
	}
	return tail + " denied=none"
}

// stuckBuild is the whole verb as one value. The platform, the text of the
// process table, the stack per pid, the log, the profile and the host version
// are all arguments, so a test steps it with recorded text and no process.
func stuckBuild(cfg stuckOpts, goos, psText string, sample func(int) string, logText, profile, productVersion string) stuckResult {
	res := stuckResult{Platform: goos, ProductVersion: productVersion}
	if goos != "darwin" {
		res.Unsupported = true
		return res
	}
	rows := parsePS(psText)
	byPID := map[int]procRow{}
	for _, r := range rows {
		byPID[r.PID] = r
	}
	allowed := profileMachNames(profile)
	denied := parseMachDenials(logText)
	for _, d := range denied {
		res.Denied = append(res.Denied, stuckDen{
			Name:       d.Name,
			Process:    d.Process,
			Count:      d.Count,
			NotAllowed: !allowed[d.Name],
		})
	}
	for _, p := range selectStuck(rows, cfg) {
		top, stack := parseSample(sample(p.PID))
		res.Processes = append(res.Processes, stuckPS{
			PID: p.PID, PPID: p.PPID,
			ETime: formatPSDuration(p.ETime), CPUTime: formatPSDuration(p.CPUTime),
			State: p.State, Command: p.Command,
			Candidate: true, Top: top, Stack: stack,
		})
		res.Verdicts = append(res.Verdicts, "STUCK LAUNCH "+verdictTail(p, top, denied))
	}
	return res
}

// renderStuck prints the one value as lines. The DENIED lines come before the
// verdicts, and the verdicts are the last lines.
func renderStuck(w io.Writer, res stuckResult) {
	if res.Unsupported {
		fmt.Fprintf(w, "STUCK UNSUPPORTED platform=%s: sample and the unified log are darwin's\n", oneline.Field(res.Platform))
		return
	}
	if len(res.Processes) == 0 {
		fmt.Fprintln(w, "STUCK NONE")
		fmt.Fprintf(w, "STUCK HOST productVersion=%s\n", oneline.Field(res.ProductVersion))
		return
	}
	for _, d := range res.Denied {
		mark := ""
		if d.NotAllowed {
			mark = " not-allowed-by-profile"
		}
		fmt.Fprintf(w, "DENIED mach-lookup name=%s process=%s count=%d%s\n",
			oneline.Field(d.Name), oneline.Field(d.Process), d.Count, mark)
	}
	fmt.Fprintf(w, "STUCK HOST productVersion=%s\n", oneline.Field(res.ProductVersion))
	for i, p := range res.Processes {
		fmt.Fprintf(w, "PS pid=%d ppid=%d etime=%s cputime=%s state=%s command=%s\n",
			p.PID, p.PPID, p.ETime, p.CPUTime, oneline.Field(p.State), oneline.Quote(p.Command))
		for _, f := range p.Stack {
			if strings.TrimSpace(f) == "" {
				continue
			}
			fmt.Fprintf(w, "STACK pid=%d %s\n", p.PID, f)
		}
		fmt.Fprintln(w, res.Verdicts[i])
	}
}

// builtinProfile is the profile text the binary carries, read at run time so a
// name the profile allows is not marked against it.
func builtinProfile() string { return profiles.DarwinTemplate }

// stuckFlagNames is every flag the verb takes, in the order an unknown flag is
// answered with. The verb parses its flags by hand, the way the bare form's
// parseVerb does, so the package keeps the one flag set its help seam builds
// (docs/STANDARD.md section 3 lives in verbhelp.go until the port).
var stuckFlagNames = []string{"--pid", "--match", "--min-age", "--max-cpu", "--window", "--json"}

// parseStuckFlags reads the verb's argv without the flag package: one or two
// dashes, `--name value` or `--name=value`, the first positional (or `--`)
// ending the flags. A repeated flag keeps its last value. The returned string
// is the refusal's reason text, empty when the flags parsed.
func parseStuckFlags(args []string) (cfg stuckOpts, window time.Duration, asJSON bool, positionals []string, refusal string) {
	cfg = stuckOpts{MinAge: stuckMinAge, MaxCPU: stuckMaxCPU}
	window = stuckWindow
	bad := func(format string, a ...any) (stuckOpts, time.Duration, bool, []string, string) {
		return cfg, window, asJSON, nil, fmt.Sprintf(format, a...)
	}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		name := strings.TrimLeft(a, "-")
		value, attached := "", false
		if cut, rest, ok := strings.Cut(name, "="); ok {
			name, value, attached = cut, rest, true
		}
		next := func() (string, bool) {
			if attached {
				return value, true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch "--" + name {
		case "--pid":
			v, ok := next()
			if !ok {
				return bad("--pid wants a value: --pid <n>")
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return bad("invalid value %q for --pid: want an integer process id", oneline.Escape(v))
			}
			cfg.PID = n
		case "--match":
			v, ok := next()
			if !ok {
				return bad("--match wants a value: --match <substring>")
			}
			cfg.Match = v
		case "--min-age", "--max-cpu", "--window":
			v, ok := next()
			if !ok {
				return bad("--%s wants a value: --%s <d>", name, name)
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return bad("invalid value %q for --%s: want a Go duration like 30s", oneline.Escape(v), name)
			}
			switch "--" + name {
			case "--min-age":
				cfg.MinAge = d
			case "--max-cpu":
				cfg.MaxCPU = d
			default:
				window = d
			}
		case "--json":
			if !attached {
				asJSON = true
				continue
			}
			b, err := strconv.ParseBool(value)
			if err != nil {
				return bad("invalid value %q for --json: want true or false", oneline.Escape(value))
			}
			asJSON = b
		default:
			return bad("unknown flag --%s: the flags are %s; run: nova-sandbox stuck -h",
				oneline.Escape(name), strings.Join(stuckFlagNames, ", "))
		}
	}
	return cfg, window, asJSON, args[i:], ""
}

// stuckVerb is the verb: it parses its flags, gathers the host's data outside
// the wall and prints the one value. It writes nothing, kills nothing and
// starts nothing but the read-only probes (`ps`, `sample`, `log`, `sw_vers`).
// Implements docs/SPEC-SANDBOX.md "The stuck verb — what a launch that sits at exec was denied".
func stuckVerb(args []string, stdout, stderr io.Writer) int {
	helpIfAsked(args, "stuck")
	cfg, window, asJSON, positionals, refusal := parseStuckFlags(args)
	if refusal != "" {
		fmt.Fprintf(stderr, "STUCK REFUSED reason=bad_flag: %s; run: nova-sandbox stuck -h\n", oneline.Escape(refusal))
		return sandbox.ExitCannotRun
	}
	if len(positionals) > 0 {
		fmt.Fprintf(stderr, "STUCK REFUSED reason=unknown_arg: stuck takes flags and no arguments, got %q; run: nova-sandbox stuck -h\n", oneline.Escape(positionals[0]))
		return sandbox.ExitCannotRun
	}
	if cfg.PID < 0 || cfg.MinAge < 0 || cfg.MaxCPU < 0 || window <= 0 {
		fmt.Fprintln(stderr, "STUCK REFUSED reason=bad_flag: --pid, --min-age and --max-cpu are not negative and --window is positive; run: nova-sandbox stuck -h")
		return sandbox.ExitCannotRun
	}
	if runtime.GOOS != "darwin" {
		renderStuck(stdout, stuckBuild(stuckOpts{}, runtime.GOOS, "", func(int) string { return "" }, "", "", ""))
		return 0
	}
	psOut, err := psText()
	if err != nil {
		fmt.Fprintf(stderr, "STUCK REFUSED reason=probe_failed: ps could not be read: %s; run: nova-sandbox stuck -h\n", oneline.Err(err))
		return sandbox.ExitCannotRun
	}
	res := stuckBuild(cfg, runtime.GOOS, psOut, sampleText, logText(window), builtinProfile(), swVersText())
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		// ignored: a closed stdout has no reader to tell, and the verb has done its work
		_ = enc.Encode(res)
		return 0
	}
	renderStuck(stdout, res)
	return 0
}
