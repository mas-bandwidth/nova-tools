package tokens

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Friction is a recorded stumble, kept beside the day files under its own lock. It is
// never folded into a day file and never fills a gap: the point of the record is that the
// gap stays NAMED, so a burn can be attributed to the issue it became instead of surfacing
// later as a number nobody can explain. A token cost is exact or rough (`~`); a rough cost
// is an estimate, and the only thing it may not carry is a claim it cost someone nothing.

// FrictionEntry is one recorded stumble: a 32-hex id, a token cost (exact or rough), the
// wall time it cost, the gap label it belongs to, the tool, the issue it became, and when
// it was recorded.
type FrictionEntry struct {
	ID          string
	Tokens      int64
	Rough       bool // the cost was recorded with a `~`, an estimate rather than a count
	WallSeconds int64 // wall clock in seconds; required to be > 0 when Rough
	Gap         string
	Tool        string
	Issue       string
	CreatedAt   string
}

// FrictionSummary is one gap's total, summed over every entry that named it.
type FrictionSummary struct {
	Gap         string
	Tokens      int64
	Occurrences int
}

const frictionColumns = 7

// ParseFrictionEntry reads one tab-separated line of the seven columns
// `id tokens wall_seconds gap tool issue created_at`. A line with any other number of
// columns, a non-hex id, a token cell that is not a count, a negative wall time, or a
// rough cost with no wall time is an error rather than a partial entry: a record nobody
// can trust is not a record.
func ParseFrictionEntry(line string) (*FrictionEntry, error) {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return nil, fmt.Errorf("friction entry is empty")
	}
	cells := strings.Split(line, "\t")
	if len(cells) != frictionColumns {
		return nil, fmt.Errorf("friction entry has %d columns, want %d", len(cells), frictionColumns)
	}
	id := cells[0]
	if !isLowercaseHex(id) || len(id) != 32 {
		return nil, fmt.Errorf("friction id %q is not 32 hex digits", id)
	}

	tokens, rough, err := parseFrictionTokens(cells[1])
	if err != nil {
		return nil, err
	}

	wall, err := parseFrictionWall(cells[2])
	if err != nil {
		return nil, err
	}
	if rough && wall <= 0 {
		return nil, fmt.Errorf("a rough token cost (%q) needs a wall_seconds above zero, got %q", cells[1], cells[2])
	}

	gap, tool, issue := cells[3], cells[4], cells[5]
	if gap == "" || tool == "" {
		return nil, fmt.Errorf("friction gap and tool are required")
	}
	if cells[6] == "" {
		return nil, fmt.Errorf("friction created_at is required")
	}
	return &FrictionEntry{
		ID:          id,
		Tokens:      tokens,
		Rough:       rough,
		WallSeconds: wall,
		Gap:         gap,
		Tool:        tool,
		Issue:       issue,
		CreatedAt:   cells[6],
	}, nil
}

// FormatFrictionEntry renders one entry as its one line, the inverse of ParseFrictionEntry.
func FormatFrictionEntry(e FrictionEntry) string {
	tokens := strconv.FormatInt(e.Tokens, 10)
	if e.Rough {
		tokens = "~" + tokens
	}
	wall := strconv.FormatInt(e.WallSeconds, 10)
	return strings.Join([]string{e.ID, tokens, wall, e.Gap, e.Tool, e.Issue, e.CreatedAt}, "\t")
}

// SummarizeFrictions groups entries by gap, sums each gap's token cost and counts how many
// entries fed it, and returns the summaries ordered by total tokens descending: the gap
// that cost the most comes first, which is what a reader of the report is there to see.
func SummarizeFrictions(entries []FrictionEntry) []FrictionSummary {
	byGap := make(map[string]*FrictionSummary)
	var order []string
	for _, e := range entries {
		s, ok := byGap[e.Gap]
		if !ok {
			s = &FrictionSummary{Gap: e.Gap}
			byGap[e.Gap] = s
			order = append(order, e.Gap)
		}
		s.Tokens += e.Tokens
		s.Occurrences++
	}
	out := make([]FrictionSummary, 0, len(order))
	for _, g := range order {
		out = append(out, *byGap[g])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Gap < out[j].Gap
	})
	return out
}

func parseFrictionTokens(s string) (int64, bool, error) {
	raw := s
	if strings.HasPrefix(s, "~") {
		s = s[1:]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("friction tokens %q is not a non-negative count", raw)
	}
	return n, raw != s, nil
}

func parseFrictionWall(s string) (int64, error) {
	if s == "" || s == Dash {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("friction wall_seconds %q is not a non-negative count", s)
	}
	return n, nil
}
