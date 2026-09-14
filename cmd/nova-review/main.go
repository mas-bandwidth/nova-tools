// nova-review packet builds a bounded, exact-revision source-review artifact.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const defaultPacketMaxBytes = 131072
const defaultPacketTimeout = 120 * time.Second
const maxPacketTimeoutSeconds = int64(time.Duration(1<<63-1) / time.Second)
const packetDiagnosticCap = oneline.TailBytes

// fullSHACommandOutputBytes is the fixed wire shape used for both Git and gh
// head resolution: forty lower-case hexadecimal bytes followed by Git/gh's
// one newline.  A resolver never needs a general JSON/document buffer.
const fullSHACommandOutputBytes = 41

var packetGitBinary = "git"
var packetGHBinary = "gh"

const usage = `nova-review: bounded exact-revision review packets (docs/SPEC-REVIEW.md)

usage:
  nova-review packet --lane <dir> (--pr <n>|--branch <name>) --who <name> --out <file> [--head <sha>] [--spec <path>]... [--rule <spec>:<n>]... [--max <n>] [--max-bytes <n>] [--reuse <file>] [--timeout <seconds>]
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
	maxBytes := fs.Int("max-bytes", defaultPacketMaxBytes, "")
	maxFlag := fs.Int("max", 20, "")
	timeoutSeconds := fs.Int("timeout", int(defaultPacketTimeout/time.Second), "")
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
	if *timeoutSeconds < 1 || int64(*timeoutSeconds) > maxPacketTimeoutSeconds {
		return refuse(errOut, fmt.Sprintf("--timeout must be a positive number of seconds no greater than %d", maxPacketTimeoutSeconds))
	}
	timeout := time.Duration(*timeoutSeconds) * time.Second
	if (*pr > 0) == (*branch != "") {
		return refuse(errOut, "give exactly one of --pr or --branch")
	}
	if filepath.IsAbs(*dest) || strings.Contains(filepath.Clean(*dest), "..") {
		return refuse(errOut, "--out must be a relative path without ..")
	}
	if _, err := os.Stat(*dest); err == nil {
		return refuse(errOut, "--out already exists; packets are immutable")
	}
	maxBytesSet := false
	fs.Visit(func(f *flag.Flag) { maxBytesSet = maxBytesSet || f.Name == "max-bytes" })
	if *reuse != "" && (len(specs) != 0 || len(rules) != 0 || maxBytesSet) {
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
		current, err = hostPRHead(timeout, st.Repo, *pr)
	} else {
		current, err = gitCommitSHA(timeout, repo, selector)
	}
	if err != nil {
		return refuse(errOut, fmt.Sprintf("could not resolve the entry head: %v", err))
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
		hdr, content, e := readReusePacket(*reuse)
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse file is not a valid packet: %v", e))
		}
		if hdr.ID != packetID || hdr.Entry != id || hdr.Head != current || hdr.Base != base || hdr.Range != rangeText {
			fmt.Fprintf(errOut, "PACKET REUSE asked=%s found=%s file=%s: that packet was built for another (entry, head, range); build this reader's own\n", packetID, oneline.Field(hdr.ID), oneline.Field(*reuse))
			return 2
		}
		sections, e := splitPacketSections(content)
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse candidate packet has invalid sections: %v", e))
		}
		files, hunks, e := packetDiffSectionCounts(sections.diff, sections.notIncluded)
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse candidate packet has invalid diff sections: %v", e))
		}
		if hdr.Cut != sections.notIncludedCount {
			return refuse(errOut, "--reuse candidate packet's cut field disagrees with Not included")
		}
		ruleCount, e := packetRulesSectionCount(sections.rules)
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse candidate packet has invalid Rules touched section: %v", e))
		}
		priorCount, e := packetTableSectionCount(sections.allVerdicts, "## All verdicts at earlier heads\n", "| who | model | kind | verdict | head | at |")
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse candidate packet has invalid earlier verdicts section: %v", e))
		}
		openCount, e := packetTableSectionCount(sections.openFindings, "## Open findings (answer with `dup <id>` if you see the same thing)\n", "| id | file:line | rule | severity | claim | seen by | author says |")
		if e != nil {
			return refuse(errOut, fmt.Sprintf("--reuse candidate packet has invalid open findings section: %v", e))
		}

		yourPriorSection := formatYourPriorVerdicts(*who, entry.Reads, base, current, rangeText)
		bodyRest := sections.thisHead + yourPriorSection + "\n" + sections.allVerdicts + sections.openFindings + sections.rules + sections.diff + sections.notIncluded
		hdrNew := packetHeader{
			ID: hdr.ID, Entry: hdr.Entry, Head: hdr.Head, Base: hdr.Base, Range: hdr.Range,
			Who: *who, Built: hdr.Built, Cut: hdr.Cut,
		}
		newBody := formatPacket(hdrNew, bodyRest)
		reuseLimit := hdr.Bytes
		if reuseLimit < defaultPacketMaxBytes {
			// v1 packets below the default were necessarily admitted by that
			// default unless a smaller flag was used; the header has no field
			// that can distinguish those historical artifacts.
			reuseLimit = defaultPacketMaxBytes
		}
		if len(newBody) > reuseLimit {
			return refuse(errOut, "--reuse packet exceeds the original packet byte bound")
		}
		return writePacket(*dest, newBody, out, errOut, id, packetID, current, base, rangeText, files, hunks, ruleCount, priorCount, openCount, len(newBody), hdr.Cut, true)
	}
	if _, err := gitCommitSHA(timeout, repo, base); err != nil {
		return refuse(errOut, fmt.Sprintf("the lane does not hold the recorded base commit: %v", err))
	}
	diffRange := base + ".." + current
	if priorRead == nil {
		// A first packet is the change from the merge base to the selected head.
		// Passing a single triple-dot revision to Git makes that selection real;
		// two separate endpoints would instead include unrelated base-only work.
		diffRange = base + "..." + current
	}
	scopedSpecs, err := loadScopedSpecs(timeout, repo, current, specs)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	baseSpecs := loadBaseScopedSpecs(timeout, repo, base, scopedSpecs)
	first, _, err := readPatch(timeout, repo, diffRange, nil, 0, scopedSpecs, baseSpecs)
	if err != nil {
		return refuse(errOut, fmt.Sprintf("could not read the selected diff: %v", err))
	}
	fileDiffs := first.Files
	files := len(fileDiffs)
	hunks := 0
	for _, f := range fileDiffs {
		hunks += f.Hunks
	}
	ruleText, ruleCount, err := selectedRulesFromPatch(timeout, repo, base, current, scopedSpecs, fileDiffs, rules, *maxFlag, *lane)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	intentTitle, intentBody, err := getAuthorIntent(timeout, repo, st.Repo, *pr, *branch, current, *maxBytes)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	thisHeadSec := formatThisHead(intentTitle, intentBody)
	yourPriorSec := formatYourPriorVerdicts(*who, entry.Reads, base, current, rangeText)
	allVerdictsSec, priorCount := formatAllVerdicts(entry.Reads, *maxFlag, *lane)
	openFindingsSec, openCount := formatOpenFindings(*maxFlag, *lane)
	rulesTouchedSec := fmt.Sprintf("## Rules touched\n%s\n", ruleText)

	bodyPrefix := thisHeadSec + "\n" +
		yourPriorSec + "\n" +
		allVerdictsSec + "\n" +
		openFindingsSec + "\n" +
		rulesTouchedSec + "\n"
	hdr := packetHeader{
		ID: packetID, Entry: id, Head: current, Base: base, Range: rangeText,
		Who: *who, Built: time.Now().UTC().Format(time.RFC3339),
	}
	selected, notIncluded, cut, err := planPatch(fileDiffs, rangeText, *maxBytes, hdr, bodyPrefix)
	if err != nil {
		return refuse(errOut, err.Error())
	}
	second, payload, err := readPatch(timeout, repo, diffRange, selected, *maxBytes, nil, nil)
	if err != nil {
		return refuse(errOut, fmt.Sprintf("could not reread the selected diff: %v", err))
	}
	if !samePatch(first, second) {
		return refuse(errOut, "selected diff changed between bounded passes; build again from the pinned commits")
	}
	diffSec := renderPatchDiff(rangeText, payload)
	body := formatPacket(packetHeader{
		ID: packetID, Entry: id, Head: current, Base: base, Range: rangeText,
		Who: *who, Built: hdr.Built, Cut: cut,
	}, bodyPrefix+diffSec+"\n"+notIncluded)
	if len(body) > *maxBytes {
		return refuse(errOut, fmt.Sprintf("--max-bytes %d cannot hold packet metadata and bounded remedy (%d bytes)", *maxBytes, len(body)))
	}
	return writePacket(*dest, body, out, errOut, id, packetID, current, base, rangeText, files, hunks, ruleCount, priorCount, openCount, len(body), cut, false)
}

type diagnosticCapture struct {
	data []byte
	cut  bool
}

func (d *diagnosticCapture) Write(p []byte) (int, error) {
	remaining := packetDiagnosticCap - len(d.data)
	if remaining < 0 {
		remaining = 0
	}
	written := remaining
	if written > len(p) {
		written = len(p)
	}
	d.data = append(d.data, p[:written]...)
	if len(p) > written {
		d.cut = true
	}
	return len(p), nil
}

func (d *diagnosticCapture) String() string {
	text := string(d.data)
	if d.cut {
		text += " [stderr truncated]"
	}
	// The event line holds rendered diagnostic data. Escaping can expand a
	// control byte, so cap the escaped representation as well as its capture.
	return oneline.Cap(oneline.Escape(strings.TrimSpace(text)), packetDiagnosticCap)
}

type boundedSourceOutput struct {
	data     []byte
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedSourceOutput) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.data)
	if remaining < 0 {
		remaining = 0
	}
	kept := remaining
	if kept > len(p) {
		kept = len(p)
	}
	b.data = append(b.data, p[:kept]...)
	if kept < len(p) {
		b.overflow = true
		// Closing the context stops a child which continues writing after the
		// pipe rejects its over-limit stdout.  The overflow is reported below
		// before cancellation can be mistaken for a caller timeout.
		b.cancel()
		return len(p), errors.New("source stdout exceeds fixed-shape bound")
	}
	return len(p), nil
}

func (b *boundedSourceOutput) String() string { return string(b.data) }

func sourceOutputWithLimit(timeout time.Duration, dir, binary string, stdoutLimit int, args ...string) (string, error) {
	if stdoutLimit <= 0 {
		return "", fmt.Errorf("source stdout limit must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	stdout := &boundedSourceOutput{limit: stdoutLimit, cancel: cancel}
	stderr := &diagnosticCapture{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A deadline must still return when a killed command left a child holding
	// its output pipe; this is the lifecycle guard merge.Exec uses as well.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	diagnostic := stderr.String()
	if stdout.overflow {
		return "", fmt.Errorf("%s output exceeds its %d-byte fixed-shape bound", binary, stdoutLimit)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if diagnostic != "" {
			return stdout.String(), fmt.Errorf("%s timed out after %s; raise --timeout <seconds> to wait longer: %s", binary, timeout, diagnostic)
		}
		return stdout.String(), fmt.Errorf("%s timed out after %s; raise --timeout <seconds> to wait longer", binary, timeout)
	}
	if err != nil {
		if diagnostic != "" {
			return stdout.String(), fmt.Errorf("%s failed: %w: %s", binary, err, diagnostic)
		}
		return stdout.String(), fmt.Errorf("%s failed: %w", binary, err)
	}
	return stdout.String(), nil
}

func sourceOutput(timeout time.Duration, dir, binary string, args ...string) (string, error) {
	var stdout bytes.Buffer
	err := sourceOutputTo(timeout, dir, binary, &stdout, args...)
	return stdout.String(), err
}

// sourceOutputTo keeps source stdout and diagnostic stderr separate while a
// caller incrementally consumes stdout.  Unlike the fixed-SHA helper above,
// its writer owns any source-specific admission decision.
func sourceOutputTo(timeout time.Duration, dir, binary string, stdout io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	stderr := &diagnosticCapture{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A deadline must still return when a killed command left a child holding
	// its output pipe; this is the lifecycle guard merge.Exec uses as well.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	diagnostic := stderr.String()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if diagnostic != "" {
			return fmt.Errorf("%s timed out after %s; raise --timeout <seconds> to wait longer: %s", binary, timeout, diagnostic)
		}
		return fmt.Errorf("%s timed out after %s; raise --timeout <seconds> to wait longer", binary, timeout)
	}
	if err != nil {
		if diagnostic != "" {
			return fmt.Errorf("%s failed: %w: %s", binary, err, diagnostic)
		}
		return fmt.Errorf("%s failed: %w", binary, err)
	}
	return nil
}

func gitOut(timeout time.Duration, repo string, args ...string) (string, error) {
	return sourceOutput(timeout, repo, packetGitBinary, args...)
}

func gitCommitSHA(timeout time.Duration, repo, revision string) (string, error) {
	out, err := sourceOutputWithLimit(timeout, repo, packetGitBinary, fullSHACommandOutputBytes,
		"rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSuffix(out, "\n")
	if !merge.IsSHA(sha) {
		return "", fmt.Errorf("git returned no full commit sha")
	}
	return sha, nil
}

func hostPRHead(timeout time.Duration, repo string, pr int) (string, error) {
	b, err := sourceOutputWithLimit(timeout, "", packetGHBinary, fullSHACommandOutputBytes,
		"pr", "view", fmt.Sprint(pr), "--repo", repo, "--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return "", err
	}
	head := strings.TrimSuffix(b, "\n")
	if !merge.IsSHA(head) {
		return "", fmt.Errorf("host returned no full pull request head")
	}
	return head, nil
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
		packetV1Prefix, h.ID, packetHeaderField(h.Entry), h.Head, h.Base, h.Range, packetHeaderField(h.Who), h.Built, h.Bytes, h.Cut)
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

const packetHeaderLimit = 4096

func readPacketFirstLine(path string) (*packetHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// The header is the only part this helper reads.  A missing newline must not
	// turn the first 4 KiB of an arbitrarily long file into a plausible packet.
	content, err := io.ReadAll(io.LimitReader(f, packetHeaderLimit+1))
	if err != nil {
		return nil, err
	}
	firstLine, _, found := bytes.Cut(content, []byte{'\n'})
	if !found || len(firstLine) > packetHeaderLimit {
		return nil, fmt.Errorf("packet header exceeds %d bytes or is unterminated", packetHeaderLimit)
	}
	return parsePacketFirstLine(strings.TrimSuffix(string(firstLine), "\r"))
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if (s[i] < '0' || s[i] > '9') && (s[i] < 'a' || s[i] > 'f') {
			return false
		}
	}
	return true
}

func packetHeaderCount(field, value string) (int, error) {
	n, err := strconv.ParseInt(value, 10, strconv.IntSize)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != value {
		return 0, fmt.Errorf("invalid %s field %q", field, value)
	}
	return int(n), nil
}

// packetHeaderField is a canonical, reversible token encoding.  oneline.Field
// deliberately is not reversible, so it cannot carry tuple members that must
// be compared byte-for-byte during reuse.
func packetHeaderField(value string) string { return url.QueryEscape(value) }

func packetHeaderToken(field, value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return "", fmt.Errorf("invalid %s field %q", field, value)
	}
	decoded, err := url.QueryUnescape(value)
	if err != nil || decoded == "" || packetHeaderField(decoded) != value {
		return "", fmt.Errorf("invalid %s field %q", field, value)
	}
	return decoded, nil
}

func packetHeaderRange(value, base, head string) error {
	sep := "..."
	if !strings.Contains(value, sep) {
		sep = ".."
	}
	left, right, ok := strings.Cut(value, sep)
	if !ok || strings.Contains(right, ".") || !isLowerHex(left, 12) || !isLowerHex(right, 12) {
		return fmt.Errorf("invalid range field %q", value)
	}
	if left != merge.Short(base) || right != merge.Short(head) {
		return fmt.Errorf("range field %q does not name base and head", value)
	}
	return nil
}

func parsePacketFirstLine(line string) (*packetHeader, error) {
	if !strings.HasPrefix(line, packetV1Prefix+" ") {
		return nil, fmt.Errorf("line does not begin with %q", packetV1Prefix+" ")
	}
	fields := strings.Fields(strings.TrimPrefix(line, packetV1Prefix+" "))
	hdr := &packetHeader{}
	seen := map[string]bool{}
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("malformed header field %q", f)
		}
		if seen[k] {
			return nil, fmt.Errorf("duplicate header field %q", k)
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
			n, err := packetHeaderCount(k, v)
			if err != nil {
				return nil, err
			}
			hdr.Bytes = n
		case "cut":
			n, err := packetHeaderCount(k, v)
			if err != nil {
				return nil, err
			}
			hdr.Cut = n
		default:
			return nil, fmt.Errorf("unknown header field %q", k)
		}
	}
	required := []string{"id", "entry", "head", "base", "range", "who", "built", "bytes", "cut"}
	for _, r := range required {
		if !seen[r] {
			return nil, fmt.Errorf("missing required header field %q", r)
		}
	}
	if !isLowerHex(hdr.ID, 12) {
		return nil, fmt.Errorf("invalid id field %q", hdr.ID)
	}
	entry, err := packetHeaderToken("entry", hdr.Entry)
	if err != nil {
		return nil, err
	}
	hdr.Entry = entry
	if !merge.IsSHA(hdr.Head) || !merge.IsSHA(hdr.Base) {
		return nil, fmt.Errorf("head and base must be full lower-case shas")
	}
	if err := packetHeaderRange(hdr.Range, hdr.Base, hdr.Head); err != nil {
		return nil, err
	}
	who, err := packetHeaderToken("who", hdr.Who)
	if err != nil {
		return nil, err
	}
	hdr.Who = who
	built, err := time.Parse(time.RFC3339, hdr.Built)
	if err != nil || built.UTC().Format(time.RFC3339) != hdr.Built {
		return nil, fmt.Errorf("invalid built field %q", hdr.Built)
	}
	if hdr.ID != packetID(hdr.Entry, hdr.Head, hdr.Base, hdr.Range) {
		return nil, fmt.Errorf("id field does not match packet tuple")
	}
	return hdr, nil
}

type packetSections struct {
	thisHead, allVerdicts, openFindings, rules, diff, notIncluded string
	notIncludedCount                                              int
}

// readReusePacket opens the supplied artifact once.  The opened descriptor is
// checked as a regular file and read with the packet's fixed admission ceiling;
// its declared byte count then binds the header to exactly the bytes reused.
func readReusePacket(path string) (*packetHeader, string, error) {
	// Lstat rejects named pipes and devices before opening them. O_NONBLOCK also
	// keeps a path swapped to a FIFO between that check and open from hanging.
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, "", fmt.Errorf("reuse candidate is not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("reuse candidate is not a regular file")
	}
	// Keep the one descriptor open while the framed header fixes the exact
	// byte budget for the remainder. A caller cannot turn a small header read
	// into an unbounded second read by swapping the pathname.
	r := bufio.NewReaderSize(f, packetHeaderLimit+1)
	firstLine, err := r.ReadSlice('\n')
	if err != nil || len(firstLine) > packetHeaderLimit+1 {
		return nil, "", fmt.Errorf("packet header exceeds %d bytes or is unterminated", packetHeaderLimit)
	}
	hdr, err := parsePacketFirstLine(strings.TrimSuffix(strings.TrimSuffix(string(firstLine), "\n"), "\r"))
	if err != nil {
		return nil, "", err
	}
	if int64(hdr.Bytes) != info.Size() {
		return nil, "", fmt.Errorf("header bytes=%d does not match regular-file size %d", hdr.Bytes, info.Size())
	}
	if hdr.Bytes < len(firstLine) {
		return nil, "", fmt.Errorf("header bytes=%d is shorter than its header", hdr.Bytes)
	}
	data := make([]byte, 0, hdr.Bytes)
	data = append(data, firstLine...)
	rest, err := io.ReadAll(io.LimitReader(r, int64(hdr.Bytes-len(firstLine))+1))
	if err != nil {
		return nil, "", err
	}
	data = append(data, rest...)
	if len(data) != hdr.Bytes {
		return nil, "", fmt.Errorf("header bytes=%d does not match %d bytes read", hdr.Bytes, len(data))
	}
	return hdr, string(data), nil
}

var packetSectionMarkers = []string{
	"## This head\n",
	"## Your prior verdicts on this entry\n",
	"## All verdicts at earlier heads\n",
	"## Open findings (answer with `dup <id>` if you see the same thing)\n",
	"## Rules touched\n",
}

func splitPacketSections(content string) (packetSections, error) {
	var sections packetSections
	firstEnd := strings.IndexByte(content, '\n')
	if firstEnd < 0 || !strings.HasPrefix(content[firstEnd:], "\n\n## This head\n") {
		return sections, fmt.Errorf("packet must begin with This head after the header")
	}
	thisHead := firstEnd + 2
	// New packets quote author data, so it cannot form a top-level delimiter.
	// For already-published legacy packets, locate the later fixed sections from
	// the end: a literal heading in author prose then remains data, rather than
	// stealing the reader-specific boundary.
	notIncluded := strings.LastIndex(content, "## Not included\n")
	if notIncluded < 0 {
		return sections, fmt.Errorf("missing required Not included section")
	}
	diff := strings.LastIndex(content[:notIncluded], "## Diff ")
	rules := strings.LastIndex(content[:diff], "## Rules touched\n")
	open := strings.LastIndex(content[:rules], "## Open findings (answer with `dup <id>` if you see the same thing)\n")
	all := strings.LastIndex(content[:open], "## All verdicts at earlier heads\n")
	your := strings.LastIndex(content[:all], "## Your prior verdicts on this entry\n")
	if diff < 0 || rules < 0 || open < 0 || all < 0 || your < 0 || !(thisHead < your && your < all && all < open && open < rules && rules < diff && diff < notIncluded) {
		return sections, fmt.Errorf("missing or unordered required packet sections")
	}
	sections.thisHead = content[thisHead:your]
	sections.allVerdicts = content[all:open]
	sections.openFindings = content[open:rules]
	sections.rules = content[rules:diff]
	sections.diff = content[diff:notIncluded]
	sections.notIncluded = content[notIncluded:]
	count, err := packetNotIncludedCount(sections.notIncluded)
	if err != nil {
		return sections, err
	}
	sections.notIncludedCount = count
	return sections, nil
}

func packetNotIncludedCount(section string) (int, error) {
	lines := strings.Split(strings.TrimRight(strings.TrimPrefix(section, "## Not included\n"), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "nothing" {
		return 0, nil
	}
	if len(lines) == 0 || lines[0] == "" {
		return 0, fmt.Errorf("Not included is empty")
	}
	for _, line := range lines {
		pathEnd := strings.LastIndex(line, ": ")
		if pathEnd <= 0 {
			return 0, fmt.Errorf("invalid Not included row %q", line)
		}
		var hunks, added, deleted int
		if n, err := fmt.Sscanf(line[pathEnd+2:], "%d hunks, +%d -%d; print with: git diff", &hunks, &added, &deleted); err != nil || n != 3 || hunks < 0 || added < 0 || deleted < 0 {
			return 0, fmt.Errorf("invalid Not included row %q", line)
		}
	}
	return len(lines), nil
}

func packetDiffSectionCounts(diffSection, notIncluded string) (int, int, error) {
	if !strings.HasPrefix(diffSection, "## Diff ") {
		return 0, 0, fmt.Errorf("missing Diff heading")
	}
	fence := strings.Index(diffSection, "\n```diff\n")
	endFence := strings.LastIndex(diffSection, "\n```\n")
	if fence < 0 || endFence < fence || diffSection[endFence+len("\n```\n"):] != "\n" {
		return 0, 0, fmt.Errorf("Diff has no complete diff fence")
	}
	diffText := diffSection[fence+len("\n```diff\n") : endFence]
	files := parseFileDiffs(diffText)
	fileCount, hunkCount := len(files), 0
	for _, file := range files {
		hunkCount += file.Hunks
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(notIncluded, "## Not included\n"), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "nothing" {
		return fileCount, hunkCount, nil
	}
	for _, line := range lines {
		pathEnd := strings.LastIndex(line, ": ")
		var hunks, added, deleted int
		if pathEnd <= 0 {
			return 0, 0, fmt.Errorf("invalid Not included row %q", line)
		}
		if n, err := fmt.Sscanf(line[pathEnd+2:], "%d hunks, +%d -%d; print with: git diff", &hunks, &added, &deleted); err != nil || n != 3 {
			return 0, 0, fmt.Errorf("invalid Not included row %q", line)
		}
		fileCount++
		hunkCount += hunks
	}
	return fileCount, hunkCount, nil
}

func packetRulesSectionCount(section string) (int, error) {
	if !strings.HasPrefix(section, "## Rules touched\n") {
		return 0, fmt.Errorf("missing Rules touched heading")
	}
	body := strings.TrimRight(strings.TrimPrefix(section, "## Rules touched\n"), "\n")
	if body == "No changed files." {
		return 0, nil
	}
	shown, rows, total := 0, 0, -1
	for _, line := range strings.Split(body, "\n") {
		if i := strings.LastIndex(line, ": rules="); i > 0 {
			if _, err := packetHeaderCount("rules", line[i+len(": rules="):]); err != nil {
				return 0, err
			}
			rows++
			continue
		}
		if strings.HasPrefix(line, "### ") {
			shown++
			continue
		}
		var omitted, all int
		if n, err := fmt.Sscanf(line, "%d more of %d;", &omitted, &all); err == nil && n == 2 && omitted >= 0 && all >= shown && omitted+shown == all && total == -1 {
			total = all
		}
	}
	if rows == 0 {
		return 0, fmt.Errorf("Rules touched has no per-file counts")
	}
	if total >= 0 {
		return total, nil
	}
	return shown, nil
}

func packetTableSectionCount(section, marker, tableHeader string) (int, error) {
	if !strings.HasPrefix(section, marker) {
		return 0, fmt.Errorf("missing section heading")
	}
	lines := strings.Split(strings.TrimRight(strings.TrimPrefix(section, marker), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "none recorded" {
		return 0, nil
	}
	if len(lines) < 2 || lines[0] != tableHeader {
		return 0, fmt.Errorf("section has no recognized table")
	}
	shown := 0
	moreTotal := -1
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "| ") {
			shown++
			continue
		}
		var omitted, total int
		if n, err := fmt.Sscanf(line, "%d more of %d;", &omitted, &total); err == nil && n == 2 && omitted >= 0 && total >= shown && omitted+shown == total && moreTotal == -1 {
			moreTotal = total
			continue
		}
		return 0, fmt.Errorf("unrecognized table row %q", line)
	}
	if moreTotal >= 0 {
		return moreTotal, nil
	}
	return shown, nil
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
func selectedRules(timeout time.Duration, repo, base, head, diff string, specFlags, requested []string, maxRules int, lane string) (string, int, error) {
	specs, err := loadScopedSpecs(timeout, repo, head, specFlags)
	if err != nil {
		return "", 0, err
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
			oldText, err := gitOut(timeout, repo, "show", base+":"+spec.Path)
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

func loadScopedSpecs(timeout time.Duration, repo, head string, flags []string) ([]scopedSpec, error) {
	var specs []scopedSpec
	for _, flag := range flags {
		p, heading, err := splitSpecFlag(flag)
		if err != nil {
			return nil, err
		}
		text, err := gitOut(timeout, repo, "show", head+":"+p)
		if err != nil {
			return nil, fmt.Errorf("--spec %s is not readable at head", p)
		}
		spec, err := parseScopedSpec(p, text, heading)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// loadBaseScopedSpecs follows selectedRules' established behavior: an absent
// or unparsable base-side spec simply has no removable rule witness.
func loadBaseScopedSpecs(timeout time.Duration, repo, base string, specs []scopedSpec) map[string]scopedSpec {
	out := make(map[string]scopedSpec)
	for _, spec := range specs {
		text, err := gitOut(timeout, repo, "show", base+":"+spec.Path)
		if err != nil {
			continue
		}
		old, err := parseScopedSpec(spec.Path, text, spec.Heading)
		if err == nil {
			out[spec.Path] = old
		}
	}
	return out
}

// selectedRulesFromPatch renders the same source-derived rule section as
// selectedRules, but takes line coordinates and citation witnesses produced by
// the streaming first pass. It therefore accounts for a rule citation even
// when that file's payload is later omitted from the packet body.
func selectedRulesFromPatch(timeout time.Duration, repo, base, head string, specs []scopedSpec, patchFiles []fileDiff, requested []string, maxRules int, lane string) (string, int, error) {
	selected := map[string]specRule{}
	previous := map[string]specRule{}
	touched := map[string][]string{}
	add := func(rule specRule, why string) {
		key := fmt.Sprintf("%s:%d", rule.Path, rule.Line)
		selected[key] = rule
		touched[key] = append(touched[key], why)
	}
	for _, file := range patchFiles {
		for _, rule := range file.Cited {
			add(rule, fmt.Sprintf("%s (cited)", file.Path))
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
	for _, file := range patchFiles {
		for _, rule := range file.ChangedHead {
			add(rule, fmt.Sprintf("%s (changed at head)", file.Path))
		}
		for _, changed := range file.ChangedBase {
			if changed.Current != nil {
				current := *changed.Current
				previous[fmt.Sprintf("%s:%d", current.Path, current.Line)] = changed.Old
				add(current, fmt.Sprintf("%s (changed at base)", file.Path))
			} else {
				add(changed.Old, fmt.Sprintf("%s (changed at base; removed at head)", file.Path))
			}
		}
	}
	var out []string
	for _, file := range patchFiles {
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
	if len(patchFiles) == 0 {
		out = append(out, "No changed files.")
	}
	return strings.Join(out, "\n"), len(selected), nil
}

func getAuthorIntent(timeout time.Duration, repo, hostRepo string, pr int, branch, head string, maxBytes int) (title, body string, err error) {
	if pr > 0 && hostRepo != "" {
		if result, complete := streamGHAuthorIntent(timeout, hostRepo, pr, maxBytes); complete {
			return result.title, result.body, result.err
		}
	}
	if head != "" && repo != "" {
		if result, complete := streamGitAuthorIntent(timeout, repo, head, maxBytes); complete {
			return result.title, result.body, result.err
		}
	}
	return "", "unknown: the lane state has no entry body; no intent is guessed.", nil
}

func quotedPacketData(text string) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		out.WriteString("> ")
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func formatThisHead(title, body string) string {
	var sb strings.Builder
	sb.WriteString("## This head\n")
	if title != "" {
		sb.WriteString(quotedPacketData(title))
		sb.WriteByte('\n')
	}
	sb.WriteString("the author says:\n")
	if body != "" {
		sb.WriteString(quotedPacketData(body))
	} else {
		sb.WriteString("> (no description provided)\n")
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
	Header      string
	Path        string
	Hunks       int
	Added       int
	Deleted     int
	Cited       []specRule
	ChangedHead []specRule
	ChangedBase []patchBaseChange
	seenCited   map[string]bool
	seenHead    map[string]bool
	seenBase    map[string]bool
	// Bytes is the exact raw patch span for this file, including its header.
	// FullText remains for legacy unit helpers; the packet path uses Bytes and
	// streams selected payload in a second pinned Git invocation.
	Bytes       int64
	EndsNewline bool
	FullText    string
}

func parseFileDiffs(diff string) []fileDiff {
	var files []fileDiff
	lines := strings.Split(diff, "\n")
	var cur *fileDiff
	var curLines []string
	inHunk := false

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
			inHunk = false
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
			inHunk = true
			cur.Hunks++
		} else if strings.HasPrefix(line, "+") && (inHunk || !strings.HasPrefix(line, "+++")) {
			cur.Added++
		} else if strings.HasPrefix(line, "-") && (inHunk || !strings.HasPrefix(line, "---")) {
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

func writePacket(dest, body string, out, errOut io.Writer, entry, id, head, base, rng string, files, hunks, rules, prior, open, bytesN, cut int, reused bool) int {
	if err := writeExclusive(dest, []byte(body)); err != nil {
		fmt.Fprintf(errOut, "PACKET REFUSED: could not exclusively create --out: %s\n", oneline.Escape(err.Error()))
		return 2
	}
	if _, err := fmt.Fprintf(out, "PACKET OK entry=%s id=%s head=%s base=%s range=%s files=%d hunks=%d rules=%d prior=%d open=%d bytes=%d cut=%d reused=%t out=%s\n",
		oneline.Field(entry), id, merge.Short(head), merge.Short(base), oneline.Field(rng), files, hunks, rules, prior, open, bytesN, cut, reused, oneline.Field(dest)); err != nil {
		fmt.Fprintf(errOut, "PACKET FAIL output: artifact published at %s: %s\n", oneline.Field(dest), oneline.Escape(err.Error()))
		return 1
	}
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
	// Rename replaces dest on POSIX, so its apparent atomicity is the wrong guarantee for
	// an immutable packet: a second publisher can erase the first after the preflight Stat.
	// A same-directory hard link creates the final name only when it does not already name
	// anything. The temporary and destination share a filesystem by construction; the one
	// caller whose Link wins removes its private name, while every loser leaves the existing
	// packet byte-for-byte alone (including a destination symlink).
	if err = os.Link(tmpName, dest); err != nil {
		return err
	}
	if err = os.Remove(tmpName); err != nil {
		return err
	}
	cleaned = true
	return nil
}
