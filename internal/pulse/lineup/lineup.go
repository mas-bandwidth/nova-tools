// Package lineup is the coding sprint's preflight (#2562): the checks that tonight's
// sprint of 2026-09-22 found by losing cards to them, asked BEFORE the first card, one row
// per bench per check, RED or GREEN, and the verb exits 1 on any RED.
//
// It is the Go half of bin/sprint-lineup, the bench checks of the coding profile:
//
//	stage            STAGE OK W/W in 60 s or less (566 cards died at staging on the house benches)
//	push-credential  the bench's harvest holds a GitHub push credential (39 cards stopped at a wall with none)
//	results-root     the results root exists and is writable (351 reports were lost when output was the workdir)
//	finished-jobs    no finished job dir is left on the bench (job storage is deleted at card end)
//	version-<tool>   every nova tool on the bench is the wanted build
//	launcher         no launcher opens one ssh session per card (#2743; the Studio sshd wedged)
//
// The remote half is one shell script run over ONE `ssh <bench> bash -s` session per bench
// (ProbeScript): it prints FACT lines and nothing is decided there. Deciding is here, in Go,
// against the facts (Coding), so a fixture of FACT lines drives every rule without a bench.
// The probe is shell and not the bench's own nova-pulse on purpose: a bench on a stale build
// is exactly the bench whose nova-pulse cannot be trusted to answer.
//
// No evidence is not negative evidence: a fact the bench did not print is a RED row that
// says MISSING, never a GREEN one.
package lineup

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Status is a row's verdict.
type Status string

const (
	Green Status = "GREEN"
	Red   Status = "RED"
)

// Row is one printed line: LINEUP <bench> <check> <status> <detail>.
type Row struct {
	Bench  string
	Check  string
	Status Status
	Detail string // already key=value fields, each value through oneline.Field
}

// String is the row as printed.
func (r Row) String() string {
	s := fmt.Sprintf("LINEUP %s %s %s", oneline.Field(r.Bench), oneline.Field(r.Check), r.Status)
	if r.Detail != "" {
		s += " " + r.Detail
	}
	return s
}

// Facts is what one bench's probe printed: FACT<TAB>name<TAB>value, by name.
type Facts map[string]string

// ParseFacts reads the FACT lines out of a probe's output. The second result is whether the
// probe reached its closing `end` fact, which is how a script cut short (an ssh that died,
// a bench that rebooted) is told from a bench that answered.
func ParseFacts(out string) (Facts, bool) {
	f := Facts{}
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(raw, "\r"), "FACT\t")
		if !ok {
			continue
		}
		name, value, _ := strings.Cut(rest, "\t")
		f[name] = value
	}
	_, ended := f["end"]
	return f, ended
}

// Policy is what the coding profile wants of a bench.
type Policy struct {
	Want     string        // the nova build stamp every tool must carry (a substring of its version line)
	Tools    []string      // the tools whose version is checked
	StageMax time.Duration // the slowest STAGE OK W/W that passes; 60 s
	StageAge time.Duration // the oldest stage receipt that counts; 0 is no limit
}

// DefaultTools are the two nova bins a coding card's launch path runs on the bench.
var DefaultTools = []string{"nova-swarm", "nova-pulse"}

// Coding is the coding profile's bench rows for one bench, in a fixed order.
func Coding(bench string, f Facts, p Policy) []Row {
	rows := []Row{stageRow(bench, f, p), pushRow(bench, f), resultsRow(bench, f), jobsRow(bench, f)}
	tools := p.Tools
	if len(tools) == 0 {
		tools = DefaultTools
	}
	for _, t := range tools {
		rows = append(rows, versionRow(bench, t, f, p.Want))
	}
	return rows
}

// Unreachable is the one row a bench that did not answer gets in place of its checks.
func Unreachable(bench, why string) Row {
	return Row{Bench: bench, Check: "reach", Status: Red, Detail: "why=" + oneline.Field(oneline.Cap(strings.TrimSpace(why), 160))}
}

func field(k, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		v = "-"
	}
	return k + "=" + oneline.Field(oneline.Cap(v, 160))
}

func red(bench, check string, detail ...string) Row {
	return Row{Bench: bench, Check: check, Status: Red, Detail: strings.Join(detail, " ")}
}

func green(bench, check string, detail ...string) Row {
	return Row{Bench: bench, Check: check, Status: Green, Detail: strings.Join(detail, " ")}
}

// stageLine matches `STAGE OK 64/64 ...` and `STAGE FAIL 60/64 ...`.
var stageLine = regexp.MustCompile(`^STAGE\s+(\S+)\s+(\d+)/(\d+)\b(.*)$`)

// stageElapsed reads the wall a STAGE line carries: elapsed=<d> or `in <d>`, where <d> is a
// Go duration (42s, 1m3s) or a bare number of seconds.
var stageElapsed = regexp.MustCompile(`(?:elapsed=|\bin\s+)(\d+(?:\.\d+)?(?:ms|s|m|h)?(?:\d+(?:\.\d+)?(?:ms|s|m|h))*)`)

// Stage is a parsed STAGE line.
type Stage struct {
	OK      bool
	Staged  int
	Width   int
	Elapsed time.Duration
	HasWall bool
}

// ParseStage reads one STAGE line. The second result is false when the line is not one.
func ParseStage(line string) (Stage, bool) {
	m := stageLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Stage{}, false
	}
	s := Stage{OK: m[1] == "OK"}
	s.Staged, _ = strconv.Atoi(m[2])
	s.Width, _ = strconv.Atoi(m[3])
	if e := stageElapsed.FindStringSubmatch(m[4]); e != nil {
		if d, ok := parseWall(e[1]); ok {
			s.Elapsed, s.HasWall = d, true
		}
	}
	return s, true
}

func parseWall(s string) (time.Duration, bool) {
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), true
	}
	d, err := time.ParseDuration(s)
	return d, err == nil
}

func stageRow(bench string, f Facts, p Policy) Row {
	const check = "stage"
	max := p.StageMax
	if max <= 0 {
		max = 60 * time.Second
	}
	line, ok := f["stage"]
	if !ok {
		return red(bench, check, "got=MISSING", field("why", "the probe printed no stage fact"))
	}
	if strings.TrimSpace(line) == "" {
		return red(bench, check, "got=MISSING", field("receipt", f["stage_receipt"]), field("why", "no STAGE line: stage W cards on this bench first"))
	}
	s, parsed := ParseStage(line)
	if !parsed {
		return red(bench, check, field("got", line), field("why", "not a STAGE line"))
	}
	// A wall the lineup measured itself (the probe ran the stage) outranks the one the
	// line reports.
	if v, ok := f["stage_elapsed_s"]; ok && strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			s.Elapsed, s.HasWall = time.Duration(n)*time.Second, true
		}
	}
	got := fmt.Sprintf("got=%d/%d", s.Staged, s.Width)
	wall := "wall=-"
	if s.HasWall {
		wall = "wall=" + oneline.Field(s.Elapsed.String())
	}
	want := field("want", "OK W/W <=") + oneline.Field(max.String())
	switch {
	case !s.OK || s.Width == 0 || s.Staged != s.Width:
		return red(bench, check, got, wall, want)
	case !s.HasWall:
		return red(bench, check, got, wall, want, field("why", "the STAGE line carries no wall"))
	case s.Elapsed > max:
		return red(bench, check, got, wall, want)
	}
	if p.StageAge > 0 {
		if v, ok := f["stage_age_s"]; ok && strings.TrimSpace(v) != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || time.Duration(n)*time.Second > p.StageAge {
				return red(bench, check, got, wall, field("age", v+"s"), field("want", "receipt <=")+oneline.Field(p.StageAge.String()))
			}
		}
	}
	return green(bench, check, got, wall)
}

func pushRow(bench string, f Facts) Row {
	const check = "push-credential"
	v, ok := f["push_credential"]
	switch {
	case !ok:
		return red(bench, check, "got=MISSING")
	case strings.HasPrefix(v, "yes"):
		_, src, _ := strings.Cut(v, ":")
		return green(bench, check, field("source", src))
	}
	return red(bench, check, "got=none", field("want", "GH_TOKEN|gh auth token|git credential for github.com"))
}

func resultsRow(bench string, f Facts) Row {
	const check = "results-root"
	v, ok := f["results_root"]
	if !ok {
		return red(bench, check, "got=MISSING")
	}
	state, path, _ := strings.Cut(strings.TrimSpace(v), " ")
	if state == "ok" {
		return green(bench, check, field("path", path))
	}
	return red(bench, check, field("got", state), field("path", path), field("want", "a writable directory"))
}

func jobsRow(bench string, f Facts) Row {
	const check = "finished-jobs"
	v, ok := f["finished_jobs"]
	if !ok {
		return red(bench, check, "got=MISSING")
	}
	nstr, first, _ := strings.Cut(strings.TrimSpace(v), " ")
	n, err := strconv.Atoi(nstr)
	if err != nil {
		return red(bench, check, field("got", v), field("why", "not a count"))
	}
	if n == 0 {
		return green(bench, check, "got=0")
	}
	return red(bench, check, fmt.Sprintf("got=%d", n), field("first", first), field("want", "0 (job storage is deleted at card end)"))
}

func versionRow(bench, tool string, f Facts, want string) Row {
	check := "version-" + tool
	v, ok := f["version:"+tool]
	if !ok || strings.TrimSpace(v) == "" {
		return red(bench, check, "got=MISSING", field("want", want))
	}
	if strings.TrimSpace(want) != "" && strings.Contains(v, want) {
		return green(bench, check, field("got", v))
	}
	return red(bench, check, field("got", v), field("want", want))
}

// sshWord is an ssh or scp invocation as a command word: at the start of a command, after a
// separator, or after a subshell or substitution opener.
var sshWord = regexp.MustCompile(`(?:^|[;&|(\x60{]|\$\(|\s)(?:\S*/)?(?:ssh|scp)(?:\s|$)`)

// LintLauncher is the per-card ssh launcher check. A launcher is run once per card, so an
// ssh (or scp) it invokes is one session per card: with 64 cards that is 64 sessions to
// one sshd, which is what wedged the Studio. The batch path opens one session per bench
// for the whole deal (#2743). Comments do not count.
func LintLauncher(path, content string) Row {
	const bench, check = "fleet", "launcher"
	for i, raw := range strings.Split(content, "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if sshWord.MatchString(line) {
			return red(bench, check, field("file", path), fmt.Sprintf("line=%d", i+1), field("got", strings.TrimSpace(line)),
				field("want", "one session per bench per batch (#2743)"))
		}
	}
	return green(bench, check, field("file", path))
}

// stripComment drops a shell comment: a line whose first word starts with #, or the text
// after a # that follows whitespace. A # inside quotes is rare enough in a launcher that
// the lint errs toward reading MORE of the line, never less.
func stripComment(line string) string {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "#") {
		return ""
	}
	for i := 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

// Verdict is the closing line and the exit: 0 when every row is GREEN, 1 on any RED.
func Verdict(profile string, rows []Row) (string, int) {
	reds := 0
	var names []string
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Status == Red {
			reds++
			k := r.Bench + "/" + r.Check
			if !seen[k] {
				seen[k] = true
				names = append(names, k)
			}
		}
	}
	if reds == 0 {
		return fmt.Sprintf("LINEUP %s GREEN rows=%d", oneline.Field(profile), len(rows)), 0
	}
	sort.Strings(names)
	return fmt.Sprintf("LINEUP %s RED red=%d/%d %s", oneline.Field(profile), reds, len(rows), field("rows", strings.Join(names, ","))), 1
}
