package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// DEPENDS-ON IS A HEADER KEY, CHECKED ONLY WHEN THE CARD IS TYPED (#2636).
//
// The cutter and the dealer are not this check. `lint --card --typed` is. A card
// written before the key existed still lints by the older rules, and by the typed
// tokens it already declares, unless `--typed` is asked. Under `--typed` the header
// block carries one of:
//
//	DEPENDS-ON: <card-id>[, ...]
//	DEPENDS-ON: -
//
// `-` is a declaration that the card depends on nothing, not an id. The card's own
// id is the first word of its contract line — `RESULT: cutter-b-1282 sha=…` names
// `cutter-b-1282` — and a dependency on that word is a self-dependency. Any other
// id is checked against the lineup only when one was handed over. With no lineup
// the lint does not guess which ids exist, the way `paused` does not guess a kind
// is paused when no trust file was handed over.
//
// A reference is `<owner>/<repo>#<n>`: one slash, then `#` and digits
// (`mas-bandwidth/nova-tools#2550`). The lint checks that shape and does not look
// it up in the lineup; the cutter resolves whether the issue or PR exists.
// A space (`nova-tools #2550`) is not that shape and is refused by name. A word
// the lineup does not hold (`dogfood`) is refused by name as an unknown card id.
//
// The lineup file is the sprint's ORDER.tsv shape, or one id per line. A header
// row that names `depends-on` is not a card. The id column is the one named `id`,
// `card`, `card-id` or `label`; otherwise it is the first column, which is where
// ORDER.tsv keeps the id (`$1` id, `$2` depends-on).

// Lineup is the set of card ids a lineup file named. Nil means no lineup was
// handed over, which is not the same as a lineup that names nothing.
type Lineup map[string]bool

// CardDependsRemedy is what the `depends-on` drift names. One remedy for the
// token, the two forms the key is allowed to take.
const CardDependsRemedy = "DEPENDS-ON: <card-id>[, ...] or DEPENDS-ON: -"

// LintCardDepends returns the depends-on findings for one card. lineup may be
// nil: an id is then not called unknown.
func LintCardDepends(raw []byte, lineup Lineup) []CardHeaderFinding {
	var out []CardHeaderFinding
	add := func(line int, excerpt string) {
		if line < 1 {
			line = 1
		}
		out = append(out, CardHeaderFinding{Check: "depends-on", Line: line, Excerpt: excerpt})
	}

	h, _ := cardHeaderBlock(raw)
	f := h["DEPENDS-ON"]
	if !f.found {
		if below := dependsLineBelow(raw); below > 0 {
			add(below, fmt.Sprintf("DEPENDS-ON: on line %d is below the header block and is not read: the typed header is the unbroken run of `KEY: value` lines directly under the contract line", below))
			return out
		}
		add(1, "no DEPENDS-ON: line under the contract line")
		return out
	}
	if f.again > 0 {
		add(f.again, fmt.Sprintf("DEPENDS-ON: is declared twice, on lines %d and %d; a card with two of this line has no one value for it", f.line, f.again))
	}
	if f.value == "-" {
		return out
	}
	if f.value == "" {
		add(f.line, "DEPENDS-ON: names no card id and is not `-`")
		return out
	}

	ids, empty := splitDepends(f.value)
	if len(ids) == 0 {
		add(f.line, fmt.Sprintf("DEPENDS-ON: %q names no card id and is not `-`", f.value))
		return out
	}
	if empty {
		add(f.line, fmt.Sprintf("DEPENDS-ON: %q has an empty entry between its commas", f.value))
	}

	own := cardOwnID(firstLine(raw))
	var selfs, bad, unknowns []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if own != "" && id == own {
			selfs = append(selfs, id)
			continue
		}
		// A reference is shape only. It is not a lineup id, and not this card's id.
		if dependsReferenceRE.MatchString(id) {
			continue
		}
		if !oneCardID(id) {
			bad = append(bad, id)
			continue
		}
		if lineup != nil && !lineup[id] {
			unknowns = append(unknowns, id)
		}
	}
	if len(bad) > 0 {
		add(f.line, fmt.Sprintf("DEPENDS-ON: %s is not a card id or an owner/repo#n reference", quoteDepends(bad)))
	}
	if len(selfs) > 0 {
		add(f.line, fmt.Sprintf("DEPENDS-ON: %s names this card's own id", strings.Join(selfs, ", ")))
	}
	if len(unknowns) > 0 {
		add(f.line, fmt.Sprintf("DEPENDS-ON: %s is not in the lineup", quoteDepends(unknowns)))
	}
	return out
}

// cardOwnID is the id the card names: the first word of the contract line, after
// `RESULT:` or the stopgap `RESULT `. `RESULT: cutter-b-1282 sha=<sha12>` names
// `cutter-b-1282`. A line that is not a contract line names no id.
func cardOwnID(line string) string {
	var rest string
	switch {
	case strings.HasPrefix(line, "RESULT: "):
		rest = line[len("RESULT: "):]
	case strings.HasPrefix(line, "RESULT "):
		rest = line[len("RESULT "):]
	default:
		return ""
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || strings.HasPrefix(fields[0], "sha=") {
		return ""
	}
	return fields[0]
}

func firstLine(raw []byte) string {
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	return strings.TrimRight(string(line), "\r")
}

// dependsReferenceRE is `<owner>/<repo>#<n>`: exactly one slash, then `#` and digits.
// `mas-bandwidth/nova-tools#2550` matches. `nova-tools #2550` does not (a space).
// `nova-tools#2550` does not (no slash). `a/b/c#1` does not (two slashes).
var dependsReferenceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*#[0-9]+$`)

// oneCardID is one dependency token. `-` is the whole-line declaration, never an
// entry in the list. An entry with whitespace is two words, not an id.
func oneCardID(id string) bool {
	return id != "-" && len(strings.Fields(id)) == 1
}

// quoteDepends names each refused entry, so the drift says which token it refused.
func quoteDepends(ids []string) string {
	q := make([]string, len(ids))
	for i, id := range ids {
		q[i] = fmt.Sprintf("%q", id)
	}
	return strings.Join(q, ", ")
}

func splitDepends(value string) (ids []string, empty bool) {
	for _, p := range strings.Split(value, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			empty = true
			continue
		}
		ids = append(ids, p)
	}
	return ids, empty
}

// dependsLineBelow is the first `DEPENDS-ON:` line that the header block did not
// take. The caller has already asked the block; this only locates the stranded line.
func dependsLineBelow(raw []byte) int {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if n == 1 {
			continue
		}
		line := strings.TrimRight(sc.Text(), "\r")
		if m := headerKeyRE.FindStringSubmatch(line); m != nil && m[1] == "DEPENDS-ON" {
			return n
		}
	}
	return 0
}

// ReadLineup reads the card ids a lineup names. A blank line and a `#` comment
// are skipped. A TSV row contributes its id column; a line with no tab is one id.
func ReadLineup(path string) (Lineup, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("lineup %s: %v", path, err)
	}
	defer f.Close()

	var rows [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			rows = append(rows, splitTab(line))
			continue
		}
		rows = append(rows, []string{trim})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lineup %s: %v", path, err)
	}

	out := Lineup{}
	if len(rows) == 0 {
		return out, nil
	}
	idCol, start := 0, 0
	if lineupHeader(rows[0]) {
		idCol = lineupIDColumn(rows[0])
		start = 1
	}
	for _, row := range rows[start:] {
		if idCol >= len(row) {
			continue
		}
		id := strings.TrimSpace(row[idCol])
		if id == "" || id == "-" {
			continue
		}
		out[id] = true
	}
	return out, nil
}

func splitTab(line string) []string {
	parts := strings.Split(line, "\t")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// lineupHeader is ORDER.tsv's header: a row that names the depends-on column, or
// a first cell `id` on a row that has more than one cell. A card id alone is not
// a header.
func lineupHeader(fields []string) bool {
	for _, f := range fields {
		if strings.EqualFold(f, "depends-on") {
			return true
		}
	}
	return len(fields) > 1 && strings.EqualFold(fields[0], "id")
}

func lineupIDColumn(fields []string) int {
	for i, f := range fields {
		switch strings.ToLower(f) {
		case "id", "card", "card-id", "label":
			return i
		}
	}
	return 0
}
