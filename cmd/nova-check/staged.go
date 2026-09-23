package main

// The --staged half of nova-check nocode: the audit's classifier over the
// INDEX, reading what is about to be committed rather than what is on disk
// (SPEC.md:805). A commit commits an index, not a tree -- staging a script
// and replacing it in the working directory leaves the script in the commit
// while every working-tree reader sees prose -- so this mode classifies the
// staged records of one plumbing command, `git diff-index -r
// --ignore-submodules=none --cached -z <base> --`, whose output carries the
// mode and the content handle together.
//
// PARITY WITH THE AUDIT is by construction where this package can make it so:
// the two deny-list FLOORS are the same embedded data the audit reads
// (check.FloorDenyExts / check.FloorDenyNames), resolved through the same
// flags by the verb, and the reason strings are the audit's own so a finding
// reads the same from either mode. The rule code that joins them is a second
// copy in this package because the audit's classify is unexported in
// internal/check and this change is scoped to cmd/nova-check; folding the two
// into one shared, substrate-parameterised classifier is the follow-up this
// copy names.
//
// It is an ADVISORY and not an enforcement boundary (SPEC.md:817): a local
// check cannot be a boundary, because the committer controls whether it runs
// at all. The enforcement is the audit run in CI.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/check"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// stagedRun drives one `nova-check nocode --staged --dir <repo>` advisory and
// returns the exit code: 0 with a count of what was classified when the index
// stages no machinery, 1 with one `NOCODE FAIL <path>: <reason>` line per
// finding on stderr, 2 for every refusal. --dir is required at the verb, on
// the no-guessing law, and this function never sees it empty.
func stagedRun(dir string, allow []string, deny []string, source string, failMax int, stdout, stderr io.Writer) int {
	denySet := make(map[string]bool, len(deny))
	for _, e := range deny {
		denySet[strings.ToLower(e)] = true
	}
	// The name and path floors are the embedded floor, unconditionally, as
	// they are in the audit: --deny-ext answers which LANGUAGES a line
	// legitimately keeps, which has nothing to say about whether CI machinery
	// belongs in a prose tree. --allow is the escape, and it is the caller's.
	denyNames, denyPrefixes, err := check.FloorDenyNames()
	if err != nil {
		return refuse(stderr, " nocode", oneline.Err(err))
	}
	allowPrefixes := normalizeStagedAllow(allow)

	// The root test, then the base detector, then the one record source: each
	// refusal below is exit 2, and none of them may be read as a clean tree.
	root, rerr := stagedRoot(dir)
	if rerr != nil {
		return refuse(stderr, " nocode", rerr.Error())
	}
	base, berr := stagedBase(root)
	if berr != nil {
		return refuse(stderr, " nocode", berr.Error())
	}
	raw, derr := stagedGit(root, "diff-index", "-r", "--ignore-submodules=none", "--cached", "-z", base, "--")
	if derr != nil {
		// Every dynamic piece of a refusal reaches the stream through refuse,
		// which escapes the whole line; oneline.Err escapes git's text here
		// so the message is safe even before that.
		return refuse(stderr, " nocode", "git diff-index failed against "+base+": "+oneline.Err(derr)+"; a failed diff-index is never a clean tree")
	}
	records, perr := parseDiffIndex(raw)
	if perr != nil {
		return refuse(stderr, " nocode", perr.Error())
	}

	var (
		classified int
		blobs      []stagedRecord
		findings   []check.Failure
	)
	for _, rec := range records {
		switch rec.status {
		case "D":
			// THE ONE SKIP, and a real status skip, never an inference from a
			// missing working-tree file: `git add evil.sh && rm evil.sh`
			// leaves an A record whose blob still carries the shebang while
			// the file is gone from disk. The inference was the original
			// design's root defect.
			continue
		case "A", "M", "T":
			// Classified on the destination mode and OID below.
		case "U":
			// An unmerged entry has an all-zero destination and no staged
			// content to classify; git refuses the commit in this state too.
			return refuse(stderr, " nocode", "the index holds unmerged entries ("+rec.path+"); resolve the conflict and commit again")
		default:
			// A switch with no default has an unbounded skip list: a gate
			// that skips what it does not recognise is a fail-open whose size
			// nobody can state, so an unrecognised letter stops the check.
			return refuse(stderr, " nocode", "unrecognised status letter "+strconv.Quote(rec.status)+" on record for "+rec.path+"; this mode classifies A, M and T, skips D, and refuses the rest")
		}
		if isStagedAllowed(rec.path, allowPrefixes) {
			continue
		}
		classified++
		switch rec.dstMode {
		case "100644", "100755":
			// A blob record: the content is read by destination OID through
			// the one batch reader below, never a process per path.
			blobs = append(blobs, rec)
		case "120000":
			// A symlink: the audit's disposition, unchanged. The name and the
			// location are classified, and the target is not read and the
			// mode is not consulted, so a gate cannot be walked out of the
			// tree it guards. A clean-named link is clean.
			reasons := stagedPathReasons(rec.path, denySet, source, denyNames, denyPrefixes)
			if len(reasons) > 0 {
				reasons = append(reasons, "symlink (target not followed)")
				findings = append(findings, check.Failure{Subject: rec.path, Reason: strings.Join(reasons, "; ")})
			}
		case "160000":
			// A gitlink -- the ONE matching rule this mode adds, because the
			// index carries a type a filesystem walk never sees. Its
			// destination OID is a commit in another repository and is not an
			// object in this one: it is classified from its mode alone and
			// its OID is never read, so it cannot collide with the
			// unreadable-blob disposition. Machinery arriving by reference,
			// suppressible by --allow like any other path.
			reasons := stagedPathReasons(rec.path, denySet, source, denyNames, denyPrefixes)
			reasons = append(reasons, "submodule gitlink (machinery arriving by reference)")
			findings = append(findings, check.Failure{Subject: rec.path, Reason: strings.Join(reasons, "; ")})
		default:
			// Reachable: it is what a 040000 sparse-directory record trips if
			// -r is ever dropped from the record source, which is the whole
			// reason to write the branch rather than leave a four-way switch
			// with no default.
			return refuse(stderr, " nocode", "staged record for "+rec.path+" has destination mode "+rec.dstMode+", which this mode does not classify")
		}
	}

	heads, herr := stagedBlobHeads(root, blobs)
	if herr != nil {
		return refuse(stderr, " nocode", oneline.Err(herr))
	}
	for _, rec := range blobs {
		reasons := stagedPathReasons(rec.path, denySet, source, denyNames, denyPrefixes)
		if rec.dstMode == "100755" {
			// ONE BIT where the audit reads three: git derives the index mode
			// from the owner bit alone, and core.fileMode=false makes every
			// newly added entry 100644. That is the sharpest reason this mode
			// is an advisory, and it is named here rather than hidden.
			reasons = append(reasons, "executable (index mode 100755)")
		}
		head := heads[rec.dstOID]
		switch {
		case head.unreadable != "":
			// The audit's disposition for content it cannot rule on, over the
			// object store instead of the filesystem: a FINDING, not a
			// refusal, and never a pass.
			reasons = append(reasons, "unreadable blob: "+head.unreadable+" (cannot rule out machinery)")
		case len(head.head) == 2 && string(head.head) == "#!":
			// The tell that survives renaming: a script with no extension and
			// no executable bit is still a script. A blob shorter than two
			// bytes genuinely holds no shebang and is not a read failure.
			reasons = append(reasons, "executable script (shebang)")
		}
		if len(reasons) > 0 {
			findings = append(findings, check.Failure{Subject: rec.path, Reason: strings.Join(reasons, "; ")})
		}
	}

	if len(findings) > 0 {
		list := bounded.Capped(stderr, failMax, "NOCODE", "path", failMaxRemedy)
		for _, f := range findings {
			list.Line(fmt.Sprintf("NOCODE FAIL %s: %s", oneline.Escape(f.Subject), oneline.Escape(oneline.Cap(f.Reason, oneline.TailBytes))))
		}
		list.More()
		fmt.Fprintf(stderr, "NOCODE FAIL staged=%d findings=%d shown=%d deny-list=%s\n", classified, list.Total(), list.Shown(), oneline.Field(source))
		return 1
	}
	// A clean run prints the audit's OK line, with the count of the records
	// classified: nothing staged, or deletions only, is a count of zero and
	// exit 0 -- an empty change set is a fact about the commit, not a broken
	// check.
	fmt.Fprintf(stdout, "NOCODE OK staged=%d clean deny-list=%s\n", classified, oneline.Field(source))
	return 0
}

// stagedRoot is the root test (SPEC.md:1021): `git -C <dir> rev-parse
// --show-toplevel`, compared with --dir after resolving symlinks on BOTH
// sides -- never a test for .git being a directory, which is false in a
// linked worktree and in a submodule, both legitimate places to commit from.
// The resolved root is what every later git call runs with -C.
func stagedRoot(dir string) (string, error) {
	out, err := stagedGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("--dir %s is not the root of a git repository (git rev-parse --show-toplevel: %s)", dir, err)
	}
	top := strings.TrimSpace(out)
	if top == "" {
		return "", fmt.Errorf("--dir %s is not the root of a git repository", dir)
	}
	want, err := stagedResolved(dir)
	if err != nil {
		return "", err
	}
	got, err := stagedResolved(top)
	if err != nil {
		return "", err
	}
	if want != got {
		return "", fmt.Errorf("--dir %s is not the root of a git repository (the root there is %s); --staged reads the index at the repository root", dir, top)
	}
	return want, nil
}

// stagedResolved is an absolute path with every symlink taken out of it,
// which is what makes two spellings of one directory comparable. On this
// platform /var is such a link, and a --dir under TMPDIR would otherwise
// disagree with git about its own name.
func stagedResolved(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("--dir %s: %w", dir, err)
	}
	full, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("--dir %s: %w", dir, err)
	}
	return full, nil
}

// stagedBase is the base detector (SPEC.md:1028): the base is HEAD, except on
// an unborn HEAD, where the comparison is against the EMPTY TREE obtained
// from the repository itself. THE DETECTOR IS THE EXIT CODE, never git's
// wording: the failure text is not stable across invocations, and a
// specification that pins another tool's error string acquires a dependency
// it cannot maintain. The trade is deliberate -- a repository's first commit
// is gated like every later one, because skipping the check where there is no
// HEAD makes the first commit the one place machinery enters unexamined.
func stagedBase(root string) (string, error) {
	if err := exec.Command("git", "-C", root, "rev-parse", "-q", "--verify", "HEAD").Run(); err != nil {
		// Run INSIDE the repository: outside one this command answers the
		// sha1 spelling regardless of what the repository is, and the sha1
		// constant 4b825dc6... names no object a sha256 repository knows.
		out, herr := stagedGit(root, "hash-object", "-t", "tree", os.DevNull)
		if herr != nil {
			return "", fmt.Errorf("HEAD is unborn and the empty tree could not be obtained inside %s: %s", root, herr)
		}
		base := strings.TrimSpace(out)
		if base == "" {
			return "", fmt.Errorf("HEAD is unborn and git hash-object printed no empty tree inside %s", root)
		}
		return base, nil
	}
	return "HEAD", nil
}

// stagedGit runs one git plumbing call with -C root and the caller's
// environment INTACT -- the hook is handed GIT_INDEX_FILE, and a tool that
// scrubbed or re-anchored the environment would read a different index than
// the one being committed -- and returns stdout. On failure stderr's first
// line travels with the error, which is where git puts the fatal; an
// implementer who read a FAILED diff-index's empty stdout as "nothing is
// staged" would ship a gate that goes green with the commit unexamined.
func stagedGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return out.String(), nil
}

// stagedRecord is one `git diff-index --cached -z` record:
// `:<srcmode> <dstmode> <srcOID> <dstOID> <status>`. The mode and the content
// handle travel together in one record, which is why the source is one
// command: a second command joining a path back to its content is the seam
// two earlier designs' bypasses lived in.
type stagedRecord struct {
	srcMode string
	dstMode string
	srcOID  string
	dstOID  string
	status  string
	path    string
}

// parseDiffIndex splits the NUL-separated metadata/path pairs of the record
// source. The trailing `--` on the command is load-bearing: without it, a
// repository holding a file named HEAD makes diff-index exit 128 with
// "ambiguous argument" on every commit.
func parseDiffIndex(raw string) ([]stagedRecord, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, "\x00")
	if last := parts[len(parts)-1]; last == "" {
		// The output ends at a NUL, so the split ends with an empty part.
		parts = parts[:len(parts)-1]
	}
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("malformed diff-index output: %d NUL-separated fields, not the metadata/path pairs this mode reads", len(parts))
	}
	var recs []stagedRecord
	for i := 0; i < len(parts); i += 2 {
		meta, path := parts[i], parts[i+1]
		if !strings.HasPrefix(meta, ":") {
			return nil, fmt.Errorf("malformed diff-index record %q: no leading ':'", meta)
		}
		f := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(f) != 5 {
			return nil, fmt.Errorf("malformed diff-index record %q, want :<srcmode> <dstmode> <srcOID> <dstOID> <status>", meta)
		}
		recs = append(recs, stagedRecord{srcMode: f[0], dstMode: f[1], srcOID: f[2], dstOID: f[3], status: f[4], path: path})
	}
	return recs, nil
}

// stagedPathReasons is the audit's classifier over the parts of a record that
// need no substrate: the name floor, the location floor and the extension
// floor. The reason strings are the audit's own, so a finding reads the same
// whichever mode produced it. Names and locations are lowercased on both
// sides, matching the audit, and a trailing space in an extension is trimmed
// for matching only.
func stagedPathReasons(rel string, denySet map[string]bool, source string, denyNames map[string]bool, denyPrefixes []string) []string {
	// Every dynamic piece is escaped where it is built, the same shape
	// main.go's own NOTHING warning uses, so a reason is one line whatever a
	// staged path carries; the FAIL print site escapes again, and the double
	// escape is idempotent because oneline never escapes a backslash.
	var reasons []string
	if base := strings.TrimSpace(strings.ToLower(filepath.Base(rel))); denyNames[base] {
		reasons = append(reasons, "build machinery by name "+oneline.Escape(base)+" (floor name list)")
	}
	lowerRel := strings.ToLower(rel)
	for _, pre := range denyPrefixes {
		if lowerRel == pre || strings.HasPrefix(lowerRel, pre+"/") {
			reasons = append(reasons, "machinery by location "+oneline.Escape(pre)+"/ (floor name list)")
			break
		}
	}
	if ext := strings.TrimSpace(strings.ToLower(filepath.Ext(rel))); denySet[ext] {
		reasons = append(reasons, "code extension "+oneline.Escape(ext)+" ("+oneline.Escape(source)+")")
	}
	return reasons
}

// normalizeStagedAllow trims each --allow entry to a bare repo-relative
// prefix, and isStagedAllowed reports whether a record's path is, or lies
// beneath, one. The same law the audit's --allow keeps: every scope narrowing
// is the caller's, stated per run, and a prefix covers everything beneath it
// at any depth.
func normalizeStagedAllow(allow []string) []string {
	out := make([]string, 0, len(allow))
	for _, a := range allow {
		a = strings.TrimSpace(a)
		a = strings.TrimPrefix(a, "./")
		a = strings.Trim(a, "/")
		if a != "" && a != "." {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func isStagedAllowed(rel string, allow []string) bool {
	for _, a := range allow {
		if rel == a || strings.HasPrefix(rel, a+"/") {
			return true
		}
	}
	return false
}

// stagedBlobHead is the first two bytes of one staged blob -- all a shebang
// needs -- and, when they could not be read, why.
type stagedBlobHead struct {
	head       []byte
	unreadable string // "" when the head was read
}

// stagedBlobHeads reads the first two bytes of every blob record's
// destination OID through ONE `git cat-file --batch` fed from the record
// list, never a process per path -- a pre-commit that takes 37 seconds is
// disabled exactly the way an advisory that refuses on every commit is. The
// two requirements the reader must both keep: stay FRAMED on the stream,
// consuming each record whole rather than two bytes and moving on, since a
// desynchronised reader slides onto the next object's bytes; and do not
// buffer a whole object, since a staged blob may be gigabytes while two bytes
// decide a shebang. `missing` and `ambiguous` replies are one line with no
// body and desync a reader that assumes one.
func stagedBlobHeads(root string, recs []stagedRecord) (map[string]stagedBlobHead, error) {
	var order []string
	seen := map[string]bool{}
	for _, r := range recs {
		// Content is read by DESTINATION OID, the fourth field: the third is
		// the source OID, all-zero for an added file, and a path is never
		// re-parsed to reach a blob.
		if !seen[r.dstOID] {
			seen[r.dstOID] = true
			order = append(order, r.dstOID)
		}
	}
	heads := make(map[string]stagedBlobHead, len(order))
	if len(order) == 0 {
		return heads, nil
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	// stderr does NOT share the stdout pipe: the batch's diagnostics print
	// there, and a reader that shares the pipe desynchronises on exactly the
	// frame this function exists to keep.
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	readErr := func() error {
		r := bufio.NewReader(stdout)
		for _, oid := range order {
			// The oid is a LOOKUP KEY to git's stdin, not display text, and
			// it must reach git verbatim; the exemption in the audit config
			// names this site and that reason. Nothing this pipe carries is
			// ever printed.
			if _, err := fmt.Fprintf(stdin, "%s\n", oid); err != nil {
				return fmt.Errorf("git cat-file --batch: %w", err)
			}
			line, err := r.ReadString('\n')
			if err != nil {
				return fmt.Errorf("git cat-file --batch: the reply for %s never completed its header line: %w", oid, err)
			}
			f := strings.Fields(line)
			if len(f) < 2 {
				return fmt.Errorf("git cat-file --batch: the reply for %s is not a framed line: %q", oid, line)
			}
			if f[1] == "missing" || f[1] == "ambiguous" {
				// One line, no body, no trailing newline.
				heads[oid] = stagedBlobHead{unreadable: f[1]}
				continue
			}
			if len(f) < 3 {
				return fmt.Errorf("git cat-file --batch: the reply for %s carries no size: %q", oid, line)
			}
			size, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil || size < 0 {
				return fmt.Errorf("git cat-file --batch: the reply for %s has no readable size: %q", oid, line)
			}
			why := ""
			if f[1] != "blob" {
				// The frame is still consumed whole; only the classification
				// declines to trust it.
				why = "object is a " + f[1] + ", not a blob"
			}
			n := int64(2)
			if size < n {
				n = size
			}
			head := make([]byte, n)
			if _, err := io.ReadFull(r, head); err != nil {
				return fmt.Errorf("git cat-file --batch: reading %s: %w", oid, err)
			}
			if _, err := io.CopyN(io.Discard, r, size-n); err != nil {
				return fmt.Errorf("git cat-file --batch: draining %s: %w", oid, err)
			}
			end, err := r.ReadByte()
			if err != nil {
				return fmt.Errorf("git cat-file --batch: draining %s: %w", oid, err)
			}
			if end != '\n' {
				return fmt.Errorf("git cat-file --batch: the stream desynchronised after %s (found %q, want the record's newline)", oid, end)
			}
			heads[oid] = stagedBlobHead{head: head, unreadable: why}
		}
		return nil
	}()
	stdin.Close()
	// Wait always runs, so the batch is never left writing into a closed
	// reader. An early read error leaves it with unread requests; its
	// complaint is reported only when nothing louder already happened.
	if werr := cmd.Wait(); readErr == nil && werr != nil {
		readErr = fmt.Errorf("git cat-file --batch: %w", werr)
	}
	return heads, readErr
}
