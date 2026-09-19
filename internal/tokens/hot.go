package tokens

import (
	"fmt"
	"sort"
	"strings"
)

// ReadRecord is one path a session read and how many times. A file read once is
// normal, and the same path read a hundred times is the heat the `hot` verb
// exists to find. Count is reads, never tokens and never bytes.
type ReadRecord struct {
	Path  string
	Count int64
}

// StepOutput is one tool step and the size of what it wrote, in bytes. A step's
// bytes are the one measurement a truncated result and an absent result still
// leave behind, but bytes are NOT tokens and the report says so. Tool and Action
// together name the step: a tool like `bash` and the action it ran, or a tool
// that has no action.
type StepOutput struct {
	Tool        string
	Action      string
	OutputBytes int64
}

// HotSessionAnalysis is what AnalyzeHotSession returns: the top repeated reads
// and the top step outputs, each sorted largest first.
type HotSessionAnalysis struct {
	TopReads   []ReadRecord
	TopOutputs []StepOutput
}

// AnalyzeHotSession ranks a session's repeated reads and its step outputs and
// keeps the top topN of each, largest first. A tie within one rank breaks on
// the path (for reads) or on the tool then the action (for outputs) so the same
// inputs give the same report every time.
func AnalyzeHotSession(reads []ReadRecord, outputs []StepOutput, topN int) HotSessionAnalysis {
	r := append([]ReadRecord(nil), reads...)
	sort.SliceStable(r, func(i, j int) bool {
		if r[i].Count != r[j].Count {
			return r[i].Count > r[j].Count
		}
		return r[i].Path < r[j].Path
	})
	r = firstN(r, topN)

	o := append([]StepOutput(nil), outputs...)
	sort.SliceStable(o, func(i, j int) bool {
		if o[i].OutputBytes != o[j].OutputBytes {
			return o[i].OutputBytes > o[j].OutputBytes
		}
		if o[i].Tool != o[j].Tool {
			return o[i].Tool < o[j].Tool
		}
		return o[i].Action < o[j].Action
	})
	o = firstN(o, topN)

	return HotSessionAnalysis{TopReads: r, TopOutputs: o}
}

// firstN is a topN bound that never slices past the end and refuses a negative
// bound.
func firstN[T any](xs []T, n int) []T {
	if n < 0 {
		n = 0
	}
	if n < len(xs) {
		xs = xs[:n]
	}
	return xs
}

// hotNoteCannotSee is the fixed tail of every HOT NOTE line: what the analysis
// measured and what it cannot, in one breath. Bytes are a proxy, a truncated or
// absent result hides its tail, cache and reasoning attach per message rather
// than per step, and a swarm job reclaimed back into a pool has no steps left to
// rank. The note ends on these so no caller mistakes the ranking for a total.
const hotNoteCannotSee = "bytes are not tokens; truncated results; absent outputs; cache and reasoning are per message; a reclaimed swarm job has no steps"

// FormatHotReport renders the analysis as one terminal HOT NOTE line. The line
// names what it ranked, then ends on hotNoteCannotSee, so the report never
// claims more than it can see.
func FormatHotReport(a HotSessionAnalysis) string {
	var b strings.Builder
	b.WriteString("HOT NOTE:")
	for _, r := range a.TopReads {
		fmt.Fprintf(&b, " %s %d reads;", r.Path, r.Count)
	}
	for _, o := range a.TopOutputs {
		fmt.Fprintf(&b, " %s %d bytes;", stepName(o), o.OutputBytes)
	}
	b.WriteString(" ")
	b.WriteString(hotNoteCannotSee)
	return b.String()
}

// stepName is the tool, or the tool and its action, as one token.
func stepName(o StepOutput) string {
	if o.Action == "" {
		return o.Tool
	}
	return o.Tool + "/" + o.Action
}
