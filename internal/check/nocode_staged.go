package check

// nocode_staged.go is the index-side counterpart to nocode.go: the
// classifier the audit uses, called UNCHANGED over the OIDs `git
// diff-index --cached` hands out, framed through ONE `git cat-file --batch`
// pipe (SPEC.md 917). The audit reads the filesystem; this path reads the
// object store. Neither reads the path back as a gitrevision -- the
// trapdoor `:path` resolves as stage 0 of every colon-prefixed NAME
// (`0:notes.md`, `:1`, `:2:foo`), and a path-shaped read returns the wrong
// bytes at exit 0 (SPEC.md 929-934).
//
// Why the unit submission is `--staged` rather than the audit: the SPEC
// declares this mode ADVISORY, not enforcement, because the committer
// controls whether a local hook runs. The byte-source difference is what
// makes this card distinct from the audit -- a shebang is the first two
// BYTES of a blob, not of a filesystem path, and the read must consume
// those two bytes and drain the rest with out-of-band buffering or it
// pages its gigabyte-blob answer entirely into the auditor (SPEC.md 925).
//
// The repo is one bare `git -C <resolved-dir>` invocation of `diff-index
// -r --ignore-submodules=none --cached -z HEAD --`, with no `-M`. The
// object read is one bare `git -C <same-dir> cat-file --batch` process,
// fed one OID per answer and answered once each. Stderr is split off
// the stdout pipe -- the framework's `error:` and `hint:` lines for an
// `ambiguous` reply are not pipelined into the framed reader (SPEC.md
// 942). The dispatch from `NoCode` to this path is the `Stage: true`
// flag on `NoCodeOptions`.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gitBatchTimeout is the budget a single cat-file --batch process is
// given from open to close. The wake mirror uses 2 minutes (laneread.go);
// that's fine here too -- a frame budget of styled-real reads against
// one repo, with the sink under the auditor.
const gitBatchTimeout = 2 * time.Minute

// gitBatchWall is the read deadline on the cat-file --batch pipe. A wedged
// git cannot hold a poll open with a child still running; killing the
// process closes the pipes after a bounded grace.
const gitBatchWall = 30 * time.Second

// StagedRecord is one row of `git diff-index -z --cached`. After the
// leading `:`, five fields appear in this exact order: srcmode, dstmode,
// srcOID, dstOID, status. The path follows the first NUL.
//
// The DST -- "destination" -- OID is the FOURTH field. The third is the
// SOURCE OID, ALL-ZERO for every file added in this commit (SPEC.md
// 935). The two are not interchangeable under --batch: the third
// answers "missing" at exit 0 even when there is a real blob at the
// fourth.
type StagedRecord struct {
	SrcMode string
	DstMode string
	SrcOID  string
	DstOID  string
	Status  string
	Path    string
}

// StageRecords runs `git -C dir diff-index -r --ignore-submodules=none
// --cached -z HEAD --` and parses the NUL-separated records.
//
// The flags are not negotiable. `-r` expands the sparse-index 040000
// records into blob records, so `git add out/deep/evil.sh` is named
// rather than collapsed into a directory (SPEC.md 891). `--cached` is
// the index. `--ignore-submodules=none` keeps 160000 gitlink records
// visible to a check that inherits its subject's configuration (SPEC.md
// 881). `-z` keeps the path through C-quoting intact instead of being
// C-escaped (SPEC.md 654 in laneread.go is the same load-bearing half).
// The trailing `--` keeps a file named HEAD out of the parser's
// revision syntax (SPEC.md 904).
//
// `-M` is deliberately NOT passed: a rename arrives as D of the old
// path and A of the new (SPEC.md 945).
func StageRecords(dir string) ([]StagedRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitBatchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git",
		"-C", dir,
		"diff-index",
		"-r",
		"--ignore-submodules=none",
		"--cached",
		"-z",
		"HEAD",
		"--",
	)
	cmd.Env = append(cmd.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-index --cached: %v", err)
	}
	return parseStagedRecords(out)
}

// parseStagedRecords splits the NUL-separated diff-index output. Each
// record is ":srcmode dstmode srcOID dstOID status\0path\0"; trailing
// garbage without a colon is silently skipped because the upstream
// command keeps its output well-formed and anything else would be a
// hard error in production, but the empty record list -- the most
// common cardinally clean case -- has to come out ZERO not refuse.
func parseStagedRecords(raw []byte) ([]StagedRecord, error) {
	s := string(raw)
	var recs []StagedRecord
	for {
		if s == "" {
			break
		}
		if s[0] != ':' {
			// junk or trailing separator; do not pretend it parses.
			return nil, fmt.Errorf("malformed staged record: leading byte %q", s[:1])
		}
		i := strings.IndexByte(s, '\x00')
		if i < 0 {
			return nil, fmt.Errorf("malformed staged record (no NUL after metadata): %q", s)
		}
		meta := s[1:i]
		rest := s[i+1:]
		j := strings.IndexByte(rest, '\x00')
		if j < 0 {
			return nil, fmt.Errorf("malformed staged record (no trailing NUL): %q", rest)
		}
		path := rest[:j]
		s = rest[j+1:]
		fields := strings.Fields(meta)
		if len(fields) != 5 {
			return nil, fmt.Errorf("malformed staged record (want 5 fields, got %d) in %q", len(fields), meta)
		}
		recs = append(recs, StagedRecord{
			SrcMode: fields[0],
			DstMode: fields[1],
			SrcOID:  fields[2],
			DstOID:  fields[3],
			Status:  fields[4],
			Path:    path,
		})
	}
	return recs, nil
}

// CatFileBatch is one `git cat-file --batch` child held open. The caller
// feeds OIDs on b.in and reads frames off b.out; stdout and stderr are
// separated at the OS pipe level so an ambiguous reply's `error:` and
// `hint:` lines do NOT enter the framed reader -- they were measured to
// desync exactly the framing this card demands (SPEC.md 942).
type CatFileBatch struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	in     io.WriteCloser
	out    *bufio.Reader
	errb   *os.File
}

// OpenCatFileBatch starts one `git -C dir cat-file --batch` child under a
// bounded budget. The caller MUST call Close; the child is killed on
// Close and the half-held pipes are released.
//
// Stderr is split from stdout with an os.Pipe so the framework's
// diagnostic lines can NEVER reach the framed reader. Without that split
// the reader desyncs on its very first ambiguous reply -- the SPEC
// measured the failure (SPEC.md 941-944).
//
// The context's cancel is kept on the returned struct and called only
// at Close, NOT through a defer here. A deferred cancel fires on
// successful return -- which is when the long-lived batch must stay
// ticking -- and would otherwise kill git before the first ReadHead.
func OpenCatFileBatch(dir string) (*CatFileBatch, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitBatchTimeout)
	cmd := exec.CommandContext(ctx, "git",
		"-C", dir,
		"cat-file", "--batch",
	)
	cmd.Env = append(cmd.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file --batch stdin: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		cancel()
		return nil, fmt.Errorf("cat-file --batch stdout pipe: %w", err)
	}
	cmd.Stdout = stdoutW
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		_ = stdoutW.Close()
		_ = stdoutR.Close()
		cancel()
		return nil, fmt.Errorf("cat-file --batch stderr pipe: %w", err)
	}
	cmd.Stderr = stderrW
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		_ = stdoutW.Close()
		_ = stdoutR.Close()
		_ = stderrW.Close()
		_ = stderrR.Close()
		cancel()
		return nil, fmt.Errorf("cat-file --batch start: %w", err)
	}
	_ = stdoutW.Close()
	_ = stderrW.Close()
	return &CatFileBatch{
		cmd:    cmd,
		cancel: cancel,
		in:     in,
		out:    bufio.NewReaderSize(stdoutR, 64<<10),
		errb:   stderrR,
	}, nil
}

// BatchReply is one cat-file --batch header line's worth of answer.
//
//   - Status: "missing" or "ambiguous" for the failure cases -- both are
//     one-line answers with NO body and the reader must not steal a byte
//     of the next record (SPEC.md 926-928).
//   - Status: the type, "blob", "tree", "commit", "tag" -- for normal
//     answers.
//
// Size is the byte length of the body, zero for missing/ambiguous.
type BatchReply struct {
	Status string
	Size   int
	Body   []byte
}

// ReadHead asks the batch for `oid` and returns up to n bytes of body.
//
// A missing or ambiguous reply has NO body and the reader leaves the
// pipe in step (SPEC.md 927). A normal reply returns the type and size
// in the header; the body is read up to n bytes, then the rest is
// dropped with io.CopyN(io.Discard, ...) so the stream is in step for
// the next call. A 1 MiB blob whose only role is to decide whether the
// first two bytes are a shebang stays exactly two-decoded bytes in
// memory; the rest is charged but not decoded.
//
// The wall deadline is the batch's whole budget, so a wedged git cannot
// hold the reader open against a deadline it cannot see -- and the
// caller is told via error, not via a possibly-valid header line that
// would silently drop the framing.
func (b *CatFileBatch) ReadHead(oid string, n int) (BatchReply, error) {
	var rep BatchReply
	if oid == "" {
		return rep, errors.New("cat-file batch: empty OID request")
	}
	if b == nil || b.cmd == nil || b.cmd.ProcessState != nil {
		return rep, errors.New("cat-file batch: not running")
	}
	if _, err := io.WriteString(b.in, oid+"\n"); err != nil {
		return rep, fmt.Errorf("cat-file batch write %s: %w", oid, err)
	}
	if b.errb != nil {
		_ = b.errb.SetReadDeadline(time.Now().Add(gitBatchWall))
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return rep, fmt.Errorf("cat-file batch header for %s: %w", oid, err)
	}
	header = strings.TrimRight(header, "\r\n")
	fields := strings.Fields(header)
	switch len(fields) {
	case 2:
		// "<oid> missing" or "<oid> ambiguous" -- one line, no body.
		rep.Status = fields[1]
		return rep, nil
	case 3:
		// "<oid> <type> <size>\n<body><LF>"
		rep.Status = fields[1]
		sz, perr := strconv.Atoi(fields[2])
		if perr != nil {
			return rep, fmt.Errorf("cat-file batch bad size for %s: %q", oid, header)
		}
		rep.Size = sz
		cap := n
		if cap > sz {
			cap = sz
		}
		if cap > 0 {
			buf := make([]byte, cap)
			if _, err := io.ReadFull(b.out, buf); err != nil {
				return rep, fmt.Errorf("cat-file batch body for %s: %w", oid, err)
			}
			rep.Body = buf
		}
		// Drain the rest of the body and its trailing newline, so the
		// stream is in step for the next call. This is the part of
		// the spec that LOOKS like a memory leak; it isn't: the rest
		// is read into io.Discard, never into a slice we keep.
		rest := sz - cap + 1
		if rest > 0 {
			if _, err := io.CopyN(io.Discard, b.out, int64(rest)); err != nil {
				return rep, fmt.Errorf("cat-file batch drain for %s: %w", oid, err)
			}
		}
		return rep, nil
	default:
		return rep, fmt.Errorf("cat-file batch unexpected reply for %s: %q", oid, header)
	}
}

// Close releases the child process and its pipes. Calling Close on a
// nil batch is a no-op so callers can chain a defer unconditionally.
//
// The timer context is cancelled at the start of Close so a wedged
// child is killed before its Wait(); the half-held pipes are then
// released in order so the child cannot see the parent close a pipe
// before it sees its stdin gone.
func (b *CatFileBatch) Close() error {
	if b == nil {
		return nil
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.in != nil {
		_ = b.in.Close()
	}
	if b.errb != nil {
		_ = b.errb.Close()
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Wait()
	}
	return nil
}

// noCodeStaged runs the audit over the INDEX -- every staged path is fed
// the dstOID from `git diff-index --cached` (the FOURTH field, never the
// all-zero SOURCE OID of the third), and the bytes are read through one
// `git cat-file --batch` pipe and classified by the same classifier the
// walk uses (SPEC.md 834-846).
//
// Notes contained in n are passed through to the classifier unchanged;
// --allow and the two --deny-ext flags continue to govern the floor
// list in both modes. --allow is consumed as a directory prefix on the
// staged path, identical to its meaning in the walk.
//
// Refuses an unrecognised destination mode, an unrecognised status
// letter, and the unmerged-`U` case (its destination is `000000`,
// therefore no staged content to classify, therefore a refusal:
// SPEC.md 962). Reports an `unreadable blob` finding when cat-file
// answers `missing` or `ambiguous` -- the spec names that a finding is
// the correct disposition rather than a refusal (SPEC.md 1000). On a
// `--dir` that is not a working tree of a git repository, refuses.
func noCodeStaged(opts NoCodeOptions) (scanned int, findings []Failure, err error) {
	deny := opts.DenyExt
	source := opts.DenySource
	if len(deny) == 0 {
		deny, err = FloorDenyExts()
		if err != nil {
			return 0, nil, err
		}
		source = DenyFloor
	} else if source == "" {
		return 0, nil, errors.New("DenyExt was set without DenySource: a finding may not name an unknown list")
	}
	denySet := make(map[string]bool, len(deny))
	for _, e := range deny {
		denySet[strings.ToLower(e)] = true
	}
	denyNames, denyPrefixes, err := FloorDenyNames()
	if err != nil {
		return 0, nil, err
	}
	allow := normalizeAllow(opts.Allow)

	root, evaluated := opts.Dir, false
	if filepath.IsAbs(root) {
		ev, eerr := filepath.EvalSymlinks(root)
		if eerr == nil {
			root = ev
			evaluated = true
		}
	}
	info, statErr := os.Stat(root)
	if statErr != nil {
		return 0, nil, fmt.Errorf("dir %q: %w", opts.Dir, statErr)
	}
	if !info.IsDir() {
		return 0, nil, fmt.Errorf("dir %q is not a directory", opts.Dir)
	}
	_ = evaluated

	recs, err := StageRecords(root)
	if err != nil {
		return 0, nil, fmt.Errorf("staged records: %w", err)
	}
	if len(recs) == 0 {
		return 0, nil, nil
	}
	batch, err := OpenCatFileBatch(root)
	if err != nil {
		return 0, nil, fmt.Errorf("cat-file --batch: %w", err)
	}
	defer batch.Close()

	seen := make(map[string]bool)
	for _, r := range recs {
		if r.Path == "" {
			continue
		}
		if seen[r.Path] {
			return scanned, findings, fmt.Errorf("staged record path %q appears twice in diff-index output", r.Path)
		}
		seen[r.Path] = true
		if isAllowed(r.Path, allow) {
			continue
		}
		switch r.Status {
		case "D":
			continue
		case "U":
			return scanned, findings, fmt.Errorf("unmerged entry %q; refusing (SPEC.md 962)", r.Path)
		}
		switch r.DstMode {
		case "100644", "100755":
			perm, perr := strconv.ParseUint(r.DstMode, 8, 32)
			if perr != nil {
				return scanned, findings, fmt.Errorf("%s: dstmode %q: %w", r.Path, r.DstMode, perr)
			}
			scanned++
			rep, rerr := batch.ReadHead(r.DstOID, 2)
			if rerr != nil {
				return scanned, findings, fmt.Errorf("%s: cat-file batch: %w", r.Path, rerr)
			}
			if rep.Status == "missing" || rep.Status == "ambiguous" {
				findings = append(findings, Failure{
					Subject: r.Path,
					Reason:  fmt.Sprintf("unreadable blob (cat-file batch %s): cannot rule out machinery", rep.Status),
				})
				continue
			}
			findings = append(findings, classifyFromBatch(r.Path, os.FileMode(perm), rep, denySet, source, denyNames, denyPrefixes)...)
		case "120000":
			scanned++
			scanned, findings = appendSymlink(scanned, findings, r.Path, denySet, source, denyNames, denyPrefixes)
		case "160000":
			scanned++
			findings = append(findings, Failure{
				Subject: r.Path,
				Reason:  "machinery by location (gitlink: dstmode 160000 arrives from another repository; OID not read)",
			})
		default:
			return scanned, findings, fmt.Errorf("%s: dstmode %q is not a classified mode", r.Path, r.DstMode)
		}
	}
	sortFindings(findings)
	return scanned, findings, nil
}

// appendSymlink is the classifier branch a 120000 record reaches: only
// the path-side rules (name, location, extension), never the
// executable-bit, never the shebang (SPEC.md 989). A symlink named
// `run.sh` is still machinery by the same argument that catches a file
// named `run.sh`.
func appendSymlink(scanned int, findings []Failure, rel string, denySet map[string]bool, source string, denyNames map[string]bool, denyPrefixes []string) (int, []Failure) {
	reasons := pathOnlyReasons(rel, denySet, source, denyNames, denyPrefixes)
	if len(reasons) > 0 {
		reasons = append(reasons, "symlink (target not followed)")
		findings = append(findings, Failure{Subject: rel, Reason: strings.Join(reasons, "; ")})
	}
	return scanned, findings
}

// classifyFromBatch is the SHEBANG+extension+location rules over the
// first two bytes of a blob. The bytes are returned by the cat-file
// batch, not by reading the path; if the byte slice is short, the
// shebang check returns false without reading more.
func classifyFromBatch(rel string, perm os.FileMode, rep BatchReply, denySet map[string]bool, source string, denyNames map[string]bool, denyPrefixes []string) []Failure {
	var reasons []string
	reasons = append(reasons, pathOnlyReasons(rel, denySet, source, denyNames, denyPrefixes)...)
	if perm&0o111 != 0 {
		reasons = append(reasons, fmt.Sprintf("executable (mode %04o)", perm))
	}
	if hasShebangBytes(rep.Body) {
		reasons = append(reasons, "executable script (shebang)")
	}
	if len(reasons) == 0 {
		return nil
	}
	return []Failure{{Subject: rel, Reason: strings.Join(reasons, "; ")}}
}

// pathOnlyReasons returns the SUBSTRATE-AGNOSTIC reasons: name, location,
// extension. They are the same on the walk and on the staged path
// because the spec pins them to ONE statement (SPEC.md 858).
func pathOnlyReasons(rel string, denySet map[string]bool, source string, denyNames map[string]bool, denyPrefixes []string) []string {
	var reasons []string
	if base := strings.TrimSpace(strings.ToLower(filepath.Base(rel))); denyNames[base] {
		reasons = append(reasons, fmt.Sprintf("build machinery by name %s (floor name list)", base))
	}
	lowerRel := strings.ToLower(rel)
	for _, pre := range denyPrefixes {
		if lowerRel == pre || strings.HasPrefix(lowerRel, pre+"/") {
			reasons = append(reasons, fmt.Sprintf("machinery by location %s/ (floor name list)", pre))
			break
		}
	}
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(rel))); denySet[ext] {
		reasons = append(reasons, fmt.Sprintf("code extension %s (%s)", ext, source))
	}
	return reasons
}

// hasShebangBytes reports whether the first two bytes begin "#!".
// This is the staged-side substrate for hasShebang in nocode.go; the
// walk's reader opens a file and Peek(2)s, this reader takes a byte
// slice the batch has already returned. A short blob -- fewer than
// two bytes -- is not a shebang.
func hasShebangBytes(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	return b[0] == '#' && b[1] == '!'
}

// sortFindings orders findings by Subject so error messages stay stable
// across runs -- the staged path may produce them in record order, but
// the audit tests expect to scan an unordered set against wantExactly.
func sortFindings(fs []Failure) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].Subject < fs[j].Subject })
}
