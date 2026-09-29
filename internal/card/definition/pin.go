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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// DefaultGitTimeout is the deadline of every git invocation when the caller's
// context carries none sooner.
const DefaultGitTimeout = 30 * time.Second

// GitCalls is the number of git invocations one pin makes: at most this many,
// whatever the size of the array (the origin lookup is skipped when the caller
// supplies the identity).
const GitCalls = 5

// pinned is one committed blob, pinned: the repository identity, the full commit,
// the repository-relative path, the Git object id, the SHA-256 of the bytes, and
// the bytes, ready for parse.
type pinned struct {
	Repository card.Repository
	Commit     string
	Path       string
	ObjectID   string
	SHA256     string
	Mode       string
	Size       int
	Data       []byte
}

// PinOption adjusts Admissions.
type PinOption func(*pinOptions)

type pinOptions struct{ identity string }

// WithIdentity supplies the repository identity, in place of the normalized
// origin URL. It is the identity to use when the repository has no origin or an
// origin that is a local path.
func WithIdentity(identity string) PinOption {
	return func(o *pinOptions) { o.identity = identity }
}

func pinRefusal(path string, c Cause, found, limit, next string) Refusal {
	return ref(OpPin, c, path, 0, "", found, limit, next)
}

// pathRefusal is the refusal for a repository-relative path that is not one.
func pathRefusal(p string) *Refusal {
	cause, why := card.PathFault(p)
	if cause == "" {
		return nil
	}
	found := card.Value(p)
	if cause == CauseRequired {
		found = ""
	}
	r := pinRefusal(p, cause, found, why, "name the file by its canonical repository-relative path")
	return &r
}

// pinDir reads the committed blobs of an array of repository-relative paths at
// one full commit of the Git repository whose root is repoDir, and pins each:
// repository identity, commit, path, object id, SHA-256, and the bytes. Working
// files, the index and refs are never read. It runs at most GitCalls git
// invocations, each under a deadline, and never fetches, publishes or opens the
// network: git runs with lazy fetching off (GIT_NO_LAZY_FETCH=1) and every
// transport refused, so in a partial clone a blob that was not fetched is the
// named refusal missing-object with the next action, and stays missing.
//
// The repository identity is the normalized origin URL (host/path, no scheme, no
// user, no .git); a repository with no usable origin is refused unless the caller
// supplies an identity with WithIdentity. A refusal about the origin names the rule
// it broke and never quotes the URL, which can carry a credential. Git runs with
// the global and system configuration off and every GIT_ variable of the
// environment removed, so the result does not depend on the machine's git setup.
//
// The commit need not be reachable from any ref: a commit object that exists is
// accepted, and what it names is what is pinned. Refused, with the whole array: a
// commit that is not a full lower-case object id, an unknown commit or one that is
// not a commit, a path that is empty, absolute, climbs out of the repository or is
// not in canonical form, a repeated path, a directory that is not the root of a
// repository (a `<repo>/.git` directory, a subdirectory and a parent's repository
// are refused), and, per path, a path missing at that commit, a symlink, a
// non-blob, a blob over MaxCardBytes, and a blob missing from a partial clone. The
// array is at most MaxFiles paths and MaxTotalBytes bytes.
func pinDir(ctx context.Context, repoDir string, commit string, paths []string, opts ...PinOption) ([]pinned, *Refusals) {
	return pin(ctx, &gitRun{dir: repoDir}, repoDir, commit, paths, opts...)
}

func pin(ctx context.Context, g *gitRun, repoDir, commit string, paths []string, opts ...PinOption) ([]pinned, *Refusals) {
	var o pinOptions
	for _, f := range opts {
		f(&o)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c := &card.Collector{}
	switch {
	case len(paths) == 0:
		return nil, just(pinRefusal("", CauseEmptyArray, "no paths", "at least 1", "pass at least one repository-relative path; a single card is an array of one"))
	case len(paths) > MaxFiles:
		return nil, just(pinRefusal("", CauseTooMany, plural(len(paths), "path"), fmt.Sprintf("%d paths", MaxFiles),
			fmt.Sprintf("narrow the request to at most %d paths; nothing is chunked for you", MaxFiles)))
	}
	if !card.ValidObjectID(commit) {
		c.Add(pinRefusal("", CauseInvalidCommit, card.Value(commit), "a full lower-case object id (40 or 64 hexadecimal digits)",
			"resolve the commit to its full sha first; abbreviations and ref names are refused"))
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if r := pathRefusal(p); r != nil {
			c.Add(*r)
			continue
		}
		if seen[p] {
			c.Add(pinRefusal(p, CauseDuplicatePath, card.Value(p)+" is named twice", "each path once", "name each path once"))
		}
		seen[p] = true
	}
	identity := card.Repository(o.identity)
	if o.identity != "" {
		if why := card.RepositoryWhy(o.identity); why != "" {
			c.Add(pinRefusal("", CauseInvalidRepository, "the supplied identity "+why+" (not quoted)", "host[:port]/owner/name",
				"supply the identity as host/owner/name, for example example.com/owner/repo"))
		}
	}
	if err := c.Err(); err != nil {
		return nil, err
	}

	dir, derr := filepath.EvalSymlinks(repoDir)
	if st, err := os.Stat(repoDir); err != nil || !st.IsDir() || derr != nil {
		return nil, just(pinRefusal("", CauseNotRepository, "the repository directory is not a directory", "the root directory of a git repository", "pass the root directory of a git repository"))
	}

	// 1. the repository: its root, not a subdirectory, not its own .git directory
	// and not a parent's repository.
	out, gf := g.run(ctx, nil, 4096, "rev-parse", "--is-bare-repository", "--absolute-git-dir", "--is-inside-git-dir", "--show-cdup")
	if gf != nil {
		if gf.notRepository {
			return nil, just(pinRefusal("", CauseNotRepository, "the directory is not a git repository", "the root directory of a git repository", "pass the root directory of a git repository"))
		}
		return nil, just(gf.refusal())
	}
	head := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(head) < 3 {
		return nil, just(pinRefusal("", CauseGitFailed, "git rev-parse answered unexpectedly", "a repository root", "report this as a defect"))
	}
	gitDir, _ := filepath.EvalSymlinks(head[1])
	isRoot := false
	switch head[0] {
	case "true": // a bare repository is its own root
		isRoot = gitDir == dir
	case "false": // a work tree's root has no way up to a parent and is not inside its own .git
		isRoot = head[2] == "false" && (len(head) == 3 || head[3] == "")
	}
	if !isRoot {
		return nil, just(pinRefusal("", CauseNotRepository, "the directory is inside a repository but is not its root", "the root of the repository, not a subdirectory and not its .git directory",
			"pass the root directory of the repository"))
	}

	// 2. the identity.
	if identity == "" {
		out, gf := g.run(ctx, nil, 4096, "config", "--local", "--get", "remote.origin.url")
		switch {
		case gf != nil && gf.noValue:
			return nil, just(pinRefusal("", CauseIdentityMissing, "the repository has no remote.origin.url", "an origin URL or a supplied identity",
				"supply the identity with WithIdentity (host/owner/name)"))
		case gf != nil:
			return nil, just(gf.refusal())
		}
		id, rule := card.NormalizeOrigin(strings.TrimSpace(string(out)))
		if rule != "" {
			return nil, just(pinRefusal("", CauseIdentityMissing, "the origin URL "+rule+" (not quoted)", "an origin that gives host[:port]/owner/name",
				"supply the identity with WithIdentity (host/owner/name)"))
		}
		identity = id
	}

	// 3. the commit is a commit.
	out, gf = g.run(ctx, []byte(commit+"\n"), 4096, "cat-file", "--batch-check")
	if gf != nil {
		return nil, just(gf.refusal())
	}
	f := strings.Fields(string(out))
	switch {
	case len(f) == 2 && f[1] == "missing":
		return nil, just(pinRefusal("", CauseUnknownCommit, "commit "+commit+" is not in the repository", "a commit object in the repository", "fetch the commit into the repository first; pin never fetches"))
	case len(f) != 3 || f[0] != commit:
		return nil, just(pinRefusal("", CauseGitFailed, "git cat-file answered unexpectedly", "a commit", "report this as a defect"))
	case f[1] != "commit":
		return nil, just(pinRefusal("", CauseNotCommit, commit+" is a "+card.Value(f[1]), "a commit", "name a commit by its full sha"))
	}

	// 4. the tree entries of every path, with modes and sizes, in one call.
	args := append([]string{"ls-tree", "-l", "-z", "--full-tree", commit, "--"}, paths...)
	out, gf = g.run(ctx, nil, MaxFiles*(MaxPathBytes+128), args...)
	if gf != nil {
		return nil, just(gf.refusal())
	}
	entries := map[string]lsEntry{}
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		e, ok := parseLsEntry(rec)
		if !ok {
			return nil, just(pinRefusal("", CauseGitFailed, "git ls-tree answered a record it does not know", "an ls-tree record", "report this as a defect"))
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
			c.Add(pinRefusal(p, CauseMissingPath, "no such path at commit "+commit, "a path in the committed tree (a path through a symlinked directory is missing too)", "name a path that exists in the committed tree"))
		case e.mode == "120000":
			c.Add(pinRefusal(p, CauseSymlink, "the path is a symlink at that commit", "a file, not a link", "pin the file the link points to, by its own path"))
		case e.typ != "blob":
			c.Add(pinRefusal(p, CauseNotBlob, fmt.Sprintf("a %s (mode %s)", card.Value(e.typ), card.Value(e.mode)), "a file", "name a file"))
		case e.bad:
			c.Add(pinRefusal(p, CauseMissingObject, "the blob "+e.oid+" is not in this clone", "a blob present in the repository",
				"fetch the blob into the clone first (git fetch, or a full clone); pin never fetches"))
		case e.size > MaxCardBytes:
			c.Add(pinRefusal(p, CauseTooLarge, fmt.Sprintf("%d bytes", e.size), fmt.Sprintf("%d bytes", MaxCardBytes), "shorten the card"))
		default:
			total += e.size
			if !wantSeen[e.oid] {
				wantSeen[e.oid] = true
				want = append(want, e.oid)
			}
		}
	}
	if total > MaxTotalBytes {
		c.Add(pinRefusal("", CauseTooLarge, fmt.Sprintf("%d bytes across the array", total), fmt.Sprintf("%d bytes", MaxTotalBytes), "narrow the request to fewer or smaller files"))
	}
	if err := c.Err(); err != nil {
		return nil, err
	}

	// 5. every blob, in one call.
	out, gf = g.run(ctx, []byte(strings.Join(want, "\n")+"\n"), MaxTotalBytes+len(want)*256, "cat-file", "--batch")
	if gf != nil {
		return nil, just(gf.refusal())
	}
	blobs := map[string][]byte{}
	rest := out
	for _, oid := range want {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return nil, just(pinRefusal("", CauseGitFailed, "git cat-file ended early", "one header per blob", "report this as a defect"))
		}
		hdr := strings.Fields(string(rest[:nl]))
		if len(hdr) == 2 && hdr[0] == oid && hdr[1] == "missing" {
			for _, p := range paths {
				if entries[p].oid == oid {
					c.Add(pinRefusal(p, CauseMissingObject, "the blob "+oid+" is not in this clone", "a blob present in the repository",
						"fetch the blob into the clone first (git fetch, or a full clone); pin never fetches"))
				}
			}
			rest = rest[nl+1:]
			continue
		}
		size := -1
		if len(hdr) == 3 && hdr[0] == oid && hdr[1] == "blob" {
			size, _ = strconv.Atoi(hdr[2])
		}
		rest = rest[nl+1:]
		if size < 0 || size > len(rest) {
			return nil, just(pinRefusal("", CauseGitFailed, "git cat-file answered a header it does not know", "a blob header", "report this as a defect"))
		}
		blobs[oid] = rest[:size]
		rest = rest[size:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
	}
	if err := c.Err(); err != nil {
		return nil, err
	}
	res := make([]pinned, 0, len(paths))
	for _, p := range paths {
		e := entries[p]
		data := append([]byte(nil), blobs[e.oid]...)
		if len(data) != e.size {
			return nil, just(pinRefusal(p, CauseGitFailed, fmt.Sprintf("read %d bytes, ls-tree said %d", len(data), e.size), "the size ls-tree named", "report this as a defect"))
		}
		h := sha256.Sum256(data)
		res = append(res, pinned{Repository: identity, Commit: commit, Path: p, ObjectID: e.oid, SHA256: hex.EncodeToString(h[:]), Mode: e.mode, Size: len(data), Data: data})
	}
	return res, nil
}

// sources turns pinned blobs into card sources for parse, named by path.
func sources(pins []pinned) []Source {
	out := make([]Source, len(pins))
	for i, p := range pins {
		out[i] = Source{Name: p.Path, Data: p.Data}
	}
	return out
}

type lsEntry struct {
	mode, typ, oid, path string
	size                 int
	bad                  bool // ls-tree could not size the blob: it is not in the repository
}

// parseLsEntry reads one `git ls-tree -l -z` record: `<mode> <type> <oid> <size>\t<path>`.
// A blob that is not in the repository (a partial clone, with lazy fetching off)
// has the size BAD.
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
	switch f[3] {
	case "-":
	case "BAD":
		e.bad = true
	default:
		n, err := strconv.Atoi(f[3])
		if err != nil {
			return lsEntry{}, false
		}
		e.size = n
	}
	return e, true
}

// gitRun runs git for pin: one place that sets the environment, the deadline and
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

// gitEnv is the environment git runs in: the caller's, less every GIT_ variable,
// plus the settings that make the result the repository's own: no global or
// system configuration, no prompt, literal pathspecs, no replace refs, no
// optional locks, and no lazy fetch and no transport, so nothing is ever fetched.
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
		"GIT_NO_LAZY_FETCH=1",
		"GIT_ALLOW_PROTOCOL=none",
		"LC_ALL=C",
	)
}

// gitFailure is what a failed git call is known by: never git's own text.
type gitFailure struct {
	verb          string
	cause         Cause
	status        int
	found         string
	notRepository bool
	noValue       bool
}

func (f *gitFailure) refusal() Refusal {
	next := "check the repository; pin reads only committed objects"
	switch f.cause {
	case CauseGitUnavailable:
		next = "install git"
	case CauseTimeout:
		next = "retry with a longer deadline on the context"
	case CauseTooLarge:
		next = "narrow the request to fewer or smaller files"
	}
	return pinRefusal("", f.cause, f.found, "a git call that finishes", next)
}

// run runs one git invocation. A failure is returned as a gitFailure that names the
// verb and the exit status and never copies git's error text; for `git config
// --get`, exit status 1 (no such key) is noValue.
func (g *gitRun) run(ctx context.Context, stdin []byte, maxOut int, args ...string) ([]byte, *gitFailure) {
	g.calls++
	ctx, cancel := context.WithTimeout(ctx, DefaultGitTimeout)
	defer cancel()
	full := append([]string{"-c", "protocol.allow=never", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
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
		return nil, &gitFailure{verb: verb, cause: CauseTooLarge, found: fmt.Sprintf("git %s answered more than %d bytes", verb, maxOut)}
	case err == nil:
		return out.buf.Bytes(), nil
	case errors.Is(err, exec.ErrNotFound):
		return nil, &gitFailure{verb: verb, cause: CauseGitUnavailable, found: "git is not on PATH"}
	case ctx.Err() != nil:
		return nil, &gitFailure{verb: verb, cause: CauseTimeout, found: fmt.Sprintf("git %s did not finish within its deadline", verb)}
	}
	var ee *exec.ExitError
	status := -1
	if errors.As(err, &ee) {
		status = ee.ExitCode()
	}
	if verb == "config" && status == 1 {
		return nil, &gitFailure{verb: verb, cause: CauseGitFailed, status: status, noValue: true}
	}
	f := &gitFailure{verb: verb, cause: CauseGitFailed, status: status, found: fmt.Sprintf("git %s failed with exit status %d", verb, status)}
	// git's words are read, never copied: this only classifies the one failure the
	// caller has a remedy for.
	f.notRepository = strings.Contains(errb.String(), "not a git repository")
	return nil, f
}

// limitedErr keeps the first 4 KiB of stderr and discards the rest.
type limitedErr struct{ b *bytes.Buffer }

func (l *limitedErr) Write(p []byte) (int, error) {
	if room := 4096 - l.b.Len(); room > 0 {
		l.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
