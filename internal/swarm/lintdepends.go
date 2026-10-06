package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
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
// An entry may instead be an external operand (docs/SPEC-ISA.md, the one wait kind;
// tla/CardISA.tla, Ext): `pr <repo>#<n> merged`, `<branch> contains <sha>`, or
// `after <RFC3339>`. The card waits until the tick sees the operand hold. An entry
// that opens like one (first word `pr` or `after`, or second word `contains`) and
// is not one of the three shapes is refused with the forms (ParseDependsOperand).
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
const CardDependsRemedy = "DEPENDS-ON: <card-id>[, ...] or DEPENDS-ON: -; an entry may be an external operand: " + DependsOperandForms

// DependsOperandForms is the three external operands of DEPENDS-ON, as a refusal names them.
const DependsOperandForms = "pr <owner/repo>#<n> merged, <branch> contains <sha>, or after <RFC3339>"

// The forms of an external operand (DependsOperand.Form).
const (
	OperandPRMerged = "pr"       // pr <repo>#<n> merged: the pull request has merged
	OperandContains = "contains" // <branch> contains <sha>: the commit is on the branch
	OperandAfter    = "after"    // after <RFC3339>: the clock has passed the time
)

// DependsOperand is one external operand of a DEPENDS-ON entry. Text is the entry
// as written, its words separated by single blanks: the operand's one name, which
// the tick asks once a tick however many cards wait on it.
type DependsOperand struct {
	Form   string
	Text   string
	Repo   string // pr: owner/repo, or repo alone
	N      int    // pr: the number
	Branch string // contains: the branch
	SHA    string // contains: the commit
	At     time.Time
}

// Waits is the operand as a card's wait says it: `nova-tools#5303 merged`,
// `main contains 0123abc`, `after 2026-10-06T12:00:00Z`.
func (o DependsOperand) Waits() string {
	if o.Form == OperandPRMerged {
		return o.Repo + "#" + strconv.Itoa(o.N) + " merged"
	}
	return o.Text
}

// prRefRE is the pull request of `pr <repo>#<n> merged`: a repository, with its
// owner or without, then `#` and digits.
var prRefRE = regexp.MustCompile(`^((?:[A-Za-z0-9][A-Za-z0-9._-]*/)?[A-Za-z0-9][A-Za-z0-9._-]*)#([0-9]+)$`)

// branchRE is a branch of `<branch> contains <sha>`; shaRE its commit, 7 to 40 hex digits.
var (
	branchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// ParseDependsOperand reads one DEPENDS-ON entry as an external operand. external
// says the entry opens like one: first word `pr` or `after`, or second word
// `contains`; a card id never does, since it is one word. err says why an entry
// that opens like one is not one of the three forms.
func ParseDependsOperand(entry string) (op DependsOperand, external bool, err error) {
	f := strings.Fields(entry)
	switch {
	case len(f) > 0 && f[0] == OperandPRMerged:
		op.Form = OperandPRMerged
	case len(f) > 0 && f[0] == OperandAfter:
		op.Form = OperandAfter
	case len(f) > 1 && f[1] == OperandContains:
		op.Form = OperandContains
	default:
		return op, false, nil
	}
	op.Text = strings.Join(f, " ")
	switch op.Form {
	case OperandPRMerged:
		if len(f) != 3 || f[2] != "merged" {
			return op, true, fmt.Errorf("%q wants three words, pr <owner/repo>#<n> merged", op.Text)
		}
		m := prRefRE.FindStringSubmatch(f[1])
		if m == nil {
			return op, true, fmt.Errorf("%q names no pull request: %q is not <owner/repo>#<n>", op.Text, f[1])
		}
		op.Repo = m[1]
		op.N, _ = strconv.Atoi(m[2]) // ignored: the pattern took digits alone
	case OperandAfter:
		if len(f) != 2 {
			return op, true, fmt.Errorf("%q wants two words, after <RFC3339>", op.Text)
		}
		if op.At, err = time.Parse(time.RFC3339, f[1]); err != nil {
			return op, true, fmt.Errorf("%q: %q is not an RFC3339 time (2026-10-06T12:00:00Z)", op.Text, f[1])
		}
	case OperandContains:
		if len(f) != 3 {
			return op, true, fmt.Errorf("%q wants three words, <branch> contains <sha>", op.Text)
		}
		if !branchRE.MatchString(f[0]) || strings.Contains(f[0], "..") {
			return op, true, fmt.Errorf("%q: %q is not a branch name", op.Text, f[0])
		}
		if !shaRE.MatchString(f[2]) {
			return op, true, fmt.Errorf("%q: %q is not a commit, 7 to 40 lowercase hex digits", op.Text, f[2])
		}
		op.Branch, op.SHA = f[0], f[2]
	}
	return op, true, nil
}

// LintCardDepends returns the depends-on findings for one card. lineup may be
// nil: an id is then not called unknown.
func LintCardDepends(raw []byte, lineup Lineup) []CardHeaderFinding {
	var out []CardHeaderFinding
	add := func(line int, excerpt string) {
		line = max(line, 1)
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
	var selfs, bad, unknowns, operands []string
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
		// An external operand is shape only too: the tick asks whether it holds.
		if _, external, err := ParseDependsOperand(id); external {
			if err != nil {
				operands = append(operands, err.Error())
			}
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
		add(f.line, fmt.Sprintf("DEPENDS-ON: %s is not a card id, an owner/repo#n reference, or an external operand (%s)", quoteDepends(bad), DependsOperandForms))
	}
	if len(operands) > 0 {
		add(f.line, fmt.Sprintf("DEPENDS-ON: %s; an external operand is %s", strings.Join(operands, "; "), DependsOperandForms))
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
