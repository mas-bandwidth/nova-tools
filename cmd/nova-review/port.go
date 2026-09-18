package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cliflags"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// portHost is the one seam through which `port` reads the world. The real
// implementation fetches from the GitHub remote the lane names; every test
// injects a fake so no test reaches the network (SPEC-REVIEW.md, red test 8).
type portHost interface {
	Head(pr int) (string, error)
	Base(pr int) (name, sha string, err error)
	File(sha, path string) ([]byte, error)
	List(sha string) ([]string, error)
	Diff(base, head string) (string, error)
}

// openPortHost is the injected host factory. Tests replace it.
var openPortHost = newGitPortHost

type gitPortHost struct {
	ctx      context.Context
	repo     string
	hostRepo string
	baseName string
}

func newGitPortHost(ctx context.Context, lane string) (portHost, error) {
	st, err := merge.Load(lane)
	if err != nil {
		return nil, err
	}
	return &gitPortHost{
		ctx:      ctx,
		repo:     filepath.Join(lane, merge.RepoDir),
		hostRepo: st.Repo,
		baseName: st.Base,
	}, nil
}

func (g *gitPortHost) Head(pr int) (string, error) {
	return fetchEntryHead(g.ctx, g.repo, pr, "", g.hostRepo)
}

func (g *gitPortHost) Base(pr int) (string, string, error) {
	name := g.baseName
	if merge.IsSHA(name) {
		return name, name, nil
	}
	sha, err := fetchBase(g.ctx, g.repo, pr, name, g.hostRepo)
	return name, sha, err
}

func (g *gitPortHost) File(sha, p string) ([]byte, error) {
	out, err := gitOut(g.ctx, g.repo, "show", sha+":"+p)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

func (g *gitPortHost) List(sha string) ([]string, error) {
	out, err := gitOut(g.ctx, g.repo, "ls-tree", "-r", "--name-only", sha)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			names = append(names, ln)
		}
	}
	return names, nil
}

func (g *gitPortHost) Diff(base, head string) (string, error) {
	return gitOut(g.ctx, g.repo, "diff", base+"..."+head)
}

type portWitness struct {
	kind string
	path string
	line int
	rule string
}

func (w portWitness) String() string { return fmt.Sprintf("%s:%d", w.path, w.line) }

type portDiffLine struct {
	path string
	line int
	text string
}

func portRefuse(w io.Writer, table string, pr int, head, reason, gate, remedy string) int {
	if strings.TrimSpace(table) == "" {
		table = "-"
	}
	headField := "-"
	if head != "" {
		headField = short12(head)
	}
	gateField := gate
	if gateField == "" {
		gateField = "-"
	}
	fmt.Fprintf(w, "PORT REFUSED table=%s pr=%d head=%s reason=%s existing_gate=%s: %s\n",
		oneline.Field(table), pr, headField, reason, oneline.Escape(gateField), oneline.Escape(remedy))
	return 2
}

func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func port(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("port", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lane := fs.String("lane", "", "")
	table := fs.String("table", "", "")
	pr := fs.Int("pr", 0, "")
	asked := fs.String("head", "", "")
	dest := fs.String("out", "", "")
	maxFlag := fs.Int("max", 200, "")
	timeout := fs.Int("timeout", 300, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		if cliflags.Help(out, err, cliflags.Usage(usage, "port")) {
			return 0
		}
		return portRefuse(errOut, *table, *pr, "", "usage", "",
			"refusing to guess; pass --table <section> --pr <n>")
	}
	if *lane == "" || *table == "" || *pr == 0 {
		return portRefuse(errOut, *table, *pr, "", "usage", "",
			"refusing to guess; pass --table <section> --pr <n>")
	}
	if *maxFlag < 0 {
		return portRefuse(errOut, *table, *pr, "", "max", "", "--max must be non-negative")
	}
	if *timeout <= 0 {
		return portRefuse(errOut, *table, *pr, "", "timeout", "", "--timeout must be positive")
	}
	if *dest == "" {
		*dest = "port-" + portSlug(*table) + ".md"
	}
	if outEscapes(*dest, *lane) {
		return portRefuse(errOut, *table, *pr, "", "out", "",
			fmt.Sprintf("--out must name a path under the current directory or the lane, not %s", oneline.Field(*dest)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	host, err := openPortHost(ctx, *lane)
	if err != nil {
		return portRefuse(errOut, *table, *pr, "", "lane", "",
			fmt.Sprintf("--lane %s holds no readable checkout: %s", oneline.Field(*lane), oneline.Err(err)))
	}

	baseName, baseSHA, err := host.Base(*pr)
	if err != nil {
		return portRefuse(errOut, *table, *pr, "", "base", "",
			fmt.Sprintf("could not read the PR base: %s", oneline.Err(err)))
	}
	head := strings.TrimSpace(*asked)
	if head == "" {
		head, err = host.Head(*pr)
		if err != nil {
			return portRefuse(errOut, *table, *pr, "", "head", "",
				fmt.Sprintf("could not read the PR head: %s", oneline.Err(err)))
		}
	}
	head = strings.TrimSpace(head)

	porting, err := host.File(head, "docs/PORTING.md")
	if err != nil {
		return portRefuse(errOut, *table, *pr, head, "table", "",
			fmt.Sprintf("the head %s holds no docs/PORTING.md; the porting table lives there", merge.Short(head)))
	}
	headings, body, ok := findPortingSection(string(porting), *table)
	if !ok {
		return portRefuse(errOut, *table, *pr, head, "table", "",
			fmt.Sprintf("docs/PORTING.md holds no heading %q; it holds: %s", *table, strings.Join(headings, ", ")))
	}
	wantPos, wantNeg, rule := parseWitnessRules(body, *table)

	diff, err := host.Diff(baseSHA, head)
	if err != nil {
		return portRefuse(errOut, *table, *pr, head, "diff", "",
			fmt.Sprintf("could not read the PR diff: %s", oneline.Err(err)))
	}
	added, removed := portDiffCounts(diff)
	if added == 0 && removed == 0 {
		gate := findExistingGate(host, head, wantPos, wantNeg)
		remedy := fmt.Sprintf("the diff is +0 -0, so the target already carries the gate; report existing_gate=%s instead of porting it", gate)
		if gate == "" {
			remedy = "the diff is +0 -0, so the target already carries the gate; report the gate the head holds instead of porting it"
		}
		return portRefuse(errOut, *table, *pr, head, "empty", gate, remedy)
	}

	addedLines := portAddedLines(diff)
	var pos, neg []portWitness
	for _, ln := range addedLines {
		if matchesAnyGlob(wantPos, ln.path) {
			pos = append(pos, portWitness{"positive", ln.path, ln.line, rule})
		}
		if matchesAnyGlob(wantNeg, ln.path) {
			neg = append(neg, portWitness{"negative", ln.path, ln.line, rule})
		}
	}
	if len(wantPos) > 0 && len(pos) == 0 {
		return portRefuse(errOut, *table, *pr, head, "no-positive", "",
			fmt.Sprintf("docs/PORTING.md#%s requires the positive witness (%s); add the test or case that shows the ported behaviour runs in the target language", rule, strings.Join(wantPos, ", ")))
	}
	if len(wantNeg) > 0 && len(neg) == 0 {
		return portRefuse(errOut, *table, *pr, head, "no-negative", "",
			fmt.Sprintf("docs/PORTING.md#%s requires the negative witness (%s); an unverified-negative-control lands when the control that fails without the gate is missing", rule, strings.Join(wantNeg, ", ")))
	}
	for _, w := range append(append([]portWitness{}, pos...), neg...) {
		if !headHolds(host, head, w.path, w.line) {
			return portRefuse(errOut, *table, *pr, head, "witness", "",
				fmt.Sprintf("the head does not hold %s; the diff must add it or the head must carry it", w.String()))
		}
	}

	all := append(append([]portWitness{}, pos...), neg...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].kind != all[j].kind {
			return all[i].kind < all[j].kind
		}
		if all[i].path != all[j].path {
			return all[i].path < all[j].path
		}
		return all[i].line < all[j].line
	})
	shown := all
	cut := 0
	if *maxFlag >= 0 && len(all) > *maxFlag {
		shown = all[:*maxFlag]
		cut = len(all) - *maxFlag
	}
	files := portChangedFiles(addedLines)
	bodyText := formatPortPacket(*table, *pr, head, baseName, baseSHA, rule, shown, cut, len(all))
	if err := portPublish(*dest, []byte(bodyText)); err != nil {
		return portRefuse(errOut, *table, *pr, head, "out", "",
			fmt.Sprintf("could not exclusively create --out: %s", oneline.Err(err)))
	}
	posField, negField := "-", "-"
	if len(pos) > 0 {
		posField = pos[0].String()
	}
	if len(neg) > 0 {
		negField = neg[0].String()
	}
	fmt.Fprintf(out, "PORT OK table=%s pr=%d head=%s base=%s@%s positive=%s negative=%s witnesses=%d files=%d bytes=%d out=%s\n",
		oneline.Field(*table), *pr, short12(head), oneline.Field(baseName), short8(baseSHA),
		oneline.Field(posField), oneline.Field(negField), len(shown), files, len(bodyText), oneline.Field(*dest))
	return 0
}

// portPublish writes the packet through the same unique O_EXCL temporary and
// rename (link) packet uses. It is a variable so a test can inject a kill.
var portPublish = writeExclusive

func portSlug(section string) string {
	var b strings.Builder
	for _, r := range section {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "table"
	}
	return b.String()
}

func findPortingSection(text, section string) (headings []string, body string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	type heading struct {
		title string
		line  int
		level int
	}
	var hs []heading
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		level := 0
		for level < len(t) && t[level] == '#' {
			level++
		}
		hs = append(hs, heading{strings.TrimSpace(t[level:]), i, level})
	}
	for _, h := range hs {
		headings = append(headings, h.title)
	}
	for i, h := range hs {
		if h.title != section {
			continue
		}
		start := h.line + 1
		end := len(lines)
		for j := i + 1; j < len(hs); j++ {
			if hs[j].level <= h.level {
				end = hs[j].line
				break
			}
		}
		return headings, strings.Join(lines[start:end], "\n"), true
	}
	return headings, "", false
}

func parseWitnessRules(body, section string) (pos, neg []string, rule string) {
	rule = section
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "rule:"):
			if v := strings.TrimSpace(t[len("rule:"):]); v != "" {
				rule = v
			}
		case strings.HasPrefix(low, "positive:"):
			if v := strings.TrimSpace(t[len("positive:"):]); v != "" {
				pos = append(pos, v)
			}
		case strings.HasPrefix(low, "negative:"):
			if v := strings.TrimSpace(t[len("negative:"):]); v != "" {
				neg = append(neg, v)
			}
		}
	}
	return pos, neg, rule
}

func matchesAnyGlob(globs []string, p string) bool {
	for _, g := range globs {
		if matchPortGlob(g, p) {
			return true
		}
	}
	return false
}

func matchPortGlob(glob, p string) bool {
	p = filepath.ToSlash(p)
	if ok, _ := path.Match(glob, p); ok {
		return true
	}
	if ok, _ := path.Match(glob, path.Base(p)); ok {
		return true
	}
	if strings.HasPrefix(glob, "**/") {
		rest := glob[3:]
		if ok, _ := path.Match(rest, path.Base(p)); ok {
			return true
		}
		return strings.HasSuffix(p, "/"+rest)
	}
	return false
}

func portDiffCounts(diff string) (added, removed int) {
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
		case strings.HasPrefix(ln, "+"):
			added++
		case strings.HasPrefix(ln, "-"):
			removed++
		}
	}
	return added, removed
}

func portAddedLines(diff string) []portDiffLine {
	var res []portDiffLine
	cur := ""
	newLine := 0
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			if parts := strings.SplitN(ln, " b/", 2); len(parts) == 2 {
				cur = parts[1]
			} else {
				cur = ""
			}
		case strings.HasPrefix(ln, "+++ "):
			p := strings.TrimSpace(ln[4:])
			p = strings.TrimPrefix(p, "b/")
			if p != "/dev/null" {
				cur = p
			}
		case strings.HasPrefix(ln, "@@"):
			newLine = parseHunkNewStart(ln)
		case strings.HasPrefix(ln, "+"):
			res = append(res, portDiffLine{cur, newLine, ln[1:]})
			newLine++
		case strings.HasPrefix(ln, "-"):
		case strings.HasPrefix(ln, " ") || ln == "":
			newLine++
		}
	}
	return res
}

func parseHunkNewStart(hunk string) int {
	plus := strings.Index(hunk, "+")
	if plus < 0 {
		return 0
	}
	rest := hunk[plus+1:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

func portChangedFiles(lines []portDiffLine) int {
	seen := map[string]bool{}
	for _, ln := range lines {
		if ln.path != "" {
			seen[ln.path] = true
		}
	}
	return len(seen)
}

func headHolds(host portHost, sha, p string, line int) bool {
	if line <= 0 {
		return false
	}
	b, err := host.File(sha, p)
	if err != nil {
		return false
	}
	n := len(strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"))
	if len(b) == 0 {
		n = 0
	}
	return line <= n
}

func findExistingGate(host portHost, head string, positive, negative []string) string {
	names, err := host.List(head)
	if err != nil {
		return ""
	}
	sort.Strings(names)
	for _, name := range names {
		if !matchesAnyGlob(positive, name) {
			continue
		}
		if len(negative) > 0 && matchesAnyGlob(negative, name) {
			continue
		}
		b, err := host.File(head, name)
		if err != nil {
			continue
		}
		line := 1
		for i, ln := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			if strings.Contains(strings.ToLower(ln), "gate") {
				line = i + 1
				break
			}
		}
		return fmt.Sprintf("%s:%d", name, line)
	}
	return ""
}

func formatPortPacket(table string, pr int, head, baseName, baseSHA, rule string, shown []portWitness, cut, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PORT PACKET table=%s pr=%d head=%s base=%s@%s rule=%s\n\n",
		oneline.Field(table), pr, short12(head), oneline.Field(baseName), short8(baseSHA), oneline.Field(rule))
	b.WriteString("## Witnesses\n")
	if len(shown) == 0 {
		b.WriteString("none\n")
	}
	for _, w := range shown {
		fmt.Fprintf(&b, "- %s %s rule=%s\n", w.kind, w.String(), oneline.Field(w.rule))
	}
	if cut > 0 {
		fmt.Fprintf(&b, "PORT MORE kind=witness shown=%d total=%d nova-review port --lane <dir> --table %s --pr %d --max 0\n",
			len(shown), total, oneline.Field(table), pr)
	}
	return b.String()
}
