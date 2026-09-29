package definition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultGitTimeout is the deadline of every git invocation when the caller's
// context carries none sooner.
const DefaultGitTimeout = 30 * time.Second

// GitCalls is the number of git invocations one Pin makes: at most this many,
// whatever the size of the array (the origin lookup is skipped when the caller
// supplies the identity).
const GitCalls = 5

// Pinned is one committed blob, pinned: the repository identity, the full commit,
// the repository-relative path, the Git object id, the SHA-256 of the bytes, and
// the bytes, ready for Parse.
type Pinned struct {
	Repository string
	Commit     string
	Path       string
	ObjectID   string
	SHA256     string
	Mode       string
	Size       int
	Data       []byte
}

type pinOptions struct{ identity string }

// PinOption adjusts Pin.
type PinOption func(*pinOptions)

// WithIdentity supplies the repository identity, in place of the normalized
// origin URL. It is the identity to use when the repository has no origin or an
// origin that is a local path.
func WithIdentity(identity string) PinOption {
	return func(o *pinOptions) { o.identity = identity }
}

var (
	commitRE = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	driveRE  = regexp.MustCompile(`^[A-Za-z]:`)
)

// Pin reads the committed blobs of an array of repository-relative paths at one
// full commit of the Git repository at repoDir, and pins each: repository
// identity, commit, path, object id, SHA-256, and the bytes. Working files, the
// index and refs are never read. It runs at most GitCalls git invocations, each
// under a deadline, and never fetches, publishes or opens the network.
//
// The repository identity is the normalized origin URL (host/path, no scheme, no
// user, no .git); a repository with no usable origin is refused unless the caller
// supplies an identity with WithIdentity. Git runs with the global and system
// configuration off and every GIT_ variable of the environment removed, so the
// result does not depend on the machine's git setup.
//
// Refused, with the whole array: a commit that is not a full lower-case object id,
// an unknown commit or one that is not a commit, a path that is empty, absolute,
// climbs out of the repository or is not in canonical form, a repeated path, a
// directory that is not the root of a repository, and, per path, a path missing at
// that commit, a symlink, a non-blob, and a blob over MaxCardBytes. The array is
// at most MaxFiles paths and MaxTotalBytes bytes.
func Pin(ctx context.Context, repoDir string, commit string, paths []string, opts ...PinOption) ([]Pinned, []Refusal) {
	return pin(ctx, &gitRun{dir: repoDir}, repoDir, commit, paths, opts...)
}

func pinRefusal(path string, c Cause, found, next string) Refusal {
	return Refusal{Operation: OpPin, File: path, Cause: c, Found: found, Next: next}
}

// pathWhy is why a repository-relative path is refused, with its cause, "" when
// it is fine: nonempty, relative, canonical (no empty, . or .. segment, no
// trailing slash), no backslash, control character or drive letter.
func pathWhy(p string) (Cause, string) {
	switch {
	case p == "":
		return CauseInvalidPath, "the path is empty"
	case strings.HasPrefix(p, "/") || driveRE.MatchString(p):
		return CausePathEscapes, fmt.Sprintf("%q is absolute; paths are relative to the repository root", p)
	case len(p) > MaxPathBytes:
		return CauseInvalidPath, fmt.Sprintf("the path is %d bytes, at most %d", len(p), MaxPathBytes)
	case hasControl(p) || strings.ContainsRune(p, 0) || strings.Contains(p, `\`):
		return CauseInvalidPath, fmt.Sprintf("%q holds a backslash or a control character", p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "..":
			return CausePathEscapes, fmt.Sprintf("%q climbs out of the repository", p)
		case "", ".", ".git":
			return CauseInvalidPath, fmt.Sprintf("%q has an empty, `.` or `.git` segment or a trailing slash; write the canonical repository-relative path", p)
		}
	}
	return "", ""
}

func pin(ctx context.Context, g *gitRun, repoDir, commit string, paths []string, opts ...PinOption) ([]Pinned, []Refusal) {
	var o pinOptions
	for _, f := range opts {
		f(&o)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var refs []Refusal
	switch {
	case len(paths) == 0:
		return nil, []Refusal{pinRefusal("", CauseEmptyArray, "no paths", "pass at least one repository-relative path; a single card is an array of one")}
	case len(paths) > MaxFiles:
		return nil, []Refusal{pinRefusal("", CauseTooManyFiles, fmt.Sprintf("%d paths, the limit is %d", len(paths), MaxFiles),
			fmt.Sprintf("narrow the request to at most %d paths; nothing is chunked for you", MaxFiles))}
	}
	if !commitRE.MatchString(commit) {
		refs = append(refs, pinRefusal("", CauseInvalidCommit, fmt.Sprintf("%q is not a full lower-case object id (40 or 64 hexadecimal digits)", commit),
			"resolve the commit to its full sha first; abbreviations and ref names are refused"))
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if c, w := pathWhy(p); c != "" {
			refs = append(refs, pinRefusal(p, c, w, "name the file by its canonical repository-relative path"))
			continue
		}
		if seen[p] {
			refs = append(refs, pinRefusal(p, CauseDuplicatePath, fmt.Sprintf("%q is named twice", p), "name each path once"))
		}
		seen[p] = true
	}
	identity := o.identity
	if identity != "" {
		if w := identityWhy(identity); w != "" {
			refs = append(refs, pinRefusal("", CauseIdentityInvalid, w, "supply the identity as host/owner/name, for example example.com/owner/repo"))
		}
	}
	if len(refs) > 0 {
		return nil, refs
	}

	if st, err := os.Stat(repoDir); err != nil || !st.IsDir() {
		return nil, []Refusal{pinRefusal("", CauseNotRepository, fmt.Sprintf("%q is not a directory", repoDir), "pass the root directory of a git repository")}
	}

	// 1. the repository: its root, not a subdirectory and not a parent's.
	out, r := g.run(ctx, nil, 4096, "rev-parse", "--is-bare-repository", "--absolute-git-dir", "--show-cdup")
	if r != nil {
		if strings.Contains(r.Found, "not a git repository") {
			r = &Refusal{Operation: OpPin, Cause: CauseNotRepository, Found: fmt.Sprintf("%q is not a git repository", repoDir), Next: "pass the root directory of a git repository"}
		}
		return nil, []Refusal{*r}
	}
	head := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(head) < 2 || (len(head) > 2 && head[2] != "") {
		return nil, []Refusal{pinRefusal("", CauseNotRepository, fmt.Sprintf("%q is inside a repository but is not its root", repoDir), "pass the root directory of the repository")}
	}

	// 2. the identity.
	if identity == "" {
		out, r := g.run(ctx, nil, 4096, "config", "--local", "--get", "remote.origin.url")
		if r != nil {
			if r.Cause == CauseGitFailed && r.Found == "" {
				r = &Refusal{Operation: OpPin, Cause: CauseIdentityMissing, Found: "the repository has no remote.origin.url",
					Next: "supply the identity with WithIdentity (host/owner/name)"}
			}
			return nil, []Refusal{*r}
		}
		id, w := normalizeOrigin(strings.TrimSpace(string(out)))
		if w != "" {
			return nil, []Refusal{pinRefusal("", CauseIdentityMissing, "the origin URL gives no usable identity: "+w,
				"supply the identity with WithIdentity (host/owner/name)")}
		}
		identity = id
	}

	// 3. the commit is a commit.
	out, r = g.run(ctx, []byte(commit+"\n"), 4096, "cat-file", "--batch-check")
	if r != nil {
		return nil, []Refusal{*r}
	}
	f := strings.Fields(string(out))
	switch {
	case len(f) == 2 && f[1] == "missing":
		return nil, []Refusal{pinRefusal("", CauseUnknownCommit, fmt.Sprintf("commit %s is not in the repository", commit), "fetch the commit into the repository first; Pin never fetches")}
	case len(f) != 3 || f[0] != commit:
		return nil, []Refusal{pinRefusal("", CauseGitFailed, fmt.Sprintf("unexpected cat-file answer %q", strings.TrimSpace(string(out))), "report this as a defect")}
	case f[1] != "commit":
		return nil, []Refusal{pinRefusal("", CauseNotCommit, fmt.Sprintf("%s is a %s, not a commit", commit, f[1]), "name a commit by its full sha")}
	}

	// 4. the tree entries of every path, with modes and sizes, in one call.
	args := append([]string{"ls-tree", "-l", "-z", "--full-tree", commit, "--"}, paths...)
	out, r = g.run(ctx, nil, MaxFiles*(MaxPathBytes+128), args...)
	if r != nil {
		return nil, []Refusal{*r}
	}
	entries := map[string]lsEntry{}
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		e, ok := parseLsEntry(rec)
		if !ok {
			return nil, []Refusal{pinRefusal("", CauseGitFailed, fmt.Sprintf("unexpected ls-tree record %q", rec), "report this as a defect")}
		}
		entries[e.path] = e
	}
	total := 0
	var want []string
	wantSeen := map[string]bool{}
	for _, p := range paths {
		e, ok := entries[p]
		switch {
		case !ok:
			refs = append(refs, pinRefusal(p, CauseMissingPath, fmt.Sprintf("no such path at commit %s (a path through a symlinked directory is missing too)", commit), "name a path that exists in the committed tree"))
		case e.mode == "120000":
			refs = append(refs, pinRefusal(p, CauseSymlink, "the path is a symlink at that commit", "pin the file the link points to, by its own path"))
		case e.typ != "blob":
			refs = append(refs, pinRefusal(p, CauseNotBlob, fmt.Sprintf("the path is a %s (mode %s), not a file", e.typ, e.mode), "name a file"))
		case e.size > MaxCardBytes:
			refs = append(refs, pinRefusal(p, CauseBlobTooLarge, fmt.Sprintf("%d bytes, the limit is %d", e.size, MaxCardBytes), "shorten the card"))
		default:
			total += e.size
			if !wantSeen[e.oid] {
				wantSeen[e.oid] = true
				want = append(want, e.oid)
			}
		}
	}
	if total > MaxTotalBytes {
		refs = append(refs, pinRefusal("", CauseTotalTooLarge, fmt.Sprintf("%d bytes across the array, the limit is %d", total, MaxTotalBytes), "narrow the request to fewer or smaller files"))
	}
	if len(refs) > 0 {
		return nil, refs
	}

	// 5. every blob, in one call.
	out, r = g.run(ctx, []byte(strings.Join(want, "\n")+"\n"), MaxTotalBytes+len(want)*256, "cat-file", "--batch")
	if r != nil {
		return nil, []Refusal{*r}
	}
	blobs := map[string][]byte{}
	rest := out
	for _, oid := range want {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return nil, []Refusal{pinRefusal("", CauseGitFailed, "cat-file ended early", "report this as a defect")}
		}
		hdr := strings.Fields(string(rest[:nl]))
		size := -1
		if len(hdr) == 3 && hdr[0] == oid && hdr[1] == "blob" {
			size, _ = strconv.Atoi(hdr[2])
		}
		rest = rest[nl+1:]
		if size < 0 || size > len(rest) {
			return nil, []Refusal{pinRefusal("", CauseGitFailed, fmt.Sprintf("unexpected cat-file header %q", string(bytes.TrimSpace(out[:min(len(out), 80)]))), "report this as a defect")}
		}
		blobs[oid] = rest[:size]
		rest = rest[size:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
	}
	res := make([]Pinned, 0, len(paths))
	for _, p := range paths {
		e := entries[p]
		data := append([]byte(nil), blobs[e.oid]...)
		if len(data) != e.size {
			return nil, []Refusal{pinRefusal(p, CauseGitFailed, fmt.Sprintf("read %d bytes, ls-tree said %d", len(data), e.size), "report this as a defect")}
		}
		h := sha256.Sum256(data)
		res = append(res, Pinned{Repository: identity, Commit: commit, Path: p, ObjectID: e.oid, SHA256: hex.EncodeToString(h[:]), Mode: e.mode, Size: len(data), Data: data})
	}
	return res, nil
}

// Sources turns pinned blobs into card sources for Parse, named by path.
func Sources(pins []Pinned) []Source {
	out := make([]Source, len(pins))
	for i, p := range pins {
		out[i] = Source{Name: p.Path, Data: p.Data}
	}
	return out
}

type lsEntry struct {
	mode, typ, oid, path string
	size                 int
}

// parseLsEntry reads one `git ls-tree -l -z` record: `<mode> <type> <oid> <size>\t<path>`.
func parseLsEntry(rec string) (lsEntry, bool) {
	meta, path, ok := strings.Cut(rec, "\t")
	if !ok {
		return lsEntry{}, false
	}
	f := strings.Fields(meta)
	if len(f) != 4 {
		return lsEntry{}, false
	}
	e := lsEntry{mode: f[0], typ: f[1], oid: f[2], path: path}
	if f[3] != "-" {
		n, err := strconv.Atoi(f[3])
		if err != nil {
			return lsEntry{}, false
		}
		e.size = n
	}
	return e, true
}

// gitRun runs git for Pin: one place that sets the environment, the deadline and
// the output bound, and counts the invocations.
type gitRun struct {
	dir   string
	calls int
}

// limitBuffer is a writer that stops the process when the output passes its bound.
type limitBuffer struct {
	buf    bytes.Buffer
	max    int
	cancel context.CancelFunc
	over   bool
}

func (l *limitBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		l.over = true
		l.cancel()
		return 0, errors.New("output over its bound")
	}
	return l.buf.Write(p)
}

func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	)
}

// run runs one git invocation. A failure is returned as a refusal; for git config
// --get, exit status 1 (no such key) is a refusal with an empty Found.
func (g *gitRun) run(ctx context.Context, stdin []byte, maxOut int, args ...string) ([]byte, *Refusal) {
	g.calls++
	ctx, cancel := context.WithTimeout(ctx, DefaultGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
	cmd.Env = gitEnv()
	cmd.WaitDelay = 2 * time.Second
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out := &limitBuffer{max: maxOut, cancel: cancel}
	var errb bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &limitedErr{b: &errb}
	err := cmd.Run()
	verb := args[0]
	switch {
	case out.over:
		return nil, &Refusal{Operation: OpPin, Cause: CauseTotalTooLarge, Found: fmt.Sprintf("git %s answered more than %d bytes", verb, maxOut), Next: "narrow the request to fewer or smaller files"}
	case err == nil:
		return out.buf.Bytes(), nil
	case errors.Is(err, exec.ErrNotFound):
		return nil, &Refusal{Operation: OpPin, Cause: CauseGitUnavailable, Found: "git is not on PATH", Next: "install git"}
	case ctx.Err() != nil:
		return nil, &Refusal{Operation: OpPin, Cause: CauseTimeout, Found: fmt.Sprintf("git %s did not finish: %v", verb, ctx.Err()), Next: "retry with a longer deadline on the context"}
	}
	var ee *exec.ExitError
	if verb == "config" && errors.As(err, &ee) && ee.ExitCode() == 1 {
		return nil, &Refusal{Operation: OpPin, Cause: CauseGitFailed}
	}
	msg := strings.TrimSpace(errb.String())
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if msg == "" {
		msg = err.Error()
	}
	return nil, &Refusal{Operation: OpPin, Cause: CauseGitFailed, Found: fmt.Sprintf("git %s: %s", verb, msg), Next: "check the repository; Pin reads only committed objects"}
}

// limitedErr keeps the first 4 KiB of stderr and discards the rest.
type limitedErr struct{ b *bytes.Buffer }

func (l *limitedErr) Write(p []byte) (int, error) {
	if room := 4096 - l.b.Len(); room > 0 {
		l.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
