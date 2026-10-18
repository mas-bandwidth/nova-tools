package roadmap

import (
	"fmt"
	"sort"
	"strings"
)

// MindmapItemsPerGroup bounds the items a group shows in the mindmap: a group of
// hundreds of items would draw an unreadable map, so the rest are counted on one
// "+N more" leaf, and the full list stays in the tables below.
const MindmapItemsPerGroup = 12

// RenderWithDiagrams returns the page Render makes with an "At a glance" section
// of Mermaid diagrams after the opening paragraph (tools/roadmap --diagrams). GitHub
// renders the blocks natively; the same text renders with Pele on the sprint
// dashboard. Every other byte is Render's, so opting in moves only that section.
func RenderWithDiagrams(doc *Doc, source string) string { return render(doc, source, true) }

// Diagrams returns the "At a glance" section: only the diagrams the data supports,
// the same bytes for the same document.
//
//   - Done and remaining, a pie, when the document records done or scheduled work.
//   - Items by group, a pie, when there is more than one group.
//   - Groups and items, a mindmap, always.
//   - Items by date, a timeline, when the items carry more than one date.
func Diagrams(doc *Doc) string {
	var b strings.Builder
	b.WriteString("\n## At a glance\n\nGenerated from the same data as the tables below.\n")
	count := doc.GroupCount()
	if len(doc.Done) > 0 || len(doc.Scheduled) > 0 {
		b.WriteString("\n```mermaid\npie title Done and remaining\n")
		for _, s := range []struct {
			label string
			n     int
		}{{"Done", len(doc.Done)}, {"Scheduled in a release", len(doc.Scheduled)}, {"On the roadmap", len(doc.Items)}} {
			if s.n > 0 {
				fmt.Fprintf(&b, "    %q : %d\n", s.label, s.n)
			}
		}
		b.WriteString("```\n")
	}
	if len(doc.Groups) > 1 {
		b.WriteString("\n```mermaid\npie title Items by group\n")
		for _, g := range doc.Groups {
			fmt.Fprintf(&b, "    \"%s\" : %d\n", Label(g.Title), count[g.ID])
		}
		b.WriteString("```\n")
	}
	b.WriteString("\n```mermaid\nmindmap\n")
	fmt.Fprintf(&b, "  root((\"%s\"))\n", Label(doc.Title))
	for gi, g := range doc.Groups {
		fmt.Fprintf(&b, "    g%d[\"%s (%d)\"]\n", gi+1, Label(g.Title), count[g.ID])
		shown := 0
		for _, it := range doc.Items {
			if it.Group != g.ID {
				continue
			}
			if shown == MindmapItemsPerGroup {
				fmt.Fprintf(&b, "      g%dmore[\"+%d more\"]\n", gi+1, count[g.ID]-shown)
				break
			}
			shown++
			fmt.Fprintf(&b, "      g%di%d[\"%s\"]\n", gi+1, shown, Label(it.Title))
		}
	}
	b.WriteString("```\n")
	byDate := map[string]int{}
	for _, it := range doc.Items {
		byDate[it.Date]++
	}
	if len(byDate) > 1 {
		dates := make([]string, 0, len(byDate))
		for d := range byDate {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		b.WriteString("\n```mermaid\ntimeline\n    title Items by the date they were recorded\n")
		for _, d := range dates {
			fmt.Fprintf(&b, "    %s : %d %s\n", d, byDate[d], plural(byDate[d], "item", "items"))
		}
		b.WriteString("```\n")
	}
	return b.String()
}

// labelSwap replaces the characters that end or reinterpret a quoted Mermaid label
// with look-alikes that do not: a double quote ends the label, a backtick opens a
// markdown string, # opens an entity code (#quot;), < and > read as HTML, and
// braces and brackets end node shapes in parsers that ignore the quotes. Entity
// codes are not used because pie, mindmap and timeline decode them unevenly across
// renderers (GitHub's Mermaid and Pele), and a look-alike reads the same in both.
var labelSwap = strings.NewReplacer(
	`"`, "'",
	"`", "'",
	"#", "＃",
	"<", "‹",
	">", "›",
	"{", "(",
	"}", ")",
	"[", "(",
	"]", ")",
	`\`, "/",
	";", ",",
)

// Label returns s as the text of a quoted Mermaid label: whitespace joined into
// one blank (so no newline ends the line), then labelSwap applied.
func Label(s string) string { return labelSwap.Replace(flat(s)) }
