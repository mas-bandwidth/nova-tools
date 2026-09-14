// nova-review packet builds a bounded, exact-revision source-review artifact.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-review: bounded exact-revision review packets (docs/SPEC-REVIEW.md)

usage:
  nova-review packet --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max-bytes <n>]
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
	base := st.Base
	// A reader's next packet starts at the last head that reader actually
	// recorded, whatever their verdict. State is the durable fold of reads.
	for _, read := range entry.Reads {
		if read.Who == *who && read.Head != "" && read.At >= lastReadAt(entry.Reads, *who) {
			base = read.Head
		}
	}
	if base == "" {
		return refuse(errOut, "the lane has no recorded base; the packet cannot guess its range")
	}
	if _, err := gitOut(repo, "rev-parse", base+"^{commit}"); err != nil {
		return refuse(errOut, "the lane does not hold the recorded base commit")
	}
	rangeText := merge.Short(base) + ".." + merge.Short(current)
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
		body, e := os.ReadFile(*reuse)
		if e != nil {
			return refuse(errOut, "--reuse could not be read")
		}
		if len(body) > *maxBytes {
			return refuse(errOut, "--reuse packet exceeds the byte budget")
		}
		files, hunks := diffCounts(string(body))
		return writePacket(*dest, string(body), out, id, packetID, current, base, rangeText, files, hunks, 0, len(body), hdr.Cut, true)
	}
	diff, err := gitOut(repo, "diff", "--no-ext-diff", "--unified=3", base, current)
	if err != nil {
		return refuse(errOut, "could not read the selected diff")
	}
	files, hunks := diffCounts(diff)
	ruleText, ruleCount, err := selectedRules(repo, base, current, diff, specs, rules)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	hdr := packetHeader{
		ID:    packetID,
		Entry: id,
		Head:  current,
		Base:  base,
		Range: rangeText,
		Who:   *who,
		Built: time.Now().UTC().Format(time.RFC3339),
		Cut:   0,
	}
	sections := fmt.Sprintf("## Author intent (data)\nunknown: the lane state has no entry body; no intent is guessed.\n\n## Prior verdicts\nunknown: verdict records are not implemented by this packet-only slice.\n\n## Open findings\nunknown: finding records are not implemented by this packet-only slice.\n\n## Rules touched\n%s\n\n## Diff\n", ruleText)
	candidateBody := formatPacket(hdr, sections+diff+"\n## Not included\nnone\n")
	if len(candidateBody) > *maxBytes {
		hunksText, e := gitOut(repo, "diff", "--no-ext-diff", "--unified=0", "--stat", base, current)
		if e != nil {
			return refuse(errOut, "could not build the bounded hunk list")
		}
		remedy := fmt.Sprintf("\n## Diff omitted\nThe selected diff exceeds --max-bytes. Read it with:\n\ngit -C %s diff --no-ext-diff %s %s\n\n%s", repo, base, current, hunksText)
		diff = remedy
		hdr.Cut = files
		body := formatPacket(hdr, sections+diff+"\n## Not included\nfull diff omitted by byte budget\n")
		if len(body) > *maxBytes {
			return refuse(errOut, fmt.Sprintf("--max-bytes %d cannot hold packet metadata and bounded remedy (%d bytes)", *maxBytes, len(body)))
		}
		return writePacket(*dest, body, out, id, packetID, current, base, rangeText, files, hunks, ruleCount, len(body), hdr.Cut, false)
	}
	return writePacket(*dest, candidateBody, out, id, packetID, current, base, rangeText, files, hunks, ruleCount, len(candidateBody), 0, false)
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
func selectedRules(repo, base, head, diff string, specFlags, requested []string) (string, int, error) {
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
	for _, rule := range orderedRules(selected) {
		key := fmt.Sprintf("%s:%d", rule.Path, rule.Line)
		quoted := fmt.Sprintf("> %s", strings.ReplaceAll(rule.Text, "\n", "\n> "))
		if old, ok := previous[key]; ok {
			quoted = fmt.Sprintf("> head:\n> %s\n> base:\n> %s", strings.ReplaceAll(rule.Text, "\n", "\n> "), strings.ReplaceAll(old.Text, "\n", "\n> "))
		}
		out = append(out, fmt.Sprintf("### %s:%d rule %d\n%s\ntouched by: %s", rule.Path, rule.Line, rule.Number, quoted, strings.Join(touched[key], ", ")))
	}
	if len(files) == 0 {
		out = append(out, "No changed files.")
	}
	return strings.Join(out, "\n"), len(selected), nil
}
func writePacket(dest, body string, out io.Writer, entry, id, head, base, rng string, files, hunks, rules, bytesN, cut int, reused bool) int {
	if err := writeExclusive(dest, []byte(body)); err != nil {
		fmt.Fprintf(os.Stderr, "PACKET REFUSED: could not exclusively create --out: %s\n", oneline.Escape(err.Error()))
		return 2
	}
	fmt.Fprintf(out, "PACKET OK entry=%s id=%s head=%s base=%s range=%s files=%d hunks=%d rules=%d prior=0 open=0 bytes=%d cut=%d reused=%t out=%s\n", oneline.Field(entry), id, merge.Short(head), merge.Short(base), oneline.Field(rng), files, hunks, rules, bytesN, cut, reused, oneline.Field(dest))
	return 0
}

// writeExclusive never follows or replaces an existing output. The hard link is the
// exclusive publication step: unlike rename it fails if another packet won the name.
func writeExclusive(dest string, body []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".nova-review-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(body); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmpName, dest); err != nil {
		return err
	}
	return nil
}
