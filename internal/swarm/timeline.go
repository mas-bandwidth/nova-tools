package swarm

// A CARD'S MINUTES ARE MEASURED PER PHASE (Glenn, 2026-09-17: "speed up average wall clock
// time per card; make each card operate more efficiently in tokens and in time from start to
// finish; look at single cards"). The harness log carried no timestamps, so nothing could say
// which phase -- clone, deps, read, edit, test, retry, result -- spent a card's wall.
//
// The harness REPORTS its own events on the child's output, one line each, and this reader
// timestamps them as they arrive: the harness says what happened, the run says when. One row
// is written per model turn and per tool call into `<job>/timeline.tsv`, beside the card's
// `usage.tsv`, so a fleet of cards can be folded into seconds per phase without re-reading a
// transcript.
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
