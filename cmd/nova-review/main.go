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
	"regexp"
	"strings"

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
	if base == "" {
		return refuse(errOut, "the lane has no recorded base; the packet cannot guess its range")
	}
	if _, err := gitOut(repo, "rev-parse", base+"^{commit}"); err != nil {
		return refuse(errOut, "the lane does not hold the recorded base commit")
	}
	rangeText := merge.Short(base) + ".." + merge.Short(current)
	packetID := packetID(id, current, base)
	if *reuse != "" {
		body, e := os.ReadFile(*reuse)
		if e != nil {
			return refuse(errOut, "--reuse could not be read")
		}
		fields := packetFields(string(body))
		if fields["id"] != packetID || fields["entry"] != id || fields["head"] != current || fields["base"] != base {
			fmt.Fprintf(errOut, "PACKET REUSE asked=%s found=%s file=%s: that packet was built for another (entry, head, range); build this reader's own\n", packetID, oneline.Field(fields["id"]), oneline.Field(*reuse))
			return 2
		}
		if len(body) > *maxBytes {
			return refuse(errOut, "--reuse packet exceeds the byte budget")
		}
		files, hunks := diffCounts(string(body))
		return writePacket(*dest, string(body), out, id, packetID, current, base, rangeText, files, hunks, 0, len(body), 0, true)
	}
	diff, err := gitOut(repo, "diff", "--no-ext-diff", "--unified=3", base, current)
	if err != nil {
		return refuse(errOut, "could not read the selected diff")
	}
	files, hunks := diffCounts(diff)
	ruleText, ruleCount, err := quotedRules(repo, current, specs, rules)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	header := fmt.Sprintf("NOVA-REVIEW PACKET v1\nid=%s\nentry=%s\nwho=%s\nhead=%s\nbase=%s\nrange=%s\nbuilt_from=lane-checkout\n\n## Author intent (data)\nunknown: the lane state has no entry body; no intent is guessed.\n\n## Prior verdicts\nunknown: verdict records are not implemented by this packet-only slice.\n\n## Open findings\nunknown: finding records are not implemented by this packet-only slice.\n\n## Rules touched\n%s\n\n## Diff\n", packetID, id, *who, current, base, rangeText, ruleText)
	if len(header)+len(diff)+len("\n## Not included\nnone\n") > *maxBytes {
		hunksText, e := gitOut(repo, "diff", "--no-ext-diff", "--unified=0", "--stat", base, current)
		if e != nil {
			return refuse(errOut, "could not build the bounded hunk list")
		}
		remedy := fmt.Sprintf("\n## Diff omitted\nThe selected diff exceeds --max-bytes. Read it with:\n\ngit -C %s diff --no-ext-diff %s %s\n\n%s", repo, base, current, hunksText)
		diff = remedy
		cut := files
		body := header + diff + "\n## Not included\nfull diff omitted by byte budget\n"
		if len(body) > *maxBytes {
			return refuse(errOut, fmt.Sprintf("--max-bytes %d cannot hold packet metadata and bounded remedy (%d bytes)", *maxBytes, len(body)))
		}
		return writePacket(*dest, body, out, id, packetID, current, base, rangeText, files, hunks, ruleCount, len(body), cut, false)
	}
	body := header + diff + "\n## Not included\nnone\n"
	return writePacket(*dest, body, out, id, packetID, current, base, rangeText, files, hunks, ruleCount, len(body), 0, false)
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
func packetID(entry, head, base string) string {
	s := sha256.Sum256([]byte(entry + "\x00" + head + "\x00" + base))
	return hex.EncodeToString(s[:])[:12]
}
func packetFields(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		if key == "id" || key == "entry" || key == "head" || key == "base" {
			out[key] = value
		}
	}
	return out
}
func quotedRules(repo, head string, specs, rules []string) (string, int, error) {
	if len(specs) == 0 && len(rules) == 0 {
		return "No caller-named spec rules; uncited changed lines are not guessed.", 0, nil
	}
	var out []string
	for _, rule := range rules {
		p, n, ok := strings.Cut(rule, ":")
		if !ok || p == "" || n == "" {
			return "", 0, fmt.Errorf("--rule wants <spec>:<n>")
		}
		text, err := gitOut(repo, "show", head+":"+p)
		if err != nil {
			return "", 0, fmt.Errorf("--rule %s is not readable at head", rule)
		}
		re := regexp.MustCompile("(?m)^" + regexp.QuoteMeta(n) + `\. .*$`)
		hit := re.FindString(text)
		if hit == "" {
			return "", 0, fmt.Errorf("--rule %s names no rule at head", rule)
		}
		out = append(out, fmt.Sprintf("%s: %s", p, hit))
	}
	if len(out) == 0 {
		return "No rules were mechanically selected; uncited changed lines are not guessed.", 0, nil
	}
	return strings.Join(out, "\n"), len(out), nil
}
func writePacket(dest, body string, out io.Writer, entry, id, head, base, rng string, files, hunks, rules, bytesN, cut int, reused bool) int {
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return 2
	}
	fmt.Fprintf(out, "PACKET OK entry=%s id=%s head=%s base=%s range=%s files=%d hunks=%d rules=%d prior=0 open=0 bytes=%d cut=%d reused=%t out=%s\n", oneline.Field(entry), id, merge.Short(head), merge.Short(base), oneline.Field(rng), files, hunks, rules, bytesN, cut, reused, oneline.Field(dest))
	return 0
}
