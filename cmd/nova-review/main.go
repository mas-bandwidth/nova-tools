// nova-review packet builds a bounded, exact-revision source-review artifact.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-review: bounded exact-revision review packets (docs/SPEC-REVIEW.md)

usage:
  nova-review packet --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max <n>] [--max-bytes <n>] [--reuse <file>]
  nova-review version
  nova-review help

example:
  nova-review version
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func refuse(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PACKET REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "PACKET REFUSED: no verb given; run: nova-review help")
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	case "version":
		if len(args) != 1 {
			return refuse(errOut, "version takes no arguments")
		}
		fmt.Fprintln(out, "nova-review devel")
		return 0
	case "packet":
		return packet(args[1:], out, errOut)
	default:
		return refuse(errOut, fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

func packet(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("packet", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lane := fs.String("lane", "", "")
	pr := fs.Int("pr", 0, "")
	branch := fs.String("branch", "", "")
	who := fs.String("who", "", "")
	asked := fs.String("head", "", "")
	dest := fs.String("out", "", "")
	reuse := fs.String("reuse", "", "")
	maxBytes := fs.Int("max-bytes", 131072, "")
	maxFlag := fs.Int("max", 20, "")
	var specs, rules stringsFlag
	fs.Var(&specs, "spec", "")
	fs.Var(&rules, "rule", "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return refuse(errOut, "bad packet flags")
	}
	if *lane == "" || *who == "" || *dest == "" {
		return refuse(errOut, "--lane, --who and --out are required")
	}
	if *maxBytes <= 0 {
		return refuse(errOut, "--max-bytes must be positive")
	}
	if *maxFlag < 0 {
		return refuse(errOut, "--max must be non-negative")
	}
	if (*pr > 0) == (*branch != "") {
		return refuse(errOut, "give exactly one of --pr or --branch")
	}
	if filepath.IsAbs(*dest) || strings.Contains(filepath.Clean(*dest), "..") {
		return refuse(errOut, "--out must be a relative path without ..")
	}
	if _, err := os.Stat(*dest); err == nil {
		return refuse(errOut, "--out already exists; packets are immutable")
	}
	if *reuse != "" && (len(specs) != 0 || len(rules) != 0 || *maxBytes != 131072) {
		return refuse(errOut, "--reuse cannot be combined with --spec, --rule or --max-bytes")
	}
	st, err := merge.Load(*lane)
	if err != nil {
		return refuse(errOut, fmt.Sprintf("could not read lane: %v", err))
	}
	id, selector := "", ""
	if *pr > 0 {
		id = fmt.Sprint(*pr)
	} else {
		id, selector = *branch, *branch
	}
	entry := st.Find(id)
	if entry == nil {
		return refuse(errOut, "the lane does not hold this entry")
	}
	repo := filepath.Join(*lane, merge.RepoDir)
	current := ""
	if *pr > 0 {
		current, err = hostPRHead(st.Repo, *pr)
	} else {
		current, err = gitOut(repo, "rev-parse", selector)
	}
	if err != nil {
		return refuse(errOut, "could not resolve the entry head")
	}
	current = strings.TrimSpace(current)
	if *asked != "" {
		if !merge.IsSHA(*asked) {
			return refuse(errOut, "--head wants a full 40-character sha")
		}
		if *asked != current {
			fmt.Fprintf(errOut, "PACKET STALE entry=%s asked=%s current=%s: the head moved; build the packet for the current head\n", oneline.Field(id), merge.Short(*asked), merge.Short(current))
			return 1
		}
	}
	var priorRead *merge.Read
	for i := range entry.Reads {
		r := &entry.Reads[i]
		if strings.EqualFold(strings.TrimSpace(r.Who), strings.TrimSpace(*who)) &&
			(strings.EqualFold(r.Verdict, "approve") || strings.EqualFold(r.Verdict, "hold")) {
			if priorRead == nil || r.At > priorRead.At {
				priorRead = r
			}
		}
	}
	base := st.Base
	rangeText := ""
	if priorRead != nil && priorRead.Head != "" {
		base = priorRead.Head
		rangeText = merge.Short(base) + ".." + merge.Short(current)
	} else {
		rangeText = merge.Short(base) + "..." + merge.Short(current)
	}
	if base == "" {
		return refuse(errOut, "the lane has no recorded base; the packet cannot guess its range")
	}
	packetID := packetID(id, current, base, rangeText)
	if *reuse != "" {
		hdr, e := readPacketFirstLine(*reuse)
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse file is not a valid packet: %v", e))
		}
		if hdr.ID != packetID || hdr.Entry != id || hdr.Head != current || hdr.Base != base || hdr.Range != rangeText {
			fmt.Fprintf(errOut, "PACKET REUSE asked=%s found=%s file=%s: that packet was built for another (entry, head, range); build this reader's own\n", packetID, oneline.Field(hdr.ID), oneline.Field(*reuse))
			return 2
		}
		candidateBytes, e := os.ReadFile(*reuse)
		if e != nil {
			return refuse(errOut, "--reuse could not be read")
		}
		content := string(candidateBytes)
		const mThisHead = "## This head\n"
		const mYourPrior = "## Your prior verdicts on this entry\n"
		const mAllVerdicts = "## All verdicts at earlier heads\n"
		idxThisHead := strings.Index(content, mThisHead)
		idxYourPrior := strings.Index(content, mYourPrior)
		idxAllVerdicts := strings.Index(content, mAllVerdicts)
		if idxThisHead == -1 || idxYourPrior == -1 || idxAllVerdicts == -1 || idxThisHead >= idxYourPrior || idxYourPrior >= idxAllVerdicts {
			return refuse(errOut, "--reuse candidate packet is missing required sections")
		}
		headSection := content[idxThisHead:idxYourPrior]
		restSection := content[idxAllVerdicts:]
		yourPriorSection := formatYourPriorVerdicts(*who, entry.Reads, base, current, rangeText)
		bodyRest := headSection + yourPriorSection + "\n" + restSection

		hdrNew := packetHeader{
			ID:    hdr.ID,
			Entry: hdr.Entry,
			Head:  hdr.Head,
			Base:  hdr.Base,
			Range: hdr.Range,
			Who:   *who,
			Built: time.Now().UTC().Format(time.RFC3339),
			Cut:   hdr.Cut,
		}
		newBody := formatPacket(hdrNew, bodyRest)
		if len(newBody) > *maxBytes {
			return refuse(errOut, "--reuse packet exceeds the byte budget")
		}
		files, hunks := diffCounts(restSection)
		ruleCount := strings.Count(restSection, "### ")
		priorCount := len(entry.Reads)
		openCount := 0
		return writePacket(*dest, newBody, out, id, packetID, current, base, rangeText, files, hunks, ruleCount, priorCount, openCount, len(newBody), hdr.Cut, true)
	}
	if _, err := gitOut(repo, "rev-parse", base+"^{commit}"); err != nil {
		return refuse(errOut, "the lane does not hold the recorded base commit")
	}
	diff, err := gitOut(repo, "diff", "--no-ext-diff", "--unified=3", base, current)
	if err != nil {
		return refuse(errOut, "could not read the selected diff")
	}
	fileDiffs := parseFileDiffs(diff)
	files := len(fileDiffs)
	hunks := 0
	for _, f := range fileDiffs {
		hunks += f.Hunks
	}
	ruleText, ruleCount, err := selectedRules(repo, base, current, diff, specs, rules, *maxFlag, *lane)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	intentTitle, intentBody, _ := getAuthorIntent(repo, st.Repo, *pr, *branch, current)
	thisHeadSec := formatThisHead(intentTitle, intentBody)
	yourPriorSec := formatYourPriorVerdicts(*who, entry.Reads, base, current, rangeText)
	allVerdictsSec, priorCount := formatAllVerdicts(entry.Reads, *maxFlag, *lane)
	openFindingsSec, openCount := formatOpenFindings(*maxFlag, *lane)
	rulesTouchedSec := fmt.Sprintf("## Rules touched\n%s\n", ruleText)

	assembleBody := func(diffSec, notIncSec string, cut int) string {
		hdr := packetHeader{
			ID:    packetID,
			Entry: id,
			Head:  current,
			Base:  base,
			Range: rangeText,
			Who:   *who,
			Built: time.Now().UTC().Format(time.RFC3339),
			Cut:   cut,
		}
		bodyRest := thisHeadSec + "\n" +
			yourPriorSec + "\n" +
			allVerdictsSec + "\n" +
			openFindingsSec + "\n" +
			rulesTouchedSec + "\n" +
			diffSec + "\n" +
			notIncSec
		return formatPacket(hdr, bodyRest)
	}

	_, _, cut, body, err := buildDiffAndNotIncluded(fileDiffs, rangeText, *maxBytes, assembleBody)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	return writePacket(*dest, body, out, id, packetID, current, base, rangeText, files, hunks, ruleCount, priorCount, openCount, len(body), cut, false)
}

func gitOut(repo string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = repo
	b, err := c.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
func hostPRHead(repo string, pr int) (string, error) {
	c := exec.Command("gh", "pr", "view", fmt.Sprint(pr), "--repo", repo, "--json", "headRefOid")
	b, err := c.Output()
	if err != nil {
		return "", err
	}
	var v struct {
		Head string `json:"headRefOid"`
	}
	if err := json.Unmarshal(b, &v); err != nil || !merge.IsSHA(v.Head) {
		return "", fmt.Errorf("host returned no full pull request head")
	}
	return v.Head, nil
}
func diffCounts(diff string) (files, hunks int) {
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "diff --git ") {
			files++
		}
		if strings.HasPrefix(l, "@@") {
			hunks++
		}
	}
	return
}

const packetV1Prefix = "nova-review packet v1"

type packetHeader struct {
	ID    string
	Entry string
	Head  string
	Base  string
	Range string
	Who   string
	Built string
	Bytes int
	Cut   int
}

func (h packetHeader) String() string {
	return fmt.Sprintf("%s id=%s entry=%s head=%s base=%s range=%s who=%s built=%s bytes=%d cut=%d",
		packetV1Prefix, h.ID, oneline.Field(h.Entry), h.Head, h.Base, oneline.Field(h.Range), oneline.Field(h.Who), h.Built, h.Bytes, h.Cut)
}

func packetID(entry, head, base, rng string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", entry, head, base, rng)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func formatPacket(hdr packetHeader, rest string) string {
	hdr.Bytes = 0
	candidate := hdr.String() + "\n\n" + rest
	for {
		l := len(candidate)
		if hdr.Bytes == l {
			return candidate
		}
		hdr.Bytes = l
		candidate = hdr.String() + "\n\n" + rest
	}
}

func readPacketFirstLine(path string) (*packetHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var buf [4096]byte
	n, err := io.ReadFull(io.LimitReader(f, 4096), buf[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	content := string(buf[:n])
	firstLine, _, _ := strings.Cut(content, "\n")
	firstLine = strings.TrimRight(firstLine, "\r")
	return parsePacketFirstLine(firstLine)
}

func parsePacketFirstLine(line string) (*packetHeader, error) {
	if !strings.HasPrefix(line, packetV1Prefix+" ") && line != packetV1Prefix {
		return nil, fmt.Errorf("line does not begin with %q", packetV1Prefix)
	}
	rest := strings.TrimPrefix(line, packetV1Prefix)
	fields := strings.Fields(rest)
	hdr := &packetHeader{}
	seen := map[string]bool{}
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, fmt.Errorf("malformed header field %q", f)
		}
		seen[k] = true
		switch k {
		case "id":
			hdr.ID = v
		case "entry":
			hdr.Entry = v
		case "head":
			hdr.Head = v
		case "base":
			hdr.Base = v
		case "range":
			hdr.Range = v
		case "who":
			hdr.Who = v
		case "built":
			hdr.Built = v
		case "bytes":
			b, err := strconv.Atoi(v)
			if err != nil || b < 0 {
				return nil, fmt.Errorf("invalid bytes field %q", v)
			}
			hdr.Bytes = b
		case "cut":
			c, err := strconv.Atoi(v)
			if err != nil || c < 0 {
				return nil, fmt.Errorf("invalid cut field %q", v)
			}
			hdr.Cut = c
		}
	}
	required := []string{"id", "entry", "head", "base", "range", "who", "built", "bytes", "cut"}
	for _, r := range required {
		if !seen[r] {
			return nil, fmt.Errorf("missing required header field %q", r)
		}
	}
	return hdr, nil
}

func lastReadAt(reads []merge.Read, who string) string {
	best := ""
	for _, r := range reads {
		if r.Who == who && r.At > best {
			best = r.At
		}
	}
	return best
}
func selectedRules(repo, base, head, diff string, specFlags, requested []string, maxRules int, lane string) (string, int, error) {
	var specs []scopedSpec
	for _, flag := range specFlags {
		p, heading, err := splitSpecFlag(flag)
		if err != nil {
			return "", 0, err
		}
		text, err := gitOut(repo, "show", head+":"+p)
		if err != nil {
			return "", 0, fmt.Errorf("--spec %s is not readable at head", p)
		}
		spec, err := parseScopedSpec(p, text, heading)
		if err != nil {
			return "", 0, err
		}
		specs = append(specs, spec)
	}
	files := changedFiles(diff)
	selected := map[string]specRule{}
	previous := map[string]specRule{}
	touched := map[string][]string{}
	add := func(rule specRule, why string) {
		key := fmt.Sprintf("%s:%d", rule.Path, rule.Line)
		selected[key] = rule
		touched[key] = append(touched[key], why)
	}
	// Scan only added diff lines for citation grammar.
	currentFile := ""
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git a/") {
			p := strings.Split(strings.TrimPrefix(line, "diff --git a/"), " b/")
			if len(p) == 2 {
				currentFile = p[1]
			}
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			for _, rule := range citationTargets(strings.TrimPrefix(line, "+"), specs) {
				add(rule, fmt.Sprintf("%s (cited)", currentFile))
			}
		}
	}
	for _, flag := range requested {
		p, nText, ok := strings.Cut(flag, ":")
		if !ok || p == "" || nText == "" {
			return "", 0, fmt.Errorf("--rule wants <spec>:<n>")
		}
		n, err := strconv.Atoi(nText)
		if err != nil || n <= 0 {
			return "", 0, fmt.Errorf("--rule wants <spec>:<n>")
		}
		var found specRule
		exists := false
		for _, spec := range specs {
			if spec.Path == p {
				found, exists = spec.Rules[n]
				break
			}
		}
		if !exists {
			return "", 0, fmt.Errorf("--rule %s names no scoped rule at head; add a matching --spec", flag)
		}
		add(found, "caller (named)")
	}
	// A changed spec selects the rule enclosing a changed line. Read the base
	// side too so the rendered section can quote both exact revisions.
	for _, file := range files {
		for _, spec := range specs {
			if file.Path != spec.Path {
				continue
			}
			for _, line := range file.AddedLines {
				for _, rule := range spec.Rules {
					if line >= rule.Line && line < rule.End {
						add(rule, fmt.Sprintf("%s (changed at head)", file.Path))
					}
				}
			}
			oldText, err := gitOut(repo, "show", base+":"+spec.Path)
			if err != nil {
				continue
			}
			old, err := parseScopedSpec(spec.Path, oldText, spec.Heading)
			if err != nil {
				continue
			}
			for _, line := range file.GoneLines {
				for n, rule := range old.Rules {
					if line >= rule.Line && line < rule.End {
						if current, ok := spec.Rules[n]; ok {
							previous[fmt.Sprintf("%s:%d", current.Path, current.Line)] = rule
							add(current, fmt.Sprintf("%s (changed at base)", file.Path))
						} else {
							add(rule, fmt.Sprintf("%s (changed at base; removed at head)", file.Path))
						}
					}
				}
			}
		}
	}
	var out []string
	for _, file := range files {
		count := 0
		for _, rule := range selected {
			for _, why := range touched[fmt.Sprintf("%s:%d", rule.Path, rule.Line)] {
				if strings.HasPrefix(why, file.Path+" ") {
					count++
					break
				}
			}
		}
		out = append(out, fmt.Sprintf("%s: rules=%d", file.Path, count))
	}
	allOrdered := orderedRules(selected)
	limit := len(allOrdered)
	if maxRules > 0 && limit > maxRules {
		limit = maxRules
	}
	for i := 0; i < limit; i++ {
		rule := allOrdered[i]
		key := fmt.Sprintf("%s:%d", rule.Path, rule.Line)
		quoted := fmt.Sprintf("> %s", strings.ReplaceAll(rule.Text, "\n", "\n> "))
		if old, ok := previous[key]; ok {
			quoted = fmt.Sprintf("> head:\n> %s\n> base:\n> %s", strings.ReplaceAll(rule.Text, "\n", "\n> "), strings.ReplaceAll(old.Text, "\n", "\n> "))
		}
		out = append(out, fmt.Sprintf("### %s:%d rule %d\n%s\ntouched by: %s", rule.Path, rule.Line, rule.Number, quoted, strings.Join(touched[key], ", ")))
	}
	if maxRules > 0 && len(allOrdered) > maxRules {
		out = append(out, fmt.Sprintf("%d more of %d; print with: nova-review packet --lane %s ... --rule <spec>:<n>", len(allOrdered)-maxRules, len(allOrdered), lane))
	}
	if len(files) == 0 {
		out = append(out, "No changed files.")
	}
	return strings.Join(out, "\n"), len(selected), nil
}

func getAuthorIntent(repo, hostRepo string, pr int, branch, head string) (title, body string, err error) {
	if pr > 0 && hostRepo != "" {
		c := exec.Command("gh", "pr", "view", fmt.Sprint(pr), "--repo", hostRepo, "--json", "title,body")
		b, err := c.Output()
		if err == nil {
			var v struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			}
			if err := json.Unmarshal(b, &v); err == nil {
				return strings.TrimSpace(v.Title), strings.TrimSpace(v.Body), nil
			}
		}
	}
	if head != "" && repo != "" {
		out, err := gitOut(repo, "log", "-1", "--format=%s%n%n%b", head)
		if err == nil {
			t, b, _ := strings.Cut(out, "\n\n")
			return strings.TrimSpace(t), strings.TrimSpace(b), nil
		}
	}
	return "", "unknown: the lane state has no entry body; no intent is guessed.", nil
}

func formatThisHead(title, body string) string {
	var sb strings.Builder
	sb.WriteString("## This head\n")
	if title != "" {
		sb.WriteString(title + "\n\n")
	}
	sb.WriteString("the author says:\n")
	if body != "" {
		sb.WriteString(body + "\n")
	} else {
		sb.WriteString("(no description provided)\n")
	}
	return sb.String()
}

func formatYourPriorVerdicts(who string, reads []merge.Read, base, current, rng string) string {
	var newest *merge.Read
	for i := range reads {
		r := &reads[i]
		if strings.EqualFold(strings.TrimSpace(r.Who), strings.TrimSpace(who)) &&
			(strings.EqualFold(r.Verdict, "approve") || strings.EqualFold(r.Verdict, "hold")) {
			if newest == nil || r.At > newest.At {
				newest = r
			}
		}
	}
	var sb strings.Builder
	sb.WriteString("## Your prior verdicts on this entry\n")
	if newest != nil {
		sb.WriteString(fmt.Sprintf("%s's newest: %s at %s (%s); range since: %s\n",
			who, strings.ToLower(newest.Verdict), merge.Short(newest.Head), newest.At, rng))
	} else {
		sb.WriteString(fmt.Sprintf("none; this packet is the whole change, %s...%s\n",
			merge.Short(base), merge.Short(current)))
	}
	return sb.String()
}

func formatAllVerdicts(reads []merge.Read, max int, lane string) (string, int) {
	var sb strings.Builder
	sb.WriteString("## All verdicts at earlier heads\n")
	if len(reads) == 0 {
		sb.WriteString("none recorded\n")
		return sb.String(), 0
	}
	sorted := make([]merge.Read, len(reads))
	copy(sorted, reads)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].At > sorted[j].At
	})
	sb.WriteString("| who | model | kind | verdict | head | at |\n")
	limit := len(sorted)
	if max > 0 && limit > max {
		limit = max
	}
	for i := 0; i < limit; i++ {
		r := sorted[i]
		sb.WriteString(fmt.Sprintf("| %s | - | line | %s | %s | %s |\n",
			r.Who, strings.ToLower(r.Verdict), merge.Short(r.Head), r.At))
	}
	if max > 0 && len(sorted) > max {
		sb.WriteString(fmt.Sprintf("%d more of %d; print with: nova-review roster --lane %s ... --max 0\n",
			len(sorted)-max, len(sorted), lane))
	}
	return sb.String(), len(reads)
}

func formatOpenFindings(max int, lane string) (string, int) {
	var sb strings.Builder
	sb.WriteString("## Open findings (answer with `dup <id>` if you see the same thing)\n")
	sb.WriteString("none recorded\n")
	return sb.String(), 0
}

type fileDiff struct {
	Header   string
	Path     string
	Hunks    int
	Added    int
	Deleted  int
	FullText string
}

func parseFileDiffs(diff string) []fileDiff {
	var files []fileDiff
	lines := strings.Split(diff, "\n")
	var cur *fileDiff
	var curLines []string

	flush := func() {
		if cur != nil {
			cur.FullText = strings.Join(curLines, "\n")
			files = append(files, *cur)
			cur = nil
			curLines = nil
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur = &fileDiff{Header: line}
			parts := strings.Split(strings.TrimPrefix(line, "diff --git a/"), " b/")
			if len(parts) == 2 {
				cur.Path = parts[1]
			}
			curLines = append(curLines, line)
			continue
		}
		if cur == nil {
			continue
		}
		curLines = append(curLines, line)
		if strings.HasPrefix(line, "@@") {
			cur.Hunks++
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			cur.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			cur.Deleted++
		}
	}
	flush()
	return files
}

func buildDiffAndNotIncluded(files []fileDiff, rangeText string, maxBytes int, assemble func(diffSec, notIncSec string, cut int) string) (string, string, int, string, error) {
	var allDiffLines []string
	for _, f := range files {
		allDiffLines = append(allDiffLines, f.FullText)
	}
	diffFull := strings.Join(allDiffLines, "\n")
	diffSection := fmt.Sprintf("## Diff %s\n```diff\n%s\n```\n", rangeText, diffFull)
	notIncludedSection := "## Not included\nnothing\n"
	candidate := assemble(diffSection, notIncludedSection, 0)
	if len(candidate) <= maxBytes {
		return diffSection, notIncludedSection, 0, candidate, nil
	}

	var included []fileDiff
	var cutFiles []fileDiff

	for i := 0; i < len(files); i++ {
		testIncluded := append(included, files[i])
		testCut := append([]fileDiff{}, cutFiles...)
		for j := i + 1; j < len(files); j++ {
			testCut = append(testCut, files[j])
		}

		var incLines []string
		for _, f := range testIncluded {
			incLines = append(incLines, f.FullText)
		}
		var testDiffSec string
		if len(incLines) > 0 {
			testDiffSec = fmt.Sprintf("## Diff %s\n```diff\n%s\n```\n", rangeText, strings.Join(incLines, "\n"))
		} else {
			testDiffSec = fmt.Sprintf("## Diff %s\n```diff\n```\n", rangeText)
		}

		var notIncLines []string
		notIncLines = append(notIncLines, "## Not included")
		for _, cf := range testCut {
			notIncLines = append(notIncLines, fmt.Sprintf("%s: %d hunks, +%d -%d; print with: git diff %s -- %s",
				cf.Path, cf.Hunks, cf.Added, cf.Deleted, rangeText, cf.Path))
		}
		testNotIncSec := strings.Join(notIncLines, "\n") + "\n"

		testCand := assemble(testDiffSec, testNotIncSec, len(testCut))
		if len(testCand) <= maxBytes {
			included = append(included, files[i])
		} else {
			cutFiles = append(cutFiles, files[i])
		}
	}

	var finalIncLines []string
	for _, f := range included {
		finalIncLines = append(finalIncLines, f.FullText)
	}
	var finalDiffSec string
	if len(finalIncLines) > 0 {
		finalDiffSec = fmt.Sprintf("## Diff %s\n```diff\n%s\n```\n", rangeText, strings.Join(finalIncLines, "\n"))
	} else {
		finalDiffSec = fmt.Sprintf("## Diff %s\n```diff\n```\n", rangeText)
	}

	var notIncLines []string
	notIncLines = append(notIncLines, "## Not included")
	if len(cutFiles) == 0 {
		notIncLines = append(notIncLines, "nothing")
	} else {
		for _, cf := range cutFiles {
			notIncLines = append(notIncLines, fmt.Sprintf("%s: %d hunks, +%d -%d; print with: git diff %s -- %s",
				cf.Path, cf.Hunks, cf.Added, cf.Deleted, rangeText, cf.Path))
		}
	}
	finalNotIncSec := strings.Join(notIncLines, "\n") + "\n"
	cutCount := len(cutFiles)
	finalCand := assemble(finalDiffSec, finalNotIncSec, cutCount)
	if len(finalCand) > maxBytes {
		return "", "", 0, "", fmt.Errorf("--max-bytes %d cannot hold packet metadata and bounded remedy (%d bytes)", maxBytes, len(finalCand))
	}
	return finalDiffSec, finalNotIncSec, cutCount, finalCand, nil
}

func writePacket(dest, body string, out io.Writer, entry, id, head, base, rng string, files, hunks, rules, prior, open, bytesN, cut int, reused bool) int {
	if err := writeExclusive(dest, []byte(body)); err != nil {
		fmt.Fprintf(os.Stderr, "PACKET REFUSED: could not exclusively create --out: %s\n", oneline.Escape(err.Error()))
		return 2
	}
	fmt.Fprintf(out, "PACKET OK entry=%s id=%s head=%s base=%s range=%s files=%d hunks=%d rules=%d prior=%d open=%d bytes=%d cut=%d reused=%t out=%s\n",
		oneline.Field(entry), id, merge.Short(head), merge.Short(base), oneline.Field(rng), files, hunks, rules, prior, open, bytesN, cut, reused, oneline.Field(dest))
	return 0
}

func writeExclusive(dest string, body []byte) error {
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	var randBytes [3]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return err
	}
	randHex := hex.EncodeToString(randBytes[:])
	tmpName := filepath.Join(dir, fmt.Sprintf("%s.%d-%s.tmp", base, os.Getpid(), randHex))
	tmp, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, dest); err != nil {
		return err
	}
	cleaned = true
	return nil
}
