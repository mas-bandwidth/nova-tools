// Companion view for nova-tools #223: browse shared moments without
// rewriting the record.
//
// View renders an explicitly selected sample of Markdown records into a
// static, chronological timeline on one machine. It is read-only: it opens
// each selected file, parses a card from it, and prints text. It never
// writes, seals, rolls up or deletes anything, and an excluded record is
// never even opened.
package play

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// viewRemedy is the second half of the MORE line: a cap with no remedy is
// censorship, so the line that stands for the hidden cards names the flag
// that shows them in the same breath.
const viewRemedy = "--max <n> raises the ceiling, --max 0 prints every card"

// viewTitleRunes bounds one card's title. A title is the first heading, and a
// heading is caller prose of any length; the timeline stays scannable because
// the card carries the source path beside it, where the whole text lives.
const viewTitleRunes = 120

// card is one record as the timeline shows it. Date and Author stay the word
// "unknown" when the record does not say: a viewer guesses nothing. Kind is
// echoed from the record alone — human, ai, summary, or whatever word the
// record carries — and is never inferred from the author.
type card struct {
	source     string // the path as it was selected, the link back
	title      string
	author     string
	date       string // as displayed, or "unknown"
	kind       string
	supersedes string // "-" when the record names none
	at         time.Time
	known      bool // the record carried a parseable date
}

// View renders sources into a static timeline and returns it as text.
//
// Every path in sources is explicit: there is no default file, no directory
// walk and no discovery. A path matching an exclude glob — against the path
// as given or its base name — is skipped before it is opened and counted as
// excluded. max caps the cards printed (0 prints every card); the summary
// line always carries the totals, so a capped run still says how much it
// stood aside. A negative max, a bad glob, or an unreadable selection is an
// error and prints nothing half-done.
func View(sources []string, exclude []string, max int) (string, error) {
	if max < 0 {
		return "", fmt.Errorf("--max must be zero or more (0 prints every card), got %d", max)
	}
	for _, e := range exclude {
		if _, err := path.Match(e, ""); err != nil {
			return "", fmt.Errorf("bad --exclude glob %q: %v", e, err)
		}
	}

	var cards []card
	excluded := 0
	for _, src := range sources {
		if excludedBy(src, exclude) {
			excluded++
			continue
		}
		fi, err := os.Stat(src)
		if err != nil {
			return "", fmt.Errorf("view %s: %v", src, err)
		}
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("view %s: not a regular file", src)
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("view %s: %v", src, err)
		}
		cards = append(cards, parseCard(src, string(raw)))
	}

	// Chronological, with unknowns last in selection order: the sort is stable
	// and two unknowns never compare, so an undated record keeps the place its
	// selector gave it instead of drifting under a guess.
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		switch {
		case a.known && !b.known:
			return true
		case !a.known && b.known:
			return false
		case a.known && b.known && !a.at.Equal(b.at):
			return a.at.Before(b.at)
		case a.source != b.source:
			return a.source < b.source
		default:
			return a.title < b.title
		}
	})

	var b strings.Builder
	fmt.Fprintf(&b, "VIEW OK files=%d cards=%d shown=%d excluded=%d\n",
		len(sources), len(cards), min(len(cards), shownCards(len(cards), max)), excluded)
	list := bounded.Capped(&b, max, "VIEW", "card", viewRemedy)
	for _, c := range cards {
		list.Line(fmt.Sprintf("CARD date=%s author=%s kind=%s source=%s supersedes=%s title=%s",
			oneline.Field(c.date), oneline.Field(c.author), oneline.Field(c.kind),
			oneline.Field(c.source), oneline.Field(c.supersedes), oneline.Escape(c.title)))
	}
	if err := list.Err(); err != nil {
		return "", err
	}
	list.More()
	return b.String(), nil
}

// shownCards is what the summary line reports as shown: the whole list, or
// the ceiling when one was asked for.
func shownCards(n, max int) int {
	if max > 0 && n > max {
		return max
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// excludedBy matches a selection against the run's exclusions, by the path as
// given and by its base name, so `--exclude draft*` holds however the record
// was spelled on the command line.
func excludedBy(src string, exclude []string) bool {
	base := filepath.Base(src)
	for _, e := range exclude {
		if ok, _ := path.Match(e, src); ok {
			return true
		}
		if ok, _ := path.Match(e, base); ok {
			return true
		}
	}
	return false
}

// parseCard reads one record. Two layouts, and only two, are recognized —
// anything else is a record with unknown metadata, never an error:
//
//  1. a leading `---` frontmatter fence holding lowercase `author:`, `date:`,
//     `kind:` and `supersedes:` lines, in the shape nova-memory already reads;
//  2. `Author:`, `Date:`, `Kind:` and `Supersedes:` trailer lines anywhere in
//     the file, first occurrence wins.
//
// The fence fills what it holds and the trailer lines fill the rest, so a
// record carrying both still yields one card. A date parses as RFC3339 or as
// YYYY-MM-DD; anything else is unknown rather than a neighbouring guess.
func parseCard(source, text string) card {
	c := card{source: source, author: "unknown", date: "unknown", kind: "unknown", supersedes: "-"}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}

	rest := lines
	if len(lines) > 0 && lines[0] == "---" {
		end := -1
		for i := 1; i < len(lines); i++ {
			if lines[i] == "---" {
				end = i
				break
			}
		}
		if end > 0 {
			for _, line := range lines[1:end] {
				key, val, ok := splitMeta(line)
				if !ok {
					continue
				}
				c.setLower(key, val)
			}
			rest = lines[end+1:]
		}
	}
	for _, line := range rest {
		key, val, ok := splitMeta(line)
		if !ok {
			continue
		}
		c.setTitle(key, val)
	}

	c.title = cardTitle(lines)
	return c
}

// splitMeta cuts a `Key: value` metadata line. The key is trimmed but keeps
// its case — `author:` is the fence layout and `Author:` the trailer layout —
// and a line with no colon is not metadata at all.
func splitMeta(line string) (key, val string, ok bool) {
	key, val, ok = strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(val), true
}

// setLower takes one lowercase fence key. An empty value says nothing, and a
// second line for the same key does not overwrite the first: the top of the
// fence is the record's own head, not an argument.
func (c *card) setLower(key, val string) {
	if val == "" {
		return
	}
	switch key {
	case "author":
		if c.author == "unknown" {
			c.author = val
		}
	case "date":
		if !c.known {
			c.setDate(val)
		}
	case "kind":
		if c.kind == "unknown" {
			c.kind = val
		}
	case "supersedes":
		if c.supersedes == "-" {
			c.supersedes = val
		}
	}
}

// setTitle takes one Title-case trailer key, filling only what the fence did
// not already say.
func (c *card) setTitle(key, val string) {
	if val == "" {
		return
	}
	switch key {
	case "Author":
		if c.author == "unknown" {
			c.author = val
		}
	case "Date":
		if !c.known {
			c.setDate(val)
		}
	case "Kind":
		if c.kind == "unknown" {
			c.kind = val
		}
	case "Supersedes":
		if c.supersedes == "-" {
			c.supersedes = val
		}
	}
}

// setDate takes a date the record states. RFC3339 instants are carried in UTC
// so one timeline never holds two spellings of the same moment; a bare
// YYYY-MM-DD is echoed as written. Anything else leaves the card unknown.
func (c *card) setDate(val string) {
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		c.at = t.UTC()
		c.date = c.at.Format(time.RFC3339)
		c.known = true
		return
	}
	if t, err := time.Parse("2006-01-02", val); err == nil {
		c.at = t
		c.date = val
		c.known = true
	}
}

// cardTitle is the card's one-line handle: the first `#` heading, else the
// first non-empty line, else unknown. It is bounded, and the bound is on
// runes, so a cut never splits a character the source holds whole.
func cardTitle(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if title != "" {
				return truncateRunes(title, viewTitleRunes)
			}
		}
	}
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateRunes(trimmed, viewTitleRunes)
		}
	}
	return "unknown"
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}
