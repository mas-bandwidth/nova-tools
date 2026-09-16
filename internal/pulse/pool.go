package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PoolInput holds everything the pool verb reads, apart from command-line parsing, so a
// test can drive it with fake gh invocations on PATH.
type PoolInput struct {
	Sources string        // path to the declared sources TSV (kind, locator, template)
	Root    string        // root directory holding seen.tsv and the default pool.tsv
	Out     string        // path pool.tsv is written to; empty means <root>/pool.tsv
	Timeout time.Duration // bound on every gh child, default 120s
	Max     int           // bound on admitted candidates; 0 means no bound
	Stdout  io.Writer
	Stderr  io.Writer
}

// source is one declared source line: a kind, a locator, and the default template.
type source struct {
	kind     string
	locator  string
	template string
}

// ghIssue is the subset of a GitHub issue the pool verb reads.
type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Body string `json:"body"`
}

// Pool enumerates bounded open work from the declared sources into pool.tsv. It runs no
// model: every candidate is read through gh, git or the files the sources name. One
// unreadable source is one refusal and no pool.tsv is written.
func Pool(in PoolInput) int {
	started := time.Now()
	if in.Timeout <= 0 {
		in.Timeout = 120 * time.Second
	}
	out := in.Out
	if out == "" {
		out = filepath.Join(in.Root, "pool.tsv")
	}
	srcs, err := readSources(in.Sources)
	if err != nil {
		fmt.Fprintf(in.Stderr, "POOL REFUSED sources=%s: %s (fix the --sources file)\n",
			oneline.Field(in.Sources), oneline.Err(err))
		return 2
	}
	seen, err := readSeen(in.Root)
	if err != nil {
		fmt.Fprintf(in.Stderr, "POOL REFUSED root=%s: %s (fix seen.tsv)\n",
			oneline.Field(in.Root), oneline.Err(err))
		return 2
	}

	var rows []PoolRow
	counts := map[string]int{}
	seenCount := 0
	planCount := 0

	for _, s := range srcs {
		cands, plan, cseen, err := poolSource(s, seen, in)
		if err != nil {
			refuseSource(in.Stderr, s, err)
			return 2
		}
		seenCount += cseen
		planCount += plan
		for _, r := range cands {
			if in.Max > 0 && len(rows) >= in.Max {
				break
			}
			rows = append(rows, r)
			counts[r.Kind]++
		}
	}

	if err := writePool(out, rows); err != nil {
		fmt.Fprintf(in.Stderr, "POOL REFUSED out=%s: %s (pass --out a writable path)\n",
			oneline.Field(out), oneline.Err(err))
		return 2
	}

	fmt.Fprintf(in.Stdout, "POOL OK sources=%d candidates=%d issues=%d audits=%d slices=%d roadmap=%d next=%d plan=%d seen=%d took=%s out=%s\n",
		len(srcs), len(rows),
		counts["issue"], counts["audit"], counts["slice"], counts["roadmap"],
		0, planCount, seenCount,
		time.Since(started).Round(time.Millisecond), oneline.Field(out))
	return 0
}

// readSources reads the declared sources TSV: kind, locator, template per line.
func readSources(path string) ([]source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unreadable: %w", err)
	}
	var srcs []source
	for i, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("line %d is not kind<TAB>locator<TAB>template", i+1)
		}
		srcs = append(srcs, source{kind: parts[0], locator: parts[1], template: parts[2]})
	}
	return srcs, nil
}

// readSeen returns the set of (source,id) already carded, running or pr; a retry state is
// not in the returned set, so a rewritten card is pooled again (rule 2).
func readSeen(root string) (map[string]bool, error) {
	seen := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, "seen.tsv"))
	if err != nil {
		if os.IsNotExist(err) {
			return seen, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		if parts[2] == "carded" || parts[2] == "running" || parts[2] == "pr" {
			seen[parts[0]+"\x00"+parts[1]] = true
		}
	}
	return seen, nil
}

// poolSource reads one source's candidates. It returns the pooled rows, the plan count,
// and the number already seen; a source it cannot read is an error.
func poolSource(s source, seen map[string]bool, in PoolInput) ([]PoolRow, int, int, error) {
	switch s.kind {
	case "issues":
		return poolIssues(s, seen, in, "issue")
	case "audits":
		return poolAudits(s, seen, in)
	case "bus":
		return poolBus(s, seen)
	case "roadmap":
		return poolRoadmap(s, seen)
	default:
		return nil, 0, 0, fmt.Errorf("unknown source kind %q (a source is issues, audits, bus or roadmap)", s.kind)
	}
}

func listIssues(locator string, in PoolInput) ([]ghIssue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "list", "--repo", locator, "--state", "open", "--limit", "500", "--json", "number,title,labels,body")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("gh issue list: bad JSON: %w", err)
	}
	return issues, nil
}

func poolIssues(s source, seen map[string]bool, in PoolInput, kind string) ([]PoolRow, int, int, error) {
	issues, err := listIssues(s.locator, in)
	if err != nil {
		return nil, 0, 0, err
	}
	var rows []PoolRow
	plan := 0
	seenCount := 0
	for _, iss := range issues {
		labelled := hasLabel(iss, "card")
		dogfood := isDogfood(iss.Body)
		if slicesBody(iss.Body) && !dogfood {
			plan++
			continue
		}
		if !labelled && !dogfood {
			continue
		}
		id := strconv.Itoa(iss.Number)
		key := s.kind + "\x00" + id
		if seen[key] {
			seenCount++
			continue
		}
		tpl := s.template
		if t := bodyTemplate(iss.Body); t != "" {
			tpl = t
		}
		rows = append(rows, PoolRow{Source: s.locator, ID: id, Kind: kind, Title: iss.Title, Template: tpl})
	}
	return rows, plan, seenCount, nil
}

func poolAudits(s source, seen map[string]bool, in PoolInput) ([]PoolRow, int, int, error) {
	issues, err := listIssues(s.locator, in)
	if err != nil {
		return nil, 0, 0, err
	}
	var rows []PoolRow
	seenCount := 0
	for _, iss := range issues {
		n := 0
		for _, line := range strings.Split(iss.Body, "\n") {
			if !strings.Contains(line, "MISSING:") && !strings.Contains(line, "DRIFT") {
				continue
			}
			n++
			id := fmt.Sprintf("%d#%d", iss.Number, n)
			key := s.kind + "\x00" + id
			if seen[key] {
				seenCount++
				continue
			}
			rows = append(rows, PoolRow{Source: s.locator, ID: id, Kind: "audit", Title: iss.Title, Template: "drift"})
		}
	}
	return rows, 0, seenCount, nil
}

func poolBus(s source, seen map[string]bool) ([]PoolRow, int, int, error) {
	if _, err := os.Stat(filepath.Join(s.locator, ".git")); err != nil {
		return nil, 0, 0, fmt.Errorf("not a bus checkout (locator must be a git checkout)")
	}
	notesDir := filepath.Join(s.locator, "notes")
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("bus has no readable notes: %w", err)
	}
	var rows []PoolRow
	seenCount := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(notesDir, e.Name()))
		if err != nil {
			continue
		}
		body := string(raw)
		if !slicesBody(body) {
			continue
		}
		tpl := sliceTemplate(body)
		if tpl == "" {
			tpl = s.template
		}
		noteID := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		id := noteID + "#1"
		if seen[s.kind+"\x00"+id] {
			seenCount++
			continue
		}
		rows = append(rows, PoolRow{Source: s.locator, ID: id, Kind: "slice", Title: noteTitle(body), Template: tpl})
	}
	return rows, 0, seenCount, nil
}

func poolRoadmap(s source, seen map[string]bool) ([]PoolRow, int, int, error) {
	raw, err := os.ReadFile(s.locator)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("roadmap unreadable: %w", err)
	}
	if !balanced(string(raw)) {
		return nil, 0, 0, fmt.Errorf("does not parse as a roadmap")
	}
	body := string(raw)
	var rows []PoolRow
	seenCount := 0
	idx := 0
	for {
		at := strings.Index(body[idx:], ":card")
		if at < 0 {
			break
		}
		start := idx + at
		tpl := cardName(body[start+len(":card"):])
		id := cellID(body[:start])
		if tpl != "" {
			key := s.kind + "\x00" + id
			if seen[key] {
				seenCount++
			} else {
				rows = append(rows, PoolRow{Source: s.locator, ID: id, Kind: "roadmap", Title: id, Template: tpl})
			}
		}
		idx = start + len(":card")
	}
	return rows, 0, seenCount, nil
}

func hasLabel(iss ghIssue, name string) bool {
	for _, l := range iss.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

func isDogfood(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "expected") && strings.Contains(lower, "smallest fix")
}

func slicesBody(body string) bool {
	return strings.Contains(body, "slices:")
}

// bodyTemplate returns the template named by an issue body line `template: <name>`.
func bodyTemplate(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "template:") {
			return strings.TrimSpace(strings.SplitN(line, "template:", 2)[1])
		}
	}
	return ""
}

// sliceTemplate returns the template word after a slice's `template:` line, or "".
func sliceTemplate(body string) string {
	return bodyTemplate(body)
}

func noteTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return "-"
}

// balanced reports whether the roadmap file's parentheses balance.
func balanced(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// cardName returns the word following ":card", trimming quotes and punctuation.
func cardName(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		end := strings.Index(s[1:], `"`)
		if end >= 0 {
			return s[1 : 1+end]
		}
	}
	return strings.SplitN(s, ")", 2)[0]
}

// cellID returns the nearest :id value preceding position p of a cell.
func cellID(prefix string) string {
	last := strings.LastIndex(prefix, ":id")
	if last < 0 {
		return "-"
	}
	rest := strings.TrimSpace(prefix[last+len(":id"):])
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end >= 0 {
			return rest[1 : 1+end]
		}
	}
	return strings.Fields(rest)[0]
}

func refuseSource(w io.Writer, s source, err error) {
	fmt.Fprintf(w, "POOL REFUSED source=%s:%s: %s (%s)\n",
		oneline.Field(s.kind), oneline.Field(s.locator), oneline.Err(err), remedy(s.kind))
}

func remedy(kind string) string {
	switch kind {
	case "issues", "audits":
		return "check gh auth and the repo name"
	case "bus":
		return "point --sources at a nova-bus checkout"
	case "roadmap":
		return "point --sources at a roadmap file that parses"
	}
	return "fix the source line"
}

func writePool(path string, rows []PoolRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", r.Source, r.ID, r.Kind, r.Title, r.Template)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
