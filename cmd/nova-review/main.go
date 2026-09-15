// nova-review packet builds a bounded, exact-revision source-review artifact.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
  nova-review packet --lane <nova-merge lane dir> (--pr <n>|--branch <name>) --who <name> --out <file, relative to the cwd or absolute under the cwd or the lane> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max <n>] [--max-bytes <n>] [--reuse <file>] [--timeout <seconds>]
  nova-review version    print this build identity (--version also accepted)
  nova-review help

example:
  nova-review --version
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func refuse(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PACKET REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

type foldErr struct {
	file string
	err  error
}

func (e *foldErr) Error() string { return e.err.Error() }
func (e *foldErr) Unwrap() error { return e.err }

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "PACKET REFUSED: no verb given; run: nova-review help")
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	case "version", "--version":
		return cmdVersion(args[1:], out, errOut)
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
	timeout := fs.Int("timeout", 120, "")
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
	if *timeout <= 0 {
		return refuse(errOut, "--timeout must be positive")
	}
	if (*pr > 0) == (*branch != "") {
		return refuse(errOut, "give exactly one of --pr or --branch")
	}
	if outEscapes(*dest, *lane) {
		return refuse(errOut, "--out escapes the current directory and the lane; give a relative path under the current directory or an absolute path under the current directory or the lane")
	}
	if _, err := os.Lstat(*dest); err == nil {
		return refuse(errOut, "--out already exists; packets are immutable")
	}
	if *reuse != "" && (len(specs) != 0 || len(rules) != 0 || *maxBytes != 131072) {
		return refuse(errOut, "--reuse cannot be combined with --spec, --rule or --max-bytes")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	st, err := merge.Load(*lane)
	if err != nil {
		if errors.Is(err, merge.ErrNotALane) {
			return refuse(errOut, fmt.Sprintf("--lane %s is not a lane; a lane is a directory made by nova-merge init --lane <dir> --repo <owner/name> --base <branch> --lane-branch <name>", *lane))
		}
		return refuse(errOut, fmt.Sprintf("could not read lane: %v", err))
	}
	id := ""
	if *pr > 0 {
		id = fmt.Sprint(*pr)
	} else {
		id = *branch
	}
	entry := st.Find(id)
	if entry == nil {
		return refuse(errOut, "the lane does not hold this entry; add it with nova-merge add --lane <dir> --pr <n> --needs-read (or add-branch --branch <name>)")
	}
	repo := filepath.Join(*lane, merge.RepoDir)
	oldHead := entry.OID
	current, err := fetchEntryHead(ctx, repo, *pr, *branch, st.Repo)
	if err != nil {
		if *asked != "" && merge.IsSHA(*asked) {
			current = *asked
			fmt.Fprintf(errOut, "PACKET NOTE fetch failed, using --head\n")
		} else {
			return refuse(errOut, fmt.Sprintf("could not fetch the entry head: %v", err))
		}
	}
	current = strings.TrimSpace(current)
	if err == nil && oldHead != "" && current != oldHead {
		fmt.Fprintf(errOut, "PACKET NOTE head moved %s -> %s\n", merge.Short(oldHead), merge.Short(current))
		if uerr := merge.Update(*lane, time.Duration(*timeout)*time.Second, func(s *merge.State) error {
			e := s.Find(id)
			if e == nil {
				return fmt.Errorf("the lane no longer holds this entry")
			}
			e.OID = current
			return nil
		}); uerr != nil {
			return refuse(errOut, fmt.Sprintf("could not record the moved head: %v", uerr))
		}
	}
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
	if base == "" {
		return refuse(errOut, "the lane has no recorded base; the packet cannot guess its range")
	}
	// The lane clone's local base branch goes stale: origin advances past a checked-out
	// ref that has not been fetched, and a range built against the stale branch drags in
	// every file that moved between the two (#418). Fetch the base before the range so
	// the range's left side is the remote's current tip, never the idle local one. A base
	// that is already a full sha names one commit and cannot go stale, so it needs no fetch.
	baseSHA := base
	if !merge.IsSHA(base) {
		baseSHA, err = fetchBase(ctx, repo, *pr, base, st.Repo)
		if err != nil {
			return refuse(errOut, err.Error())
		}
	}
	rangeText := ""
	var fullRange string
	var diffBase string
	if priorRead != nil && priorRead.Head != "" {
		base = priorRead.Head
		baseSHA = base
		rangeText = merge.Short(base) + ".." + merge.Short(current)
		fullRange = base + ".." + current
		diffBase = base
	} else {
		rangeText = merge.Short(base) + "..." + merge.Short(current)
		fullRange = baseSHA + "..." + current
	}
	packetID := packetID(id, current, base, rangeText)
	if *reuse != "" {
		hdr, content, e := readReusePacket(*reuse, *maxBytes)
		if e != nil {
			return refuse(errOut, e.Error())
		}
		if hdr.ID != packetID || hdr.Entry != id || hdr.Head != current || hdr.Base != base || hdr.Range != rangeText {
			fmt.Fprintf(errOut, "PACKET REUSE asked=%s found=%s file=%s: that packet was built for another (entry, head, range); build this reader's own\n", packetID, oneline.Field(hdr.ID), oneline.Field(*reuse))
			return 2
		}
		sections, err := splitPacketSections(content)
		if err != nil {
			return refuse(errOut, "--reuse candidate packet is missing required sections or malformed")
		}
		yourPriorSec := formatYourPriorVerdicts(*who, entry.Reads, base, current, rangeText)
		bodyRest := sections.thisHead + yourPriorSec + "\n" +
			sections.allVerdicts +
			sections.openFindings +
			sections.rulesTouched +
			sections.diff +
			sections.notIncluded

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
		priorCount := parseSectionCount(sections.allVerdicts, "| who |")
		openCount := parseSectionCount(sections.openFindings, "| id |")
		ruleCount, err := packetRulesSectionCount(sections.rulesTouched)
		if err != nil {
			return refuse(errOut, err.Error())
		}
		diffFiles, diffHunks := diffCounts(sections.diff)
		cutFiles, cutHunks := notIncludedCounts(sections.notIncluded)
		files := diffFiles + cutFiles
		hunks := diffHunks + cutHunks
		return writePacket(*dest, newBody, out, id, packetID, current, base, baseSHA, rangeText, files, hunks, ruleCount, priorCount, openCount, len(newBody), hdr.Cut, true)
	}

	if diffBase == "" {
		mb, err := gitOut(ctx, repo, "merge-base", baseSHA, current)
		if err != nil {
			return refuse(errOut, "could not determine merge-base for triple-dot range")
		}
		diffBase = strings.TrimSpace(mb)
	}

	if _, err := gitOut(ctx, repo, "rev-parse", baseSHA+"^{commit}"); err != nil {
		return refuse(errOut, "the lane does not hold the recorded base commit")
	}
	diff, err := gitOut(ctx, repo, "diff", "--no-ext-diff", "--unified=3", fullRange)
	if err != nil {
		return refuse(errOut, "could not read the selected diff")
	}
	fileDiffs := parseFileDiffs(diff)
	files := len(fileDiffs)
	hunks := 0
	for _, f := range fileDiffs {
		hunks += f.Hunks
	}
	ruleText, ruleCount, err := selectedRules(ctx, repo, diffBase, current, diff, specs, rules, *maxFlag, *lane)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	intentTitle, intentBody, _ := getAuthorIntent(ctx, repo, st.Repo, *pr, *branch, current)
	thisHeadSec := formatThisHead(intentTitle, intentBody)
	yourPriorSec := formatYourPriorVerdicts(*who, entry.Reads, base, current, rangeText)
	allVerdictsSec, priorCount := formatAllVerdicts(entry.Reads, *maxFlag, *lane, *pr, *branch)
	openFindingsSec, openCount, err := formatOpenFindings(*lane, id, *maxFlag, *pr, *branch)
	if err != nil {
		var fe *foldErr
		if errors.As(err, &fe) {
			fmt.Fprintf(errOut, "PACKET FOLD file=%s: %s\n", oneline.Field(fe.file), oneline.Escape(fe.err.Error()))
			return 2
		}
		return refuse(errOut, err.Error())
	}
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
	if *maxFlag > 0 && priorCount > *maxFlag {
		entryFlag := ""
		if *pr > 0 {
			entryFlag = fmt.Sprintf("--pr %d", *pr)
		} else {
			entryFlag = fmt.Sprintf("--branch %s", shellQuote(*branch))
		}
		fmt.Fprintf(out, "PACKET MORE kind=prior shown=%d total=%d nova-review packet --lane %s %s --max 0\n", *maxFlag, priorCount, shellQuote(*lane), entryFlag)
	}
	return writePacket(*dest, body, out, id, packetID, current, base, baseSHA, rangeText, files, hunks, ruleCount, priorCount, openCount, len(body), cut, false)
}

func gitOut(ctx context.Context, repo string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = repo
	stdout, err := c.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return "", err
	}
	const maxRead = 16 * 1024 * 1024
	b, err := io.ReadAll(io.LimitReader(stdout, maxRead+1))
	if err != nil {
		_ = c.Process.Kill()
		return "", err
	}
	if len(b) > maxRead {
		_ = c.Process.Kill()
		return "", fmt.Errorf("git %s output exceeded limit (%d bytes)", args[0], maxRead)
	}
	if err := c.Wait(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return string(b), nil
}

// fetchEntryHead fetches the entry's current head into the lane's clone: the pull request's
// `pull/<n>/head` for a PR, the branch itself for a branch, then reads the fetched commit
// back out of FETCH_HEAD. The fetch is the verb's one way to learn a head the remote moved
// (a force-push) without trusting a local ref that has not been updated.
//
// A PR head comes from the GitHub remote the lane's --repo names (https://github.com/<owner>/<name>.git),
// never from the lane's --remote: --remote is the record-branch push target, and a local
// rehearsal remote has no pull/*/head refs (#449). A branch entry still fetches from the
// lane remote as before.
func fetchEntryHead(ctx context.Context, repo string, pr int, branch, hostRepo string) (string, error) {
	refspec := branch
	remote := "origin"
	if pr > 0 {
		refspec = fmt.Sprintf("pull/%d/head", pr)
		remote = fmt.Sprintf("https://github.com/%s.git", hostRepo)
	}
	if _, err := gitOut(ctx, repo, "fetch", remote, refspec); err != nil {
		return "", fmt.Errorf("fetching %q from %q: %w", refspec, remote, err)
	}
	return gitOut(ctx, repo, "rev-parse", "FETCH_HEAD")
}

// fetchBase fetches the merge base into the lane's clone and returns its sha.
// A PR's base lives on the GitHub remote the lane's --repo names, never on the
// lane's --remote: a local rehearsal remote has no main branch (#493). A branch
// entry still fetches its base from the lane remote as before.
func fetchBase(ctx context.Context, repo string, pr int, base, hostRepo string) (string, error) {
	remote := "origin"
	refOut := "refs/remotes/origin/" + base + "^{commit}"
	if pr > 0 {
		remote = fmt.Sprintf("https://github.com/%s.git", hostRepo)
		refOut = "FETCH_HEAD"
	}
	if _, err := gitOut(ctx, repo, "fetch", remote, base); err != nil {
		return "", fmt.Errorf("could not fetch the base %q from %q: %v", base, remote, err)
	}
	fetched, err := gitOut(ctx, repo, "rev-parse", refOut)
	if err != nil {
		return "", fmt.Errorf("the lane does not hold the fetched base %q: %v", base, err)
	}
	return strings.TrimSpace(fetched), nil
}

// outEscapes reports whether --out, resolved against the current directory, lies outside
// both the current directory and the lane directory. A path accepted here is under one of
// the two; a path that escapes both is refused.
func outEscapes(dest, lane string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return true
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(cwd, dest)
	}
	if underDir(cwd, dest) {
		return false
	}
	return !underDir(lane, dest)
}

func underDir(root, path string) bool {
	rootAbs, err1 := filepath.Abs(root)
	pathAbs, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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

func isLowerHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validRangeFormat(r string) bool {
	if strings.Contains(r, "...") {
		parts := strings.Split(r, "...")
		return len(parts) == 2 && len(parts[0]) == 12 && isLowerHex(parts[0]) && len(parts[1]) == 12 && isLowerHex(parts[1])
	}
	if strings.Contains(r, "..") {
		parts := strings.Split(r, "..")
		return len(parts) == 2 && len(parts[0]) == 12 && isLowerHex(parts[0]) && len(parts[1]) == 12 && isLowerHex(parts[1])
	}
	return false
}

func readPacketFirstLine(path string) (*packetHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("packet %s is not a regular file", path)
	}

	var buf [4096]byte
	n, err := io.ReadFull(io.LimitReader(f, 4096), buf[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("packet file is empty")
	}
	idx := bytes.IndexByte(buf[:n], '\n')
	if idx == -1 {
		if n == 4096 {
			return nil, fmt.Errorf("overlong unterminated header line")
		}
		return nil, fmt.Errorf("unterminated header line")
	}
	firstLine := strings.TrimRight(string(buf[:idx]), "\r")
	return parsePacketFirstLine(firstLine)
}

func parsePacketFirstLine(line string) (*packetHeader, error) {
	if !strings.HasPrefix(line, packetV1Prefix+" ") {
		return nil, fmt.Errorf("line does not begin with %q", packetV1Prefix)
	}
	rest := strings.TrimPrefix(line, packetV1Prefix+" ")
	fields := strings.Split(rest, " ")
	hdr := &packetHeader{}
	seen := map[string]bool{}
	allowed := map[string]bool{
		"id":    true,
		"entry": true,
		"head":  true,
		"base":  true,
		"range": true,
		"who":   true,
		"built": true,
		"bytes": true,
		"cut":   true,
	}
	for _, f := range fields {
		if f == "" {
			return nil, fmt.Errorf("consecutive spaces in header line")
		}
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, fmt.Errorf("malformed header field %q", f)
		}
		if !allowed[k] {
			return nil, fmt.Errorf("unknown header field %q", k)
		}
		if seen[k] {
			return nil, fmt.Errorf("duplicate header field %q", k)
		}
		seen[k] = true
		switch k {
		case "id":
			if len(v) != 12 || !isLowerHex(v) {
				return nil, fmt.Errorf("invalid id field %q: must be 12 lower-hex characters", v)
			}
			hdr.ID = v
		case "entry":
			if v == "" || strings.ContainsAny(v, " \t\r\n") {
				return nil, fmt.Errorf("invalid entry field %q", v)
			}
			hdr.Entry = v
		case "head":
			if !merge.IsSHA(v) {
				return nil, fmt.Errorf("invalid head field %q: must be 40-character sha", v)
			}
			hdr.Head = v
		case "base":
			if !merge.IsSHA(v) {
				return nil, fmt.Errorf("invalid base field %q: must be 40-character sha", v)
			}
			hdr.Base = v
		case "range":
			if !validRangeFormat(v) {
				return nil, fmt.Errorf("invalid range field %q", v)
			}
			hdr.Range = v
		case "who":
			if v == "" || strings.ContainsAny(v, " \t\r\n") {
				return nil, fmt.Errorf("invalid who field %q", v)
			}
			hdr.Who = v
		case "built":
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return nil, fmt.Errorf("invalid built timestamp %q: %v", v, err)
			}
			hdr.Built = v
		case "bytes":
			b, err := strconv.Atoi(v)
			if err != nil || b <= 0 {
				return nil, fmt.Errorf("invalid bytes field %q: must be positive integer", v)
			}
			hdr.Bytes = b
		case "cut":
			c, err := strconv.Atoi(v)
			if err != nil || c < 0 {
				return nil, fmt.Errorf("invalid cut field %q: must be non-negative integer", v)
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
	parts := strings.Split(hdr.Range, "...")
	if len(parts) != 2 {
		parts = strings.Split(hdr.Range, "..")
	}
	if len(parts) == 2 {
		if !strings.HasPrefix(hdr.Base, parts[0]) || !strings.HasPrefix(hdr.Head, parts[1]) {
			return nil, fmt.Errorf("range %q does not match base %s and head %s", hdr.Range, hdr.Base, hdr.Head)
		}
	}
	return hdr, nil
}

func readReusePacket(path string, maxBytes int) (*packetHeader, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !fi.Mode().IsRegular() {
		return nil, "", fmt.Errorf("--reuse candidate %s is not a regular file", path)
	}
	if fi.Size() > int64(maxBytes) {
		return nil, "", fmt.Errorf("--reuse packet exceeds the byte budget")
	}
	contentBytes, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return nil, "", err
	}
	if len(contentBytes) > maxBytes {
		return nil, "", fmt.Errorf("--reuse packet exceeds the byte budget")
	}
	idx := bytes.IndexByte(contentBytes, '\n')
	if idx == -1 {
		return nil, "", fmt.Errorf("--reuse candidate has unterminated header")
	}
	firstLine := strings.TrimRight(string(contentBytes[:idx]), "\r")
	hdr, err := parsePacketFirstLine(firstLine)
	if err != nil {
		return nil, "", fmt.Errorf("--reuse file is not a valid packet: %w", err)
	}
	if len(contentBytes) != hdr.Bytes {
		return nil, "", fmt.Errorf("--reuse packet byte count mismatch: file has %d bytes, header claims %d", len(contentBytes), hdr.Bytes)
	}
	return hdr, string(contentBytes), nil
}

type packetSections struct {
	thisHead     string
	yourPrior    string
	allVerdicts  string
	openFindings string
	rulesTouched string
	diff         string
	notIncluded  string
}

func splitPacketSections(text string) (packetSections, error) {
	const (
		mThisHead     = "## This head\n"
		mYourPrior    = "## Your prior verdicts on this entry\n"
		mAllVerdicts  = "## All verdicts at earlier heads\n"
		mOpenFindings = "## Open findings (answer with `dup <id>` if you see the same thing)\n"
		mRulesTouched = "## Rules touched\n"
		mDiff         = "## Diff "
		mNotIncluded  = "## Not included\n"
	)

	findHeader := func(s, prefix string) int {
		if strings.HasPrefix(s, prefix) {
			return 0
		}
		idx := strings.Index(s, "\n"+prefix)
		if idx != -1 {
			return idx + 1
		}
		return -1
	}

	idxThisHead := findHeader(text, mThisHead)
	if idxThisHead == -1 {
		return packetSections{}, fmt.Errorf("packet missing '## This head' section")
	}

	idxAllVerdicts := findHeader(text, mAllVerdicts)
	if idxAllVerdicts == -1 || idxAllVerdicts <= idxThisHead {
		return packetSections{}, fmt.Errorf("packet missing or misplaced '## All verdicts at earlier heads' section")
	}

	// Find the true yourPrior section, which immediately precedes allVerdicts.
	// We search for the last occurrence of mYourPrior before allVerdicts to ensure
	// any mention of that heading in author text within thisHead is preserved.
	beforeAll := text[:idxAllVerdicts]
	idxYourPrior := -1
	lastIdx := strings.LastIndex(beforeAll, "\n"+mYourPrior)
	if lastIdx != -1 {
		idxYourPrior = lastIdx + 1
	} else if strings.HasPrefix(beforeAll, mYourPrior) {
		idxYourPrior = 0
	}
	if idxYourPrior == -1 || idxYourPrior <= idxThisHead {
		return packetSections{}, fmt.Errorf("packet missing or misplaced '## Your prior verdicts on this entry' section")
	}

	idxOpenFindingsRel := findHeader(text[idxAllVerdicts:], mOpenFindings)
	if idxOpenFindingsRel == -1 {
		return packetSections{}, fmt.Errorf("packet missing '## Open findings' section")
	}
	idxOpenFindings := idxAllVerdicts + idxOpenFindingsRel

	idxRulesTouchedRel := findHeader(text[idxOpenFindings:], mRulesTouched)
	if idxRulesTouchedRel == -1 {
		return packetSections{}, fmt.Errorf("packet missing '## Rules touched' section")
	}
	idxRulesTouched := idxOpenFindings + idxRulesTouchedRel

	idxDiffRel := findHeader(text[idxRulesTouched:], mDiff)
	if idxDiffRel == -1 {
		return packetSections{}, fmt.Errorf("packet missing '## Diff' section")
	}
	idxDiff := idxRulesTouched + idxDiffRel

	idxNotIncludedRel := findHeader(text[idxDiff:], mNotIncluded)
	if idxNotIncludedRel == -1 {
		return packetSections{}, fmt.Errorf("packet missing '## Not included' section")
	}
	idxNotIncluded := idxDiff + idxNotIncludedRel

	return packetSections{
		thisHead:     text[idxThisHead:idxYourPrior],
		yourPrior:    text[idxYourPrior:idxAllVerdicts],
		allVerdicts:  text[idxAllVerdicts:idxOpenFindings],
		openFindings: text[idxOpenFindings:idxRulesTouched],
		rulesTouched: text[idxRulesTouched:idxDiff],
		diff:         text[idxDiff:idxNotIncluded],
		notIncluded:  text[idxNotIncluded:],
	}, nil
}

func packetRulesSectionCount(section string) (int, error) {
	trimmed := strings.TrimSpace(section)
	if !strings.HasPrefix(trimmed, "## Rules touched") {
		return 0, fmt.Errorf("rules section missing '## Rules touched' header")
	}
	if strings.Contains(trimmed, "No changed files") || strings.Contains(trimmed, "none recorded") {
		return 0, nil
	}
	if total := matchMoreOfTotal(section); total > 0 {
		return total, nil
	}
	seen := make(map[string]struct{})
	for _, l := range strings.Split(section, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "### ") {
			seen[l] = struct{}{}
		}
	}
	return len(seen), nil
}

func parseSectionCount(sec, tableHeader string) int {
	if strings.Contains(sec, "none recorded") {
		return 0
	}
	if t := matchMoreOfTotal(sec); t > 0 {
		return t
	}
	count := 0
	for _, l := range strings.Split(sec, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "|") && !strings.Contains(l, tableHeader) {
			count++
		}
	}
	return count
}

func matchMoreOfTotal(sec string) int {
	for _, l := range strings.Split(sec, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, " more of ") && strings.Contains(l, "; print with:") {
			parts := strings.Split(l, " more of ")
			if len(parts) == 2 {
				sub := strings.Split(parts[1], ";")[0]
				if t, err := strconv.Atoi(strings.TrimSpace(sub)); err == nil && t >= 0 {
					return t
				}
			}
		}
	}
	return 0
}

func notIncludedCounts(sec string) (files, hunks int) {
	for _, l := range strings.Split(sec, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l == "nothing" || strings.HasPrefix(l, "##") {
			continue
		}
		if strings.Contains(l, " hunks,") {
			files++
			parts := strings.Split(l, ": ")
			if len(parts) >= 2 {
				hunkPart := strings.Split(parts[1], " hunks,")[0]
				if n, err := strconv.Atoi(strings.TrimSpace(hunkPart)); err == nil {
					hunks += n
				}
			}
		}
	}
	return
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

func selectedRules(ctx context.Context, repo, base, head, diff string, specFlags, requested []string, maxRules int, lane string) (string, int, error) {
	var specs []scopedSpec
	for _, flag := range specFlags {
		p, heading, err := splitSpecFlag(flag)
		if err != nil {
			return "", 0, err
		}
		text, err := gitOut(ctx, repo, "show", head+":"+p)
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
	currentFile := ""
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Split(strings.TrimPrefix(line, "diff --git a/"), " b/")
			if len(parts) == 2 {
				currentFile = parts[1]
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
			oldText, err := gitOut(ctx, repo, "show", base+":"+spec.Path)
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

func getAuthorIntent(ctx context.Context, repo, hostRepo string, pr int, branch, head string) (title, body string, err error) {
	if pr > 0 && hostRepo != "" {
		c := exec.CommandContext(ctx, "gh", "pr", "view", fmt.Sprint(pr), "--repo", hostRepo, "--json", "title,body")
		stdout, err := c.StdoutPipe()
		if err == nil {
			if err := c.Start(); err == nil {
				const maxRead = 1024 * 1024
				b, err := io.ReadAll(io.LimitReader(stdout, maxRead+1))
				if err == nil && len(b) <= maxRead && c.Wait() == nil {
					var v struct {
						Title string `json:"title"`
						Body  string `json:"body"`
					}
					if err := json.Unmarshal(b, &v); err == nil {
						return strings.TrimSpace(v.Title), strings.TrimSpace(v.Body), nil
					}
				}
			}
		}
	}
	if head != "" && repo != "" {
		out, err := gitOut(ctx, repo, "log", "-1", "--format=%s%n%n%b", head)
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

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '/' || c == '.' || c == '_' || c == '-' || c == ':') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func formatAllVerdicts(reads []merge.Read, max int, lane string, pr int, branch string) (string, int) {
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
		entryFlag := ""
		if pr > 0 {
			entryFlag = fmt.Sprintf("--pr %d", pr)
		} else {
			entryFlag = fmt.Sprintf("--branch %s", shellQuote(branch))
		}
		sb.WriteString(fmt.Sprintf("%d more of %d; print with: nova-review roster --lane %s %s --max 0\n",
			len(sorted)-max, len(sorted), shellQuote(lane), entryFlag))
	}
	return sb.String(), len(reads)
}

type rawReviewRecord struct {
	Version  int          `json:"version"`
	Entry    string       `json:"entry"`
	Who      string       `json:"who"`
	Model    string       `json:"model"`
	Kind     string       `json:"kind"`
	Verdict  string       `json:"verdict"`
	Head     string       `json:"head"`
	At       string       `json:"at"`
	Findings []rawFinding `json:"findings"`
}

type rawFinding struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Side     string `json:"side"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Spec     string `json:"spec"`
	SpecLine int    `json:"spec_line"`
	RuleKind string `json:"rule_kind"`
	Rule     string `json:"rule"`
	Claim    string `json:"claim"`
}

type rawAnswerRecord struct {
	Version int    `json:"version"`
	Entry   string `json:"entry"`
	Who     string `json:"who"`
	Finding string `json:"finding"`
	As      string `json:"as"`
	Of      string `json:"of"`
	Head    string `json:"head"`
	At      string `json:"at"`
	Note    string `json:"note"`
}

func formatOpenFindings(lane, entryID string, max int, pr int, branch string) (string, int, error) {
	var sb strings.Builder
	sb.WriteString("## Open findings (answer with `dup <id>` if you see the same thing)\n")
	entryDir := merge.EntryDirName(entryID)
	reviewsDir := filepath.Join(lane, "reviews", entryDir)
	entries, err := os.ReadDir(reviewsDir)
	if err != nil {
		if os.IsNotExist(err) {
			sb.WriteString("none recorded\n")
			return sb.String(), 0, nil
		}
		return "", 0, fmt.Errorf("could not read reviews directory: %w", err)
	}

	type findingItem struct {
		ID         string
		FileLine   string
		Rule       string
		Severity   string
		Claim      string
		SeenBy     []string
		AuthorSays string
		At         string
		Closed     bool
	}

	findingsMap := make(map[string]*findingItem)
	answers := make(map[string]rawAnswerRecord)
	var verdictFiles []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if strings.HasPrefix(e.Name(), "policy-") {
			continue
		}
		p := filepath.Join(reviewsDir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			return "", 0, fmt.Errorf("could not read review file %s: %w", e.Name(), err)
		}
		if strings.HasPrefix(e.Name(), "answer-") {
			var ans rawAnswerRecord
			if err := json.Unmarshal(b, &ans); err != nil {
				return "", 0, &foldErr{file: p, err: fmt.Errorf("could not decode answer record: %w", err)}
			}
			answers[ans.Finding] = ans
		} else {
			verdictFiles = append(verdictFiles, p)
		}
	}

	var verdicts []rawReviewRecord
	for _, p := range verdictFiles {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", 0, fmt.Errorf("could not read review file %s: %w", p, err)
		}
		var rec rawReviewRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			return "", 0, &foldErr{file: p, err: fmt.Errorf("could not decode review record: %w", err)}
		}
		verdicts = append(verdicts, rec)
	}

	sort.Slice(verdicts, func(i, j int) bool {
		return verdicts[i].At < verdicts[j].At
	})

	for _, rec := range verdicts {
		for _, f := range rec.Findings {
			if strings.EqualFold(f.State, "ok") {
				continue
			}
			if strings.EqualFold(f.State, "close") || strings.EqualFold(f.State, "closed") {
				if item, ok := findingsMap[f.ID]; ok {
					item.Closed = true
				}
				continue
			}
			if strings.EqualFold(f.State, "dup") {
				targetID := f.ID
				if f.Claim != "" && findingsMap[f.Claim] != nil {
					targetID = f.Claim
				}
				if item, ok := findingsMap[targetID]; ok {
					if !containsString(item.SeenBy, rec.Who) {
						item.SeenBy = append(item.SeenBy, rec.Who)
					}
				}
				continue
			}
			fl := f.Path
			if f.Side == "base" {
				fl = "base:" + fl
			}
			fl = fmt.Sprintf("%s:%d", fl, f.Line)

			if existing, ok := findingsMap[f.ID]; ok {
				if !containsString(existing.SeenBy, rec.Who) {
					existing.SeenBy = append(existing.SeenBy, rec.Who)
				}
			} else {
				findingsMap[f.ID] = &findingItem{
					ID:       f.ID,
					FileLine: fl,
					Rule:     f.Rule,
					Severity: strings.ToLower(f.State),
					Claim:    f.Claim,
					SeenBy:   []string{rec.Who},
					At:       rec.At,
				}
			}
		}
	}

	for id, ans := range answers {
		if item, ok := findingsMap[id]; ok {
			item.AuthorSays = ans.As
			if ans.As == "dup" && ans.Of != "" {
				if target, tok := findingsMap[ans.Of]; tok {
					if !containsString(target.SeenBy, ans.Who) {
						target.SeenBy = append(target.SeenBy, ans.Who)
					}
				}
			}
		}
	}

	var open []*findingItem
	for _, item := range findingsMap {
		if !item.Closed {
			open = append(open, item)
		}
	}

	if len(open) == 0 {
		sb.WriteString("none recorded\n")
		return sb.String(), 0, nil
	}

	sort.Slice(open, func(i, j int) bool {
		pri := func(s string) int {
			switch strings.ToLower(s) {
			case "block":
				return 0
			case "fix":
				return 1
			case "nit":
				return 2
			default:
				return 3
			}
		}
		pi, pj := pri(open[i].Severity), pri(open[j].Severity)
		if pi != pj {
			return pi < pj
		}
		if open[i].At != open[j].At {
			return open[i].At > open[j].At
		}
		return open[i].ID < open[j].ID
	})

	sb.WriteString("| id | file:line | rule | severity | claim | seen by | author says |\n")
	limit := len(open)
	if max > 0 && limit > max {
		limit = max
	}
	cleanCell := func(s string) string {
		s = strings.ReplaceAll(s, "|", "\\|")
		s = strings.ReplaceAll(s, "\n", " ")
		return strings.TrimSpace(s)
	}
	for i := 0; i < limit; i++ {
		item := open[i]
		auth := "-"
		if item.AuthorSays != "" {
			auth = cleanCell(item.AuthorSays)
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s |\n",
			cleanCell(item.ID), cleanCell(item.FileLine), cleanCell(item.Rule),
			cleanCell(item.Severity), cleanCell(item.Claim), strings.Join(item.SeenBy, ", "), auth))
	}
	if max > 0 && len(open) > max {
		entryFlag := ""
		if pr > 0 {
			entryFlag = fmt.Sprintf("--pr %d", pr)
		} else {
			entryFlag = fmt.Sprintf("--branch %s", shellQuote(branch))
		}
		sb.WriteString(fmt.Sprintf("%d more of %d; print with: nova-review dedupe --lane %s %s --max 0\n",
			len(open)-max, len(open), shellQuote(lane), entryFlag))
	}
	return sb.String(), len(open), nil
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
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
				cf.Path, cf.Hunks, cf.Added, cf.Deleted, rangeText, shellQuote(cf.Path)))
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
				cf.Path, cf.Hunks, cf.Added, cf.Deleted, rangeText, shellQuote(cf.Path)))
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

func short8(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

func writePacket(dest, body string, out io.Writer, entry, id, head, base, baseSHA, rng string, files, hunks, rules, prior, open, bytesN, cut int, reused bool) int {
	if err := writeExclusive(dest, []byte(body)); err != nil {
		fmt.Fprintf(os.Stderr, "PACKET REFUSED: could not exclusively create --out: %s\n", oneline.Escape(err.Error()))
		return 2
	}
	baseField := merge.Short(base)
	if base != baseSHA {
		baseField += "@" + short8(baseSHA)
	}
	_, err := fmt.Fprintf(out, "PACKET OK entry=%s id=%s head=%s base=%s range=%s files=%d hunks=%d rules=%d prior=%d open=%d bytes=%d cut=%d reused=%t out=%s\n",
		oneline.Field(entry), id, merge.Short(head), baseField, oneline.Field(rng), files, hunks, rules, prior, open, bytesN, cut, reused, oneline.Field(dest))
	if err != nil {
		fmt.Fprintf(os.Stderr, "PACKET REFUSED: could not write receipt: %s\n", oneline.Escape(err.Error()))
		return 2
	}
	return 0
}

func writeExclusive(dest string, body []byte) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("destination file %s already exists", dest)
	}
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	var randBytes [6]byte
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
	if err = os.Link(tmpName, dest); err != nil {
		return err
	}
	cleaned = true
	_ = os.Remove(tmpName)
	return nil
}
