package swarm

// A CARD'S MINUTES ARE MEASURED PER PHASE (Glenn, 2026-09-17: "speed up average wall clock
// time per card; make each card operate more efficiently in tokens and in time from start to
// finish; look at single cards"). The harness log carried no timestamps, so nothing could say
// which phase -- clone, deps, read, edit, test, retry, result -- spent a card's wall.
//
// The harness REPORTS its own events on the child's output, one line each, and this reader
// timestamps them as they arrive: the harness says what happened, the run says when. One row
// is written per model turn and per tool call into `<job>/timeline.tsv`, beside the card's
// `usage.tsv`, so `nova-swarm profile --jobs <glob>` can fold a fleet of cards into seconds
// per phase without re-reading a transcript.
//
// The event grammar is the harness adapter's, and it is deliberately two lines per event so
// both ends of a span are observed rather than guessed:
//
//	NOVA-TIMELINE TURN BEGIN
//	NOVA-TIMELINE TURN END in=<n|-> out=<n|->
//	NOVA-TIMELINE TOOL BEGIN name=<tool> cmd=<command to the end of the line>
//	NOVA-TIMELINE TOOL END name=<tool> rc=<n>
//
// A field the harness does not report is the empty string in the row, never a zero.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TimelineFileName is the per-turn timeline a native run writes beside a card's RESULT.md
// and usage.tsv. It is one header line and one row per model turn and per tool call.
const TimelineFileName = "timeline.tsv"

// TimelineColumns are the six columns of one card's timeline.tsv, in this order: the span's
// two ends, what ran, its wall in milliseconds, and the turn's token counts (empty where the
// harness reported none).
var TimelineColumns = []string{
	"t_start", "t_end", "tool", "wall_ms", "input_tokens", "output_tokens",
}

// TimelineRow is one observed event: a model turn or a tool call.
type TimelineRow struct {
	Start        time.Time
	End          time.Time
	Tool         string // `model` for a turn, else the tool name and command
	InputTokens  string // empty when the harness reported none
	OutputTokens string
}

// phaseOrder is the fixed order the profile line and its summary print their phases.
var phaseOrder = []string{"clone", "deps", "read", "edit", "test", "retry", "result"}

// Timeline is an io.Writer over the child's output: it timestamps the harness's own
// NOVA-TIMELINE report lines as they arrive and keeps one row per completed span. It is
// safe for the child's concurrent stdout and stderr copies; a partial line is held until
// its newline arrives.
type Timeline struct {
	mu   sync.Mutex
	buf  []byte
	rows []TimelineRow
	open *timelineSpan
}

type timelineSpan struct {
	start    time.Time
	kind     string // "turn" | "tool"
	name     string
	cmd      string
	haveMeta bool
}

// NewTimeline returns an empty timeline recorder.
func NewTimeline() *Timeline { return &Timeline{} }

// Write accepts the child's output, one chunk at a time, and observes every complete line.
func (t *Timeline) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		line := string(t.buf[:i])
		t.buf = t.buf[i+1:]
		t.observe(line)
	}
	return len(p), nil
}

// Rows returns a copy of the spans observed so far, in report order.
func (t *Timeline) Rows() []TimelineRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TimelineRow, len(t.rows))
	copy(out, t.rows)
	return out
}

// observe parses one line. A line that is not a NOVA-TIMELINE report is ignored; a BEGIN
// with a span already open closes that one at this line's instant (a harness reporting two
// spans with no end between them is read as one span ending where the next began).
func (t *Timeline) observe(line string) {
	line = strings.TrimSpace(line)
	const prefix = "NOVA-TIMELINE "
	if !strings.HasPrefix(line, prefix) {
		return
	}
	rest := strings.TrimPrefix(line, prefix)
	kind, rest, ok := cutWord(rest)
	if !ok {
		return
	}
	verb, meta, _ := cutWord(rest)
	kind = strings.ToLower(kind)
	verb = strings.ToUpper(verb)
	now := time.Now()
	switch verb {
	case "BEGIN":
		if t.open != nil {
			t.close(now, "", "", "")
		}
		sp := &timelineSpan{start: now, kind: kind, haveMeta: true}
		sp.name = metaValue(meta, "name")
		sp.cmd = cmdValue(meta)
		t.open = sp
	case "END":
		if t.open == nil {
			return
		}
		k := kind
		if k == "" {
			k = t.open.kind
		}
		if k != t.open.kind {
			return
		}
		if name := metaValue(meta, "name"); name != "" {
			t.open.name = name
		}
		t.close(now, metaValue(meta, "in"), metaValue(meta, "out"), metaValue(meta, "rc"))
	}
}

// close finishes the open span at end and appends its row.
func (t *Timeline) close(end time.Time, in, out, rc string) {
	sp := t.open
	t.open = nil
	if sp == nil {
		return
	}
	row := TimelineRow{Start: sp.start, End: end}
	if sp.kind == "turn" {
		row.Tool = "model"
		row.InputTokens = tokenOrEmpty(in)
		row.OutputTokens = tokenOrEmpty(out)
	} else {
		tool := strings.TrimSpace(sp.name)
		if sp.cmd != "" {
			tool = strings.TrimSpace(tool + " " + sp.cmd)
		}
		if tool == "" {
			tool = "tool"
		}
		if rc != "" {
			tool += " rc=" + rc
		}
		row.Tool = tool
	}
	t.rows = append(t.rows, row)
}

// cutWord splits the first whitespace-delimited word off s, and reports whether one was
// there.
func cutWord(s string) (word, rest string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:]), true
	}
	return s, "", true
}

// metaValue reads one `key=value` token whose value holds no spaces.
func metaValue(s, key string) string {
	for _, tok := range strings.Fields(s) {
		if v, found := strings.CutPrefix(tok, key+"="); found {
			return v
		}
	}
	return ""
}

// cmdValue reads `cmd=` to the end of the line, because a command carries spaces.
func cmdValue(s string) string {
	i := strings.Index(s, "cmd=")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(s[i+len("cmd="):])
}

// tokenOrEmpty keeps a token count the harness gave, and reads `-` (the harness's own word
// for "did not report") as an absence, never a zero.
func tokenOrEmpty(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == Dash {
		return ""
	}
	return v
}

// WriteTimeline writes one card's timeline.tsv atomically: the header line and one row per
// event, in order. A field is scrubbed of tabs and newlines so the row is always one row.
func WriteTimeline(path string, rows []TimelineRow) error {
	var b strings.Builder
	b.WriteString(strings.Join(TimelineColumns, "\t"))
	b.WriteByte('\n')
	for _, r := range rows {
		b.WriteString(strings.Join([]string{
			r.Start.UTC().Format(time.RFC3339Nano),
			r.End.UTC().Format(time.RFC3339Nano),
			scrubCell(r.Tool),
			strconv.FormatInt(r.End.Sub(r.Start).Milliseconds(), 10),
			scrubCell(r.InputTokens),
			scrubCell(r.OutputTokens),
		}, "\t"))
		b.WriteByte('\n')
	}
	return writeAtomic(path, []byte(b.String()), 0o644)
}

func scrubCell(v string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(strings.TrimSpace(v))
}

// ReadTimeline reads one card's timeline.tsv, mapping its columns by the header so a reader
// is not wedded to a column's position. An absent file is an empty timeline and no error,
// which is how a job that reported nothing phases as nothing rather than failing the fold.
func ReadTimeline(path string) ([]TimelineRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}
	head := strings.Split(lines[0], "\t")
	at := map[string]int{}
	for i, c := range head {
		at[strings.TrimSpace(c)] = i
	}
	var rows []TimelineRow
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cells := strings.Split(line, "\t")
		row := TimelineRow{
			Tool:         cell(cells, at, "tool"),
			InputTokens:  cell(cells, at, "input_tokens"),
			OutputTokens: cell(cells, at, "output_tokens"),
		}
		row.Start, _ = time.Parse(time.RFC3339Nano, cell(cells, at, "t_start"))
		row.End, _ = time.Parse(time.RFC3339Nano, cell(cells, at, "t_end"))
		rows = append(rows, row)
	}
	return rows, nil
}

func cell(cells []string, at map[string]int, name string) string {
	i, ok := at[name]
	if !ok || i < 0 || i >= len(cells) {
		return ""
	}
	return cells[i]
}

// JobProfile is one job's folded timeline: its span, how many turns and tool calls it
// reported, and the seconds each phase spent.
type JobProfile struct {
	Label   string
	Wall    float64
	Turns   int
	Tools   int
	Seconds map[string]float64
}

// ProfileJobs reads every timeline a job glob names and prints one PROFILE line per job and
// one PROFILE SUMMARY line with the mean per phase. A glob match may be a job directory (its
// timeline.tsv is read) or the timeline file itself; a match with no readable timeline is
// skipped. The command exits 0 even when the glob matches nothing, printing a zero summary,
// because an empty fleet is a measurement and not a refusal.
func ProfileJobs(pattern string, stdout, stderr io.Writer) int {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm profile: --jobs wants a glob, got %q: %s\n", pattern, err)
		return 2
	}
	sort.Strings(matches)

	var jobs []JobProfile
	for _, m := range matches {
		path := m
		label := filepath.Base(m)
		if fi, statErr := os.Stat(m); statErr == nil && fi.IsDir() {
			path = filepath.Join(m, TimelineFileName)
			label = filepath.Base(filepath.Clean(m))
		} else if statErr == nil {
			label = filepath.Base(filepath.Dir(m))
		}
		rows, readErr := ReadTimeline(path)
		if readErr != nil || len(rows) == 0 {
			continue
		}
		jobs = append(jobs, profileRows(label, rows))
	}

	phaseMeans := map[string]float64{}
	var wallSum float64
	for _, j := range jobs {
		fmt.Fprintln(stdout, profileLine(j))
		wallSum += j.Wall
		for _, ph := range phaseOrder {
			phaseMeans[ph] += j.Seconds[ph]
		}
	}
	n := len(jobs)
	mean := func(total float64) float64 {
		if n == 0 {
			return 0
		}
		return total / float64(n)
	}
	summary := fmt.Sprintf("PROFILE SUMMARY jobs=%d mean_wall=%.1f", n, mean(wallSum))
	for _, ph := range phaseOrder {
		summary += fmt.Sprintf(" %s=%.1f", ph, mean(phaseMeans[ph]))
	}
	fmt.Fprintln(stdout, summary)
	return 0
}

// profileLine renders one job's PROFILE line in the fixed phase order.
func profileLine(j JobProfile) string {
	line := fmt.Sprintf("PROFILE job=%s wall=%.1f turns=%d tools=%d",
		scrubCell(j.Label), j.Wall, j.Turns, j.Tools)
	for _, ph := range phaseOrder {
		line += fmt.Sprintf(" %s=%.1f", ph, j.Seconds[ph])
	}
	return line
}

// profileRows folds one job's rows: the wall is the span from the first start to the last
// end, and each tool call's seconds land in the phase its command names. A test run that
// follows a failing test run is retry, and only the first `go build` is a deps cost.
func profileRows(label string, rows []TimelineRow) JobProfile {
	j := JobProfile{Label: label, Seconds: map[string]float64{}}
	var first, last time.Time
	failedTest := false
	sawBuild := false
	for _, r := range rows {
		if first.IsZero() || r.Start.Before(first) {
			first = r.Start
		}
		if r.End.After(last) {
			last = r.End
		}
		if r.Tool == "model" || r.Tool == "" {
			j.Turns++
			continue
		}
		j.Tools++
		phase := phaseOfTool(r.Tool, &sawBuild)
		if phase == "test" && failedTest {
			phase = "retry"
		}
		if phase == "test" {
			failedTest = strings.Contains(r.Tool, "rc=") && !strings.Contains(r.Tool, "rc=0")
		}
		if phase != "" {
			j.Seconds[phase] += r.End.Sub(r.Start).Seconds()
		}
	}
	if !first.IsZero() && last.After(first) {
		j.Wall = last.Sub(first).Seconds()
	}
	return j
}

// phaseOfTool names the phase a tool call's command belongs to, or "" when no listed phase
// owns it. The checks run most-specific first: a RESULT.md write is the result even though
// it is also an edit, and a test is a test before it is a read.
func phaseOfTool(tool string, sawBuild *bool) string {
	s := strings.ToLower(tool)
	switch {
	case strings.Contains(s, "result.md"):
		return "result"
	case strings.Contains(s, "git clone") || strings.Contains(s, "git fetch"):
		return "clone"
	case strings.Contains(s, "go test") || strings.Contains(s, "run-tests.sh") || strings.Contains(s, "make test"):
		return "test"
	case strings.Contains(s, "go mod"):
		return "deps"
	case strings.Contains(s, "go build"):
		if !*sawBuild {
			*sawBuild = true
			return "deps"
		}
		return ""
	case strings.Contains(s, "grep") || strings.Contains(s, "cat ") || strings.Contains(s, "sed -n") ||
		strings.HasPrefix(s, "read"):
		return "read"
	case strings.HasPrefix(s, "edit") || strings.HasPrefix(s, "write"):
		return "edit"
	}
	return ""
}
