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
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// namedSlots are the slots a validated template may declare (SPEC-PULSE, "Cut, from a
// validated template"). cut fills every one it finds from the source it read; a declared
// slot with no value is CUT REFUSED check=slot.
var namedSlots = []string{"issue", "title", "body", "branch", "base", "row", "replay"}

// CutValidatedInput is everything the validated-template cut needs, held apart from flag
// parsing so a test can drive it with a fixture gh and a fixture git.
type CutValidatedInput struct {
	Source     string // issue, rows or branch-from
	Issue      string // <owner>/<repo>#<n> when Source is issue
	Rows       string // the rows.tsv path when Source is rows
	BranchFrom string // <owner>/<repo>#<n> when Source is branch-from
	Templates  string // the directory holding the source's .md template
	Out        string // the directory the cards go into
	Root       string // the state root, accepted with the other verbs' shape and otherwise unused
	Max        int    // cap on the cards cut; 0 lifts it
	Stdout     io.Writer
	Stderr     io.Writer
}

// validatedCard is one card before it is rendered: its label, the branch and base the checks
// read, the slot values the template fills, and the paths the base check must find.
type validatedCard struct {
	label  string
	branch string
	base   string
	slots  map[string]string
	paths  []string
}

// CutValidated cuts cards from one input (an issue, a table of rows, or a PR's head branch)
// through a template with named slots. It writes the cards under --out and their rows to
// cards.tsv, and nothing else. Before a byte is written it runs five checks in order --
// branch, base, STEP 1, row, result -- and stops at the first that fails, exit 2 with one
// refusal line. Success prints one CUT OK line naming the source.
func CutValidated(in CutValidatedInput) int {
	tmplPath := filepath.Join(in.Templates, in.Source+".md")
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --templates wants %s.md: %s\n", oneline.Field(in.Source), oneline.Err(err))
		return 2
	}
	tmpl := string(raw)

	cards, skipped, err := validatedCards(in)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", oneline.Err(err))
		return 2
	}

	if code, done := validateCards(in, cards, tmpl); done {
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
		name := c.label + ".md"
		if err := os.WriteFile(filepath.Join(in.Out, name), []byte(renderValidated(tmpl, c)), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s\n", oneline.Field(name), oneline.Err(err))
			return 2
		}
		rows = append(rows, CardRow{Label: c.label, Slot: SlotDash, Model: "-", Card: filepath.Join(in.Out, name)})
	}
	if err := writeCardsTSV(filepath.Join(in.Out, "cards.tsv"), rows); err != nil {
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
func validateCards(in CutValidatedInput, cards []validatedCard, tmpl string) (int, bool) {
	// (1) the branch is derived or named and does not exist on origin. --branch-from names
	// its exact head ref, so the check is skipped for it.
	if in.Source != "branch-from" {
		for _, c := range cards {
			if c.branch == "" {
				continue
			}
			out, err := gitStdout("ls-remote", "origin", c.branch)
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
			if exec.Command("git", "cat-file", "-e", c.base+":"+p).Run() != nil {
				fmt.Fprintf(in.Stderr, "CUT REFUSED check=base path=%s not at %s (fix the row, or add the file)\n", oneline.Field(p), oneline.Field(c.base))
				return 2, true
			}
		}
	}
	// (3) STEP 1 parses as one shell line, in-process.
	if reason, ok := stepOneIsOneShellLine(tmpl); !ok {
		fmt.Fprintf(in.Stderr, "CUT REFUSED check=step1 (%s)\n", reason)
		return 2, true
	}
	// (4) no row is a table separator or a header: the reader in validatedCards skips them,
	// so this check has already passed by the time the cards are here.
	// (5) the rendered RESULT line is one line, and every declared slot has a value.
	for _, c := range cards {
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
		c, err := issueCard(in.Issue)
		if err != nil {
			return nil, 0, err
		}
		return []validatedCard{c}, 0, nil
	case "branch-from":
		c, err := branchFromCard(in.BranchFrom)
		if err != nil {
			return nil, 0, err
		}
		return []validatedCard{c}, 0, nil
	case "rows":
		return rowsCards(in.Rows)
	}
	return nil, 0, fmt.Errorf("cut reads one of --pool, --issue, --rows or --branch-from, got source %q", in.Source)
}

// issueCard reads the issue's title and body verbatim through gh and derives the branch from
// the issue number and the title slug.
func issueCard(spec string) (validatedCard, error) {
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
		base:   "dev",
		slots: map[string]string{
			"issue":  fmt.Sprintf("%s#%d", repo, number),
			"title":  v.Title,
			"body":   v.Body,
			"branch": branch,
			"base":   "dev",
		},
	}, nil
}

// branchFromCard reads the PR's exact head ref through gh and carries it as the branch.
func branchFromCard(spec string) (validatedCard, error) {
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
		base:   "dev",
		slots: map[string]string{
			"issue":  fmt.Sprintf("%s#%d", repo, number),
			"branch": v.HeadRefName,
			"base":   "dev",
		},
	}, nil
}

// rowsCards reads a tab-separated table, one card per data group: label, base, row, replay,
// branch. A separator row (|---|) and a header row naming the columns are not cards, and are
// counted as skipped.
func rowsCards(path string) ([]validatedCard, int, error) {
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
			base = "dev"
		}
		row, replay, branch := at(2), at(3), at(4)
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
			label:  label,
			branch: branch,
			base:   base,
			slots: map[string]string{
				"row":    row,
				"replay": replay,
				"branch": branch,
				"base":   base,
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
		case "label", "base", "row", "replay", "branch", "kind", "template":
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

// gitOut runs one git child and returns its stdout.
func gitStdout(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// slug lowercases a title and folds every run of non-alphanumerics to one dash.
var slugClean = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugClean.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
