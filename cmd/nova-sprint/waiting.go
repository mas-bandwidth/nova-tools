package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	verbClasses["waiting"] = classRead
	verbEffect["waiting"] = "inspection: reads the waiting cards and why each waits, writes nothing"
}

func (a *app) cmdWaiting(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("waiting")
	stream := fs.String("stream", "", "the waiting cards of one stream (default: every stream)")
	s, code := a.workSnapshot("waiting", fs, args, c, stderr)
	if code != 0 {
		return code
	}
	view := sprint.ClassifyWaiting(s, *stream)
	if c.json {
		b, _ := json.Marshal(view)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, card := range view.Cards {
		fmt.Fprintf(stdout, "WAITING %s stream=%s reason=%s head=%s length=%d\n", oneline.Escape(card.ID), oneline.Escape(card.Stream), oneline.Escape(card.Reason), oneline.Escape(card.Head), card.Length)
	}
	fmt.Fprintf(stdout, "WAITING OK cards=%d%s\n", len(view.Cards), countSummary(view.Counts))
	return 0
}

// countSummary renders one reason kind and its count for each, in name order,
// for the waiting summary line: " behind-sentinel=1 held=2 needs-missing=1".
func countSummary(counts map[string]int) string {
	kinds := slices.Collect(maps.Keys(counts))
	slices.Sort(kinds)
	var b strings.Builder
	for _, k := range kinds {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strconv.Itoa(counts[k]))
	}
	return b.String()
}
