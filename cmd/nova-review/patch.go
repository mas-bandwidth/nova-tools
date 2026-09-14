package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// patchControlPrefix is deliberately only for control records (the diff header
// and hunk coordinates). Payload lines are never retained by the first pass.
// A Git path is bounded by the host path limit; a longer control record cannot
// be named faithfully in a one-line packet remedy, so refuse rather than guess.
const patchControlPrefix = 64 * 1024
const patchNameTokenCap = 64 * 1024

type patchBaseChange struct {
	Old     specRule
	Current *specRule
}

// patchInventory is the source-derived fact set needed to plan a packet. Its
// file metadata is proportional to files and hunks, while raw patch payload is
// neither retained nor copied on this pass.
type patchInventory struct {
	Files []fileDiff
	Hash  [sha256.Size]byte
}

type patchName struct {
	Old, New   string
	TypeChange bool
}

type patchNameReader struct {
	token  []byte
	status string
	old    string
	names  []patchName
}

func (r *patchNameReader) Write(data []byte) (int, error) {
	original := len(data)
	for len(data) != 0 {
		i := bytes.IndexByte(data, 0)
		if i < 0 {
			if len(data) > patchNameTokenCap-len(r.token) {
				return 0, fmt.Errorf("git name-status path exceeds %d bytes", patchNameTokenCap)
			}
			r.token = append(r.token, data...)
			break
		}
		if i > patchNameTokenCap-len(r.token) {
			return 0, fmt.Errorf("git name-status path exceeds %d bytes", patchNameTokenCap)
		}
		r.token = append(r.token, data[:i]...)
		if err := r.field(string(r.token)); err != nil {
			return 0, err
		}
		r.token = r.token[:0]
		data = data[i+1:]
	}
	return original, nil
}

func (r *patchNameReader) field(value string) error {
	if r.status == "" {
		if value == "" {
			return fmt.Errorf("git name-status emitted an empty status")
		}
		r.status = value
		return nil
	}
	if r.status[0] == 'R' || r.status[0] == 'C' {
		if r.old == "" {
			r.old = value
			return nil
		}
		r.names = append(r.names, patchName{Old: r.old, New: value})
		r.status, r.old = "", ""
		return nil
	}
	if len(r.status) != 1 || !strings.ContainsRune("MADTUB", rune(r.status[0])) {
		return fmt.Errorf("unsupported git name-status code %q", r.status)
	}
	r.names = append(r.names, patchName{Old: value, New: value, TypeChange: r.status == "T"})
	r.status = ""
	return nil
}

func (r *patchNameReader) finish() error {
	if len(r.token) != 0 || r.status != "" || r.old != "" {
		return fmt.Errorf("git name-status ended mid-record")
	}
	return nil
}

type patchReader struct {
	hash               hash.Hash
	files              []fileDiff
	current            int
	oldLine            int
	newLine            int
	inHunk             bool
	prefix             []byte
	raw                []byte
	lineBytes          int64
	truncated          bool
	lineEnded          bool
	citationIncomplete bool
	collect            []bool
	names              []patchName
	nameIndex          int
	pendingType        bool
	specs              []scopedSpec
	baseSpecs          map[string]scopedSpec
	payload            bytes.Buffer
	payloadMax         int
	selected           bool
}

func newPatchReader(selection []bool, payloadMax int, specs []scopedSpec, baseSpecs map[string]scopedSpec, names []patchName) *patchReader {
	return &patchReader{hash: sha256.New(), current: -1, collect: selection, payloadMax: payloadMax, specs: specs, baseSpecs: baseSpecs, names: names}
}

// Write is an exec stdout sink. os/exec feeds it bounded chunks; it keeps only
// a control-line prefix, and on the second pass only payload which the packet
// planner has already shown can fit in the output budget.
func (p *patchReader) Write(data []byte) (int, error) {
	original := len(data)
	if _, err := p.hash.Write(data); err != nil {
		return 0, err
	}
	for len(data) != 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if err := p.part(data, false); err != nil {
				return 0, err
			}
			break
		}
		if err := p.part(data[:i+1], true); err != nil {
			return 0, err
		}
		data = data[i+1:]
	}
	return original, nil
}

func (p *patchReader) part(data []byte, end bool) error {
	if p.collect != nil && p.selected {
		if len(data) > p.payloadMax-p.payload.Len()-len(p.raw) {
			return fmt.Errorf("selected diff exceeds packet byte budget")
		}
		p.raw = append(p.raw, data...)
	}
	p.lineBytes += int64(len(data))
	line := data
	if end {
		line = line[:len(line)-1]
	}
	if len(p.prefix) < patchControlPrefix {
		n := patchControlPrefix - len(p.prefix)
		if n > len(line) {
			n = len(line)
		}
		p.prefix = append(p.prefix, line[:n]...)
	}
	if int64(len(p.prefix)) < p.lineBytes-int64(boolInt(end)) {
		p.truncated = true
	}
	if !end {
		return nil
	}
	p.lineEnded = true
	if err := p.finishLine(); err != nil {
		return err
	}
	p.prefix = p.prefix[:0]
	p.raw = p.raw[:0]
	p.lineBytes = 0
	p.truncated = false
	p.lineEnded = false
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (p *patchReader) finish() error {
	if p.lineBytes != 0 {
		if err := p.finishLine(); err != nil {
			return err
		}
	}
	if p.nameIndex != len(p.names) || p.pendingType {
		return fmt.Errorf("git diff headers do not complete name-status metadata")
	}
	if p.citationIncomplete {
		return fmt.Errorf("a changed source line exceeds %d bytes; cannot fully inspect scoped rule citations", patchControlPrefix)
	}
	return nil
}

func (p *patchReader) finishLine() error {
	line := p.prefix
	if bytes.HasPrefix(line, []byte("diff --git ")) {
		if p.truncated {
			return fmt.Errorf("git diff header exceeds %d bytes", patchControlPrefix)
		}
		if p.nameIndex >= len(p.names) {
			return fmt.Errorf("git diff has more file headers than its name-status metadata")
		}
		expected := p.names[p.nameIndex]
		_, path, err := parseDiffHeader(string(line), expected)
		if err != nil {
			return err
		}
		if p.pendingType {
			// Git represents one T name-status row as the regular-file and
			// symlink patch headers. They are one whole-file budget group.
			if p.current < 0 || p.files[p.current].Path != path {
				return fmt.Errorf("type-change patch headers do not form one path group")
			}
			f := &p.files[p.current]
			f.Bytes += p.lineBytes
			f.EndsNewline = p.lineEnded
			p.pendingType = false
			p.nameIndex++
		} else {
			p.files = append(p.files, fileDiff{Header: string(line), Path: path, Bytes: p.lineBytes, EndsNewline: p.lineEnded})
			p.current = len(p.files) - 1
			if expected.TypeChange {
				p.pendingType = true
			} else {
				p.nameIndex++
			}
		}
		p.oldLine, p.newLine, p.inHunk = 0, 0, false
		p.selected = p.current < len(p.collect) && p.collect[p.current]
		if p.collect != nil && p.selected {
			if len(p.raw) != 0 {
				return p.appendPayload(p.raw)
			}
			raw := append([]byte(nil), line...)
			if p.lineEnded {
				raw = append(raw, '\n')
			}
			return p.appendPayload(raw)
		}
		return nil
	}
	if p.current < 0 {
		// git diff --no-ext-diff normally has no preamble. Retaining it would
		// make the packet's per-file accounting dishonest, so fail closed.
		if len(line) != 0 {
			return fmt.Errorf("git diff emitted data before its first file header")
		}
		return nil
	}
	f := &p.files[p.current]
	f.Bytes += p.lineBytes
	f.EndsNewline = p.lineEnded
	if bytes.HasPrefix(line, []byte("@@ ")) {
		old, _, next, _, ok := hunkCoordinates(line)
		if ok {
			p.oldLine, p.newLine = old, next
			p.inHunk = true
			f.Hunks++
		}
	} else if len(line) != 0 {
		if p.inHunk && line[0] == '\\' {
			// Git's marker is not a source line on either side.
			goto payload
		}
		switch line[0] {
		case '+':
			if p.inHunk || !bytes.HasPrefix(line, []byte("+++")) {
				f.Added++
				// Citation extraction sees all ordinary added lines in the first
				// pass, including those in files that later become omissions.
				// Never silently treat a bounded-prefix overflow as citation-free.
				if len(p.specs) != 0 && p.truncated {
					p.citationIncomplete = true
				} else if len(p.specs) != 0 {
					for _, rule := range citationTargets(string(line[1:]), p.specs) {
						appendDistinctRule(&f.Cited, &f.seenCited, rule)
					}
				}
				if p.newLine > 0 {
					for _, spec := range p.specs {
						if spec.Path != f.Path {
							continue
						}
						for _, rule := range spec.Rules {
							if p.newLine >= rule.Line && p.newLine < rule.End {
								appendDistinctRule(&f.ChangedHead, &f.seenHead, rule)
							}
						}
					}
					p.newLine++
				}
			}
		case '-':
			if p.inHunk || !bytes.HasPrefix(line, []byte("---")) {
				f.Deleted++
				if p.oldLine > 0 {
					if old, ok := p.baseSpecs[f.Path]; ok {
						for n, rule := range old.Rules {
							if p.oldLine < rule.Line || p.oldLine >= rule.End {
								continue
							}
							var current *specRule
							for _, spec := range p.specs {
								if spec.Path != f.Path {
									continue
								}
								if r, exists := spec.Rules[n]; exists {
									copy := r
									current = &copy
								}
							}
							appendDistinctBase(&f.ChangedBase, &f.seenBase, patchBaseChange{Old: rule, Current: current})
						}
					}
					p.oldLine++
				}
			}
		default:
			if p.oldLine > 0 {
				p.oldLine++
			}
			if p.newLine > 0 {
				p.newLine++
			}
		}
	}
payload:
	if p.collect != nil && p.selected {
		return p.appendPayload(p.raw)
	}
	return nil
}

func (p *patchReader) appendPayload(raw []byte) error {
	if len(raw) > p.payloadMax-p.payload.Len() {
		return fmt.Errorf("selected diff exceeds packet byte budget")
	}
	p.payload.Write(raw)
	return nil
}

func patchRuleKey(rule specRule) string { return fmt.Sprintf("%s:%d", rule.Path, rule.Line) }

func appendDistinctRule(dst *[]specRule, seen *map[string]bool, rule specRule) {
	if *seen == nil {
		*seen = make(map[string]bool)
	}
	key := patchRuleKey(rule)
	if !(*seen)[key] {
		(*seen)[key] = true
		*dst = append(*dst, rule)
	}
}

func appendDistinctBase(dst *[]patchBaseChange, seen *map[string]bool, change patchBaseChange) {
	if *seen == nil {
		*seen = make(map[string]bool)
	}
	key := patchRuleKey(change.Old)
	if change.Current != nil {
		key += "->" + patchRuleKey(*change.Current)
	}
	if !(*seen)[key] {
		(*seen)[key] = true
		*dst = append(*dst, change)
	}
}

func hunkCoordinates(line []byte) (int, int, int, int, bool) {
	// Parse the fixed coordinate prefix without buffering the optional hunk
	// description, which can itself be arbitrarily long.
	i := len("@@ -")
	old, n := decimalPrefix(line[i:])
	if n == 0 {
		return 0, 0, 0, 0, false
	}
	i += n
	oldCount := 1
	if i < len(line) && line[i] == ',' {
		i++
		oldCount, n = decimalPrefix(line[i:])
		if n == 0 {
			return 0, 0, 0, 0, false
		}
		i += n
	}
	if i+2 > len(line) || line[i] != ' ' || line[i+1] != '+' {
		return 0, 0, 0, 0, false
	}
	i += 2
	newLine, n := decimalPrefix(line[i:])
	if n == 0 {
		return 0, 0, 0, 0, false
	}
	i += n
	newCount := 1
	if i < len(line) && line[i] == ',' {
		i++
		newCount, n = decimalPrefix(line[i:])
		if n == 0 {
			return 0, 0, 0, 0, false
		}
	}
	return old, oldCount, newLine, newCount, true
}

func decimalPrefix(b []byte) (int, int) {
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, 0
	}
	n, err := strconv.Atoi(string(b[:i]))
	if err != nil || n < 0 {
		return 0, 0
	}
	return n, i
}

// parseDiffHeader accepts Git's two path tokens exactly: unquoted names have
// no whitespace, quoted names use Git's C escapes. Splitting on " b/" would
// misidentify both spaces and a literal b/ in a legal pathname.
func parseDiffHeader(line string, expected patchName) (string, string, error) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if rest == line {
		return "", "", fmt.Errorf("invalid git diff header %q", line)
	}
	// Git leaves spaces alone under core.quotePath. Such a header is
	// ambiguous in isolation: a literal " b/" may occur in either path.
	// The same -z name-status invocation supplies the ordered pair, so compare
	// the raw unquoted spelling rather than guessing a separator.
	if line == "diff --git a/"+expected.Old+" b/"+expected.New {
		return expected.Old, expected.New, nil
	}
	old, rest, err := gitPathToken(rest)
	if err != nil {
		return "", "", fmt.Errorf("invalid git diff header: %w", err)
	}
	if !strings.HasPrefix(rest, " ") {
		return "", "", fmt.Errorf("invalid git diff header: missing second path")
	}
	newPath, rest, err := gitPathToken(rest[1:])
	if err != nil || rest != "" || !strings.HasPrefix(old, "a/") || !strings.HasPrefix(newPath, "b/") {
		return "", "", fmt.Errorf("invalid git diff header %q", line)
	}
	if old[2:] != expected.Old || newPath[2:] != expected.New {
		return "", "", fmt.Errorf("git diff header paths disagree with name-status metadata")
	}
	return old[2:], newPath[2:], nil
}

func gitPathToken(s string) (string, string, error) {
	if s == "" {
		return "", "", fmt.Errorf("empty path token")
	}
	if s[0] != '"' {
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			return s, "", nil
		}
		return s[:i], s[i:], nil
	}
	var out []byte
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			return string(out), s[i+1:], nil
		}
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i == len(s) {
			return "", "", fmt.Errorf("unterminated escape")
		}
		switch c = s[i]; c {
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '\\', '"':
			out = append(out, c)
		default:
			if c < '0' || c > '7' || i+2 >= len(s) || s[i+1] < '0' || s[i+1] > '7' || s[i+2] < '0' || s[i+2] > '7' {
				return "", "", fmt.Errorf("invalid escape")
			}
			out = append(out, (c-'0')*64+(s[i+1]-'0')*8+(s[i+2]-'0'))
			i += 2
		}
	}
	return "", "", fmt.Errorf("unterminated quoted path")
}

func patchGitArgs(diffRange string) []string {
	// Do not inherit configured quoting, relative-path, textconv, external-diff,
	// or rename behavior: metadata and both raw-patch passes must mean exactly
	// the same source bytes on every host.
	return []string{"-c", "core.quotePath=true", "diff", "--no-ext-diff", "--no-textconv", "--no-relative", "--find-renames=50%", "--src-prefix=a/", "--dst-prefix=b/", "--unified=3", diffRange}
}

func readPatchNames(timeout time.Duration, repo, diffRange string) ([]patchName, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r := &patchNameReader{}
	args := patchGitArgs(diffRange)
	args = append(args[:3], append([]string{"--name-status", "-z"}, args[3:]...)...)
	cmd := exec.CommandContext(ctx, packetGitBinary, args...)
	cmd.Dir = repo
	stderr := &diagnosticCapture{}
	cmd.Stdout, cmd.Stderr = r, stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if finishErr := r.finish(); finishErr != nil && err == nil {
		err = finishErr
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, sourceCommandError(packetGitBinary, timeout, "timed out", stderr.String())
	}
	if err != nil {
		return nil, sourceCommandError(packetGitBinary, timeout, err.Error(), stderr.String())
	}
	return r.names, nil
}

func readPatch(timeout time.Duration, repo, diffRange string, selection []bool, payloadMax int, specs []scopedSpec, baseSpecs map[string]scopedSpec) (patchInventory, []byte, error) {
	names, err := readPatchNames(timeout, repo, diffRange)
	if err != nil {
		return patchInventory{}, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	p := newPatchReader(selection, payloadMax, specs, baseSpecs, names)
	cmd := exec.CommandContext(ctx, packetGitBinary, patchGitArgs(diffRange)...)
	cmd.Dir = repo
	stderr := &diagnosticCapture{}
	cmd.Stdout = p
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	if finishErr := p.finish(); finishErr != nil && err == nil {
		err = finishErr
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return patchInventory{}, nil, sourceCommandError(packetGitBinary, timeout, "timed out", stderr.String())
	}
	if err != nil {
		return patchInventory{}, nil, sourceCommandError(packetGitBinary, timeout, err.Error(), stderr.String())
	}
	var hash [sha256.Size]byte
	copy(hash[:], p.hash.Sum(nil))
	return patchInventory{Files: p.files, Hash: hash}, append([]byte(nil), p.payload.Bytes()...), nil
}

func sourceCommandError(binary string, timeout time.Duration, problem, diagnostic string) error {
	if problem == "timed out" {
		if diagnostic != "" {
			return fmt.Errorf("%s timed out after %s; raise --timeout <seconds> to wait longer: %s", binary, timeout, diagnostic)
		}
		return fmt.Errorf("%s timed out after %s; raise --timeout <seconds> to wait longer", binary, timeout)
	}
	if diagnostic != "" {
		return fmt.Errorf("%s failed: %s: %s", binary, problem, diagnostic)
	}
	return fmt.Errorf("%s failed: %s", binary, problem)
}

func samePatch(a, b patchInventory) bool {
	return a.Hash == b.Hash
}

func patchPayloadEndsNewline(files []fileDiff, selected []bool) bool {
	for i := len(files) - 1; i >= 0; i-- {
		if selected[i] {
			return files[i].EndsNewline
		}
	}
	return false
}

func patchNotIncluded(files []fileDiff, selected []bool, rangeText string) string {
	var out strings.Builder
	out.WriteString("## Not included\n")
	cut := 0
	for i, f := range files {
		if selected[i] {
			continue
		}
		cut++
		fmt.Fprintf(&out, "%s: %d hunks, +%d -%d; print with: git diff %s -- %s\n",
			packetPathArgument(f.Path), f.Hunks, f.Added, f.Deleted, rangeText, packetPathArgument(f.Path))
	}
	if cut == 0 {
		out.WriteString("nothing\n")
	}
	return out.String()
}

// packetPathArgument is a POSIX-shell-safe spelling for the remedy line. Git
// gives paths as bytes; quote line/control characters as C escapes before the
// outer single quotes so an odd but legal path cannot forge another packet row.
func packetPathArgument(path string) string {
	safe := true
	for i := 0; i < len(path); i++ {
		c := path[i]
		if !(c == '.' || c == '/' || c == '_' || c == '-' || c == '+' || c == '=' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			safe = false
			break
		}
	}
	if safe && path != "" {
		return path
	}
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '\'':
			b.WriteString("'\\\"'\\\"'")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, "\\%03o", c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func packetSizeForRest(hdr packetHeader, restBytes int64) (int, error) {
	if restBytes < 0 || restBytes > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("packet body is too large to measure")
	}
	hdr.Bytes = 0
	for {
		n := len(hdr.String()) + 2 + int(restBytes)
		if hdr.Bytes == n {
			return n, nil
		}
		hdr.Bytes = n
	}
}

// planPatch preserves the historical greedy whole-file selection: each file is
// included only when the complete packet with every later file named in Not
// included fits. It never materializes the raw diff while evaluating a plan.
func planPatch(files []fileDiff, rangeText string, maxBytes int, hdr packetHeader, bodyPrefix string) ([]bool, string, int, error) {
	selected := make([]bool, len(files))
	for i := range files {
		trial := append([]bool(nil), selected...)
		trial[i] = true
		notIncluded := patchNotIncluded(files, trial, rangeText)
		var payload int64
		for j, f := range files {
			if trial[j] {
				payload += f.Bytes
				if payload < 0 || payload > int64(maxBytes) {
					break
				}
			}
		}
		if payload > int64(maxBytes) {
			continue
		}
		diffBytes := int64(len("## Diff "+rangeText+"\n```diff\n")) + payload
		if payload == 0 || !patchPayloadEndsNewline(files, trial) {
			diffBytes++
		}
		diffBytes += int64(len("```\n\n"))
		hdr.Cut = len(files)
		for _, include := range trial {
			if include {
				hdr.Cut--
			}
		}
		size, err := packetSizeForRest(hdr, int64(len(bodyPrefix))+diffBytes+int64(len(notIncluded)))
		if err != nil {
			return nil, "", 0, err
		}
		if size <= maxBytes {
			selected[i] = true
		}
	}
	notIncluded := patchNotIncluded(files, selected, rangeText)
	cut := 0
	for _, include := range selected {
		if !include {
			cut++
		}
	}
	var payload int64
	for i, f := range files {
		if selected[i] {
			payload += f.Bytes
		}
	}
	diffBytes := int64(len("## Diff "+rangeText+"\n```diff\n")) + payload
	if payload == 0 || !patchPayloadEndsNewline(files, selected) {
		diffBytes++
	}
	diffBytes += int64(len("```\n\n"))
	hdr.Cut = cut
	size, err := packetSizeForRest(hdr, int64(len(bodyPrefix))+diffBytes+int64(len(notIncluded)))
	if err != nil {
		return nil, "", 0, err
	}
	if size > maxBytes {
		return nil, "", 0, fmt.Errorf("--max-bytes %d cannot hold packet metadata and bounded remedy (%d bytes)", maxBytes, size)
	}
	return selected, notIncluded, cut, nil
}

func renderPatchDiff(rangeText string, payload []byte) string {
	var out strings.Builder
	fmt.Fprintf(&out, "## Diff %s\n```diff\n", rangeText)
	out.Write(payload)
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteString("```\n")
	return out.String()
}

var _ io.Writer = (*patchReader)(nil)
