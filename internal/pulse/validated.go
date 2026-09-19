package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// namedSlots are the slots a validated template may declare (SPEC-PULSE, "Cut, from a
// validated template"). cut fills every one it finds from the source it read; a declared
// slot with no value is CUT REFUSED check=slot.
var namedSlots = []string{"issue", "title", "body", "branch", "base", "row", "replay", "lane"}

// CutValidatedInput is everything the validated-template cut needs, held apart from flag
// parsing so a test can drive it with a fixture gh and a fixture git.
type CutValidatedInput struct {
	Source     string // issue, rows or branch-from
	Issue      string // <owner>/<repo>#<n> when Source is issue
	Rows       string // the rows.tsv path when Source is rows
	BranchFrom string // <owner>/<repo>#<n> when Source is branch-from
	Templates  string // the directory holding the source's .md template
	Out        string // the directory the cards go into
	Repo       string // the clone every git call runs in: `git -C <repo> ...`, never the working directory
	Base       string // the base branch a source that names none is cut onto; "" is dev
	Cards      string // where the cards.tsv goes; "" is <out>/cards.tsv, which puts a table in a queue directory
	Max        int    // cap on the cards cut; 0 lifts it
	Stdout     io.Writer
	Stderr     io.Writer
}

// validatedCard is one card before it is rendered: its label, the branch and base the checks
// read, the slot values the template fills, and the paths the base check must find.
type validatedCard struct {
	label    string
	branch   string
	base     string
	template string // the template this card is rendered from; "" is the source's own
	slots    map[string]string
	paths    []string
}

// CutValidated cuts cards from one input (an issue, a table of rows, or a PR's head branch)
// through a template with named slots. It writes the cards under --out and their rows to
// cards.tsv, and nothing else. Before a byte is written it runs five checks in order --
// branch, base, STEP 1, row, result -- and stops at the first that fails, exit 2 with one
// refusal line. Success prints one CUT OK line naming the source.
func CutValidated(in CutValidatedInput) int {
	if info, err := os.Stat(in.Repo); err != nil || !info.IsDir() {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --repo %s is not a directory (name the clone every git call runs in; cut never reads the working directory)\n", oneline.Field(in.Repo))
		return 2
	}
	if in.Base == "" {
		in.Base = "dev"
	}
	templates := map[string]string{}
	load := func(name string) (string, bool) {
		if t, ok := templates[name]; ok {
			return t, true
		}
		raw, err := os.ReadFile(filepath.Join(in.Templates, name+".md"))
		if err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: --templates wants %s.md: %s\n", oneline.Field(name), oneline.Err(err))
			return "", false
		}
		templates[name] = string(raw)
		return templates[name], true
	}
	if _, ok := load(in.Source); !ok {
		return 2
	}

	cards, skipped, err := validatedCards(in)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	for i := range cards {
		cards[i].label = cardLabel(cards[i].label)
		if cards[i].template == "" {
			cards[i].template = in.Source
		}
		if _, ok := load(cards[i].template); !ok {
			return 2
		}
	}

	if code, done := validateCards(in, cards, templates); done {
		return code
	}
	if in.Max > 0 && len(cards) > in.Max {
		skipped += len(cards) - in.Max
		cards = cards[:in.Max]
	}

	if err := os.MkdirAll(in.Out, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --out %s: %s (pass a directory cut may create)\n", oneline.Field(in.Out), oneline.Err(err))
		return 2
	}
	var rows []CardRow
	for _, c := range cards {
		name := cardFileName(c.label)
		if err := os.WriteFile(filepath.Join(in.Out, name), []byte(renderValidated(templates[c.template], c)), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s\n", oneline.Field(name), oneline.Err(err))
			return 2
		}
		rows = append(rows, CardRow{Label: c.label, Slot: SlotDash, Model: "-", Card: filepath.Join(in.Out, name)})
	}
	if err := appendCardsTSV(cardsTable(in), rows); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(in.Stdout, "CUT OK cards=%d from=%s skipped=%d out=%s\n", len(rows), oneline.Field(in.Source), skipped, oneline.Field(in.Out))
	if skipped > 0 {
		return 1
	}
	return 0
}

// validateCards is the five checks, in order, before a byte is written. It returns the exit
// code and whether it stopped.
func validateCards(in CutValidatedInput, cards []validatedCard, templates map[string]string) (int, bool) {
	// (1) the branch is a branch name git would accept, and it does not exist on origin.
	// --branch-from names its exact head ref, so the check is skipped for it.
	if in.Source != "branch-from" {
		for _, c := range cards {
			if c.branch == "" {
				continue
			}
			if why := checkRefFormatBranch(c.branch); why != "" {
				fmt.Fprintf(in.Stderr, "CUT REFUSED check=branch branch=%s (%s: git check-ref-format --branch refuses this name)\n", oneline.Field(c.branch), why)
				return 2, true
			}
			out, err := gitStdout(in.Repo, "ls-remote", "origin", c.branch)
			if err != nil {
				fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
				return 2, true
			}
			if strings.TrimSpace(out) != "" {
				fmt.Fprintf(in.Stderr, "CUT REFUSED check=branch branch=%s (pass --branch-from <pr>, or rename the issue)\n", oneline.Field(c.branch))
				return 2, true
			}
		}
	}
	// (2) every file path and replay name a row names exists at the base revision.
	for _, c := range cards {
		for _, p := range c.paths {
			if p == "" {
				continue
			}
			if exec.Command("git", "-C", in.Repo, "cat-file", "-e", c.base+":"+p).Run() != nil {
				fmt.Fprintf(in.Stderr, "CUT REFUSED check=base path=%s not at %s (fix the row, or add the file)\n", oneline.Field(p), oneline.Field(c.base))
				return 2, true
			}
		}
	}
	// (3) STEP 1 parses as one shell line, in-process, in every template a card names.
	for _, name := range sortedNames(templates) {
		if reason, ok := stepOneIsOneShellLine(templates[name]); !ok {
			fmt.Fprintf(in.Stderr, "CUT REFUSED check=step1 (%s)\n", reason)
			return 2, true
		}
	}
	// (4) no row is a table separator or a header: the reader in validatedCards skips them,
	// so this check has already passed by the time the cards are here.
	// (5) the rendered RESULT line is one line, and every declared slot has a value.
	for _, c := range cards {
		tmpl := templates[c.template]
		if name, missing := missingSlot(tmpl, c.slots); missing {
			fmt.Fprintf(in.Stderr, "CUT REFUSED check=slot slot=%s (named slot with no value: fill it, or drop it from the template)\n", oneline.Field(name))
			return 2, true
		}
		if !resultIsOneLine(tmpl, c) {
			fmt.Fprintf(in.Stderr, "CUT REFUSED check=result (the RESULT line is more than one line: fix the template)\n")
			return 2, true
		}
	}
	return 0, false
}

// validatedCards reads the one source and returns the cards it names, the rows nothing cut,
// and an error when the source could not be read.
func validatedCards(in CutValidatedInput) ([]validatedCard, int, error) {
	switch in.Source {
	case "issue":
		c, err := issueCard(in.Issue, in.Base)
		if err != nil {
			return nil, 0, err
		}
		return []validatedCard{c}, 0, nil
	case "branch-from":
		c, err := branchFromCard(in.BranchFrom, in.Base)
		if err != nil {
			return nil, 0, err
		}
		return []validatedCard{c}, 0, nil
	case "rows":
		return rowsCards(in.Rows, in.Base)
	}
	return nil, 0, fmt.Errorf("cut reads one of --pool, --issue, --rows or --branch-from, got source %q", in.Source)
}

// issueCard reads the issue's title and body verbatim through gh and derives the branch from
// the issue number and the title slug.
func issueCard(spec, base string) (validatedCard, error) {
	repo, number, err := parseRepoRef("--issue", spec)
	if err != nil {
		return validatedCard{}, err
	}
	raw, err := ghStdout("issue", "view", strconv.Itoa(number), "--repo", repo, "--json", "title,body")
	if err != nil {
		return validatedCard{}, err
	}
	var v struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return validatedCard{}, fmt.Errorf("--issue %s: gh answered something that is not title,body: %s", spec, oneline.Err(err))
	}
	branch := fmt.Sprintf("rowan/issue-%d-%s", number, slug(v.Title))
	return validatedCard{
		label:  strconv.Itoa(number),
		branch: branch,
		base:   base,
		slots: map[string]string{
			"issue":  fmt.Sprintf("%s#%d", repo, number),
			"title":  v.Title,
			"body":   v.Body,
			"branch": branch,
			"base":   base,
		},
	}, nil
}

// branchFromCard reads the PR's exact head ref through gh and carries it as the branch.
func branchFromCard(spec, base string) (validatedCard, error) {
	repo, number, err := parseRepoRef("--branch-from", spec)
	if err != nil {
		return validatedCard{}, err
	}
	raw, err := ghStdout("pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "headRefName,headRefOid")
	if err != nil {
		return validatedCard{}, err
	}
	var v struct {
		HeadRefName string `json:"headRefName"`
		HeadRefOid  string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return validatedCard{}, fmt.Errorf("--branch-from %s: gh answered something that is not a head ref: %s", spec, oneline.Err(err))
	}
	if v.HeadRefName == "" {
		return validatedCard{}, fmt.Errorf("--branch-from %s: the PR names no head ref", spec)
	}
	return validatedCard{
		label:  slug(v.HeadRefName),
		branch: v.HeadRefName,
		base:   base,
		slots: map[string]string{
			"issue":  fmt.Sprintf("%s#%d", repo, number),
			"branch": v.HeadRefName,
			"base":   base,
		},
	}, nil
}

// rowsCards reads a tab-separated table, one card per data group: label, base, row, replay,
// branch. A separator row (|---|) and a header row naming the columns are not cards, and are
// counted as skipped.
func rowsCards(path, defaultBase string) ([]validatedCard, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("--rows wants a readable table: %s", oneline.Err(err))
	}
	var cards []validatedCard
	skipped := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if isSeparatorRow(fields) || isHeaderRow(fields) {
			skipped++
			continue
		}
		at := func(n int) string {
			if n < len(fields) {
				return fields[n]
			}
			return ""
		}
		base := at(1)
		if base == "" {
			base = defaultBase
		}
		row, replay, branch := at(2), at(3), at(4)
		lane, template := strings.TrimSpace(at(5)), strings.TrimSpace(at(6))
		label := at(0)
		if label == "" {
			label = slug(row)
		}
		var paths []string
		if row != "" {
			paths = append(paths, row)
		}
		if replay != "" {
			paths = append(paths, replay)
		}
		cards = append(cards, validatedCard{
			label:    label,
			branch:   branch,
			base:     base,
			template: template,
			slots: map[string]string{
				"row":    row,
				"replay": replay,
				"branch": branch,
				"base":   base,
				"lane":   lane,
			},
			paths: paths,
		})
	}
	return cards, skipped, nil
}

// isSeparatorRow says whether a row is a table separator (`|---|`, `---|---`), never a card.
func isSeparatorRow(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if f == "" {
			return false
		}
		if strings.Trim(f, "-| ") != "" {
			return false
		}
	}
	return true
}

// isHeaderRow says whether a row names the table's columns, never a card.
func isHeaderRow(fields []string) bool {
	if len(fields) < 2 {
		return false
	}
	for _, f := range fields {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "label", "base", "row", "replay", "branch", "lane", "kind", "template":
		default:
			return false
		}
	}
	return true
}

// stepOneIsOneShellLine parses the STEP 1 line in-process: a trailing continuation operator
// or an indented continuation line means STEP 1 is more than one shell line. No shell is run.
func stepOneIsOneShellLine(tmpl string) (string, bool) {
	const reason = "STEP 1 is not one shell line: fix the template"
	lines := strings.Split(tmpl, "\n")
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), "STEP 1") {
			continue
		}
		trimmed := strings.TrimRight(strings.TrimSpace(ln), " ")
		for _, tail := range []string{"&&", "||", "|", "\\"} {
			if strings.HasSuffix(trimmed, tail) {
				return reason, false
			}
		}
		if i+1 < len(lines) {
			next := lines[i+1]
			nt := strings.TrimSpace(next)
			indented := next != "" && (next[0] == ' ' || next[0] == '\t')
			if indented && nt != "" && !strings.HasPrefix(nt, "STEP ") && !strings.HasPrefix(nt, "check:") {
				return reason, false
			}
		}
		return "", true
	}
	return "", true
}

// missingSlot names the first declared slot with no value.
func missingSlot(tmpl string, slots map[string]string) (string, bool) {
	for _, name := range namedSlots {
		if strings.Contains(tmpl, "<"+name+">") && slots[name] == "" {
			return name, true
		}
	}
	return "", false
}

// resultIsOneLine says whether the rendered RESULT line stays one physical line.
func resultIsOneLine(tmpl string, c validatedCard) bool {
	first := strings.SplitN(tmpl, "\n", 2)[0]
	return !strings.Contains(substituteSlots(first, c), "\n")
}

// renderValidated fills the slots, then binds line 1 by replacing <sha12> with the sha-12 of
// everything below line 1.
func renderValidated(tmpl string, c validatedCard) string {
	rendered := substituteSlots(tmpl, c)
	lines := strings.Split(rendered, "\n")
	body := strings.Join(lines[1:], "\n")
	sum := sha256.Sum256([]byte(body))
	lines[0] = strings.ReplaceAll(lines[0], "<sha12>", hex.EncodeToString(sum[:])[:12])
	return strings.Join(lines, "\n")
}

// substituteSlots fills the named slots and the built-in <label>.
func substituteSlots(s string, c validatedCard) string {
	return strings.NewReplacer(
		"<label>", c.label,
		"<issue>", c.slots["issue"],
		"<title>", c.slots["title"],
		"<body>", c.slots["body"],
		"<branch>", c.slots["branch"],
		"<base>", c.slots["base"],
		"<row>", c.slots["row"],
		"<replay>", c.slots["replay"],
		"<lane>", c.slots["lane"],
	).Replace(s)
}

// parseRepoRef reads the `<owner>/<repo>#<n>` a source names.
func parseRepoRef(flag, spec string) (string, int, error) {
	left, right, ok := strings.Cut(spec, "#")
	if !ok {
		return "", 0, fmt.Errorf("%s wants <owner>/<repo>#<number>, got %q", flag, spec)
	}
	number, err := strconv.Atoi(right)
	if err != nil || number < 1 {
		return "", 0, fmt.Errorf("%s wants an issue number after #, got %q", flag, right)
	}
	if owner, repo, ok := strings.Cut(left, "/"); !ok || owner == "" || repo == "" {
		return "", 0, fmt.Errorf("%s wants <owner>/<repo>#<number>, got %q", flag, spec)
	}
	return left, number, nil
}

// ghOut runs one gh child and returns its stdout.
func ghStdout(args ...string) (string, error) {
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// gitStdout runs one git child in the named clone and returns its stdout. Every git call
// this verb makes runs with -C: a command that reads the working directory is a command
// that answers differently depending on where a hand happened to stand, and cut ran
// `git ls-remote origin` against whatever clone the caller was in.
func gitStdout(repo string, args ...string) (string, error) {
	full := append([]string{"-C", repo}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(full, " "), err)
	}
	return string(out), nil
}

// checkRefFormatBranch is `git check-ref-format --branch <name>` in Go -- the rules of
// git-check-ref-format(1) as they apply to refs/heads/<name> -- and answers why the name is
// refused, or "" when git would accept it. No subprocess: a validity question with a fixed
// answer is arithmetic, and `rowan/has a space` was CUT OK because nobody asked it.
func checkRefFormatBranch(name string) string {
	switch {
	case name == "":
		return "it is empty"
	case strings.HasPrefix(name, "-"):
		return "it starts with a dash"
	case name == "@":
		return "it is the single character @"
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return "it starts or ends with a slash"
	case strings.Contains(name, "//"):
		return "it holds an empty path component"
	case strings.HasSuffix(name, "."):
		return "it ends with a dot"
	case strings.Contains(name, ".."):
		return "it holds two dots in a row"
	case strings.Contains(name, "@{"):
		return "it holds @{"
	}
	for _, r := range name {
		switch {
		case r == ' ':
			return "it holds a space"
		case r == '\\':
			return "it holds a backslash"
		case r < 0x20 || r == 0x7f:
			return "it holds a control character"
		case strings.ContainsRune("~^:?*[", r):
			return fmt.Sprintf("it holds %q", r)
		case unicode.IsSpace(r):
			return "it holds whitespace"
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return "a path component starts with a dot"
		}
		if strings.HasSuffix(part, ".lock") {
			return "a path component ends with .lock"
		}
	}
	return ""
}

// cardFileName is the one filename contract of the queue directories: card-<n>.md, which is
// what `fill` globs and what every other verb writes. cut wrote `<label>.md` until
// 2026-09-18, and a directory of cut cards sat in --ready that the tick stepped over in
// silence. A label that already carries the prefix is not given it twice.
func cardFileName(label string) string {
	if strings.HasPrefix(label, "card-") {
		return label + ".md"
	}
	return "card-" + label + ".md"
}

// cardsTable is where the cards.tsv goes: --cards when it is named, and <out>/cards.tsv
// otherwise. --out is a queue directory in real use, and a table dropped into it is a file
// nothing in the queue reads and every glob has to step over (dogfood, 2026-09-18).
func cardsTable(in CutValidatedInput) string {
	if strings.TrimSpace(in.Cards) != "" {
		return in.Cards
	}
	return filepath.Join(in.Out, "cards.tsv")
}

// cardLabel is the label a card carries into its RESULT line and its cards.tsv row. A label
// already spelt card-9601 is 9601: the queue's filename prefix belongs to the filename, and
// carrying it in the label too rendered CARD-card-9601 and rode into a PR title.
func cardLabel(label string) string {
	if bare := strings.TrimPrefix(label, "card-"); bare != "" && bare != label {
		return bare
	}
	return label
}

// sortedNames is the template names in a fixed order, so a refusal over several templates
// is the same refusal every run.
func sortedNames(templates map[string]string) []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// appendCardsTSV adds rows to a cards.tsv rather than replacing it: a second cut into the
// same --out is a second batch of cards in the same queue, and overwriting the table lost
// every card of the first. A row already in the file is not written twice, so a cut run
// again over the same source is the same table.
func appendCardsTSV(path string, cards []CardRow) error {
	have := map[string]bool{}
	var b strings.Builder
	if raw, err := os.ReadFile(path); err == nil {
		b.Write(raw)
		if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line != "" {
				have[line] = true
			}
		}
	}
	for _, c := range cards {
		slot := c.Slot
		if slot == "" {
			slot = SlotDash
		}
		line := fmt.Sprintf("%s\t%s\t%s\t%s", c.Label, slot, c.Model, c.Card)
		if have[line] {
			continue
		}
		have[line] = true
		b.WriteString(line)
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// slug lowercases a title and folds every run of non-alphanumerics to one dash.
var slugClean = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugClean.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
