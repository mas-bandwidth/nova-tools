package ci

// run.go is our own CI (nova-tools #3597, #3349; Glenn 2026-09-24 "we can
// do our own ci"). A head is requested once into a pool, a bench claims it
// with a lease, runs the repo's declared checks in a scratch clone at the sha
// and writes one receipt per check; the last receipt writes the summary word
// ci = green|red on the request record and on the PR record that names the
// head. Every Redis step is one call into the nova_sprint library
// (internal/nsprint/fn/lua/ci_run.lua); the lander reads the record, never
// GitHub.
//
// Runner-only (#3349): nothing here writes bench legs. A check is an argv,
// never a shell line: the declared command is split on spaces and run as is,
// in the clone, so what ran is exactly what the record says.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/ci_run.lua.
const (
	FunctionRequest = "ns_ci_request"
	FunctionClaim   = "ns_ci_claim"
	FunctionReceipt = "ns_ci_receipt"
	FunctionRelease = "ns_ci_release"
)

// Summary words on the request record's ci field.
const (
	SummaryPending = "pending"
	SummaryGreen   = "green"
	SummaryRed     = "red"
)

// PoolKey is the ZSET the benches pull requests from.
const PoolKey = "ci:pool"

// Check is one declared check: a name and the argv that runs it in the clone.
type Check struct {
	Name string
	Argv string
}

// Defaults is the declared set of a repo when cfg:ci:<repo> names none. The
// nova-tools set is the ci workflow's build, vet and test steps, the tests in
// two package groups, and the class rules in internal/ci; the rowan-tools set
// is its shell workflow (shellcheck ratchet, then bats, both in tests/shell-ci
// of that repository).
var Defaults = map[string][]Check{
	"nova-tools": {
		{Name: "go-build", Argv: "go build ./..."},
		{Name: "go-vet", Argv: "go vet ./..."},
		{Name: "go-test-cmd", Argv: "go test -count=1 ./cmd/..."},
		{Name: "go-test-internal", Argv: "go test -count=1 ./internal/..."},
		{Name: "internal-ci", Argv: "go test -count=1 ./internal/ci/..."},
	},
	"rowan-tools": {
		{Name: "shellcheck", Argv: "shellcheck -S warning bin"},
		{Name: "bats", Argv: "bats tests"},
	},
}

// reservedSuffixes are the ci:<repo>:<sha>:<suffix> keys the ci card family
// owns (ci.lua, civerdict); a check may not take one of their names.
var reservedSuffixes = map[string]bool{"gids": true, "waiting": true, "runners": true, "tip": true}

var checkRx = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,40}$`)

// RecordKey is the request record of one head.
func RecordKey(repo, sha string) string { return "ci:" + repo + ":" + sha }

// ReceiptKey is the receipt of one check on one head.
func ReceiptKey(repo, sha, check string) string { return RecordKey(repo, sha) + ":" + check }

// ConfigKey is where a repo declares its checks.
func ConfigKey(repo string) string { return "cfg:ci:" + repo }

// PRKey is the PR record the summary is copied to when its head is the sha:
// the one PR record key, pr:<name>:<n> (internal/nsprint/prkey).
func PRKey(repo string, pr int) string { return prkey.Key(repo, pr) }

// CheckName validates a check name against the key grammar and the reserved
// suffixes.
func CheckName(name string) error {
	if !checkRx.MatchString(name) {
		return fmt.Errorf("check %q is not [a-z0-9][a-z0-9._-]{0,40}", name)
	}
	if reservedSuffixes[name] {
		return fmt.Errorf("check %q is a reserved record suffix", name)
	}
	return nil
}

// RequestRequest asks for the checks of one head. Checks empty means every
// declared check; each named one must be declared. PR is optional; URL is the
// clone url the runner uses (empty: the runner's default for the repo).
type RequestRequest struct {
	Repo   string
	SHA    string
	PR     int
	URL    string
	Checks []string
}

// Request writes ci:<repo>:<sha> and adds it to the pool, once. EXISTS on a
// second request for the head, REFUSED when a check is not declared.
func Request(ctx context.Context, st *store.Store, req RequestRequest) (Result, error) {
	if !repoRx.MatchString(req.Repo) {
		return Result{}, fmt.Errorf("repo %q is not a repository name", req.Repo)
	}
	if !shaRx.MatchString(req.SHA) {
		return Result{}, fmt.Errorf("sha %q is not a full 40-hex sha", req.SHA)
	}
	for _, c := range req.Checks {
		if err := CheckName(c); err != nil {
			return Result{}, err
		}
	}
	pr := ""
	if req.PR > 0 {
		pr = strconv.Itoa(req.PR)
	}
	args := []string{req.Repo, req.SHA, pr, req.URL, strings.Join(req.Checks, ",")}
	for _, c := range Defaults[req.Repo] {
		args = append(args, c.Name, c.Argv)
	}
	return call(ctx, st, FunctionRequest, args...)
}

// Claimed is one request a bench holds under a lease.
type Claimed struct {
	Repo    string
	SHA     string
	PR      string
	URL     string
	Attempt int
	Token   string
	Checks  []Check
}

// Claim takes the oldest claimable request for the bench; ok is false when
// the pool has none. The token fences every later write of this attempt.
func Claim(ctx context.Context, st *store.Store, bench string, lease time.Duration) (Claimed, bool, error) {
	if bench == "" {
		return Claimed{}, false, errors.New("claim needs a bench name")
	}
	token, err := newToken()
	if err != nil {
		return Claimed{}, false, err
	}
	raw, err := st.Client().FCall(ctx, FunctionClaim, nil, bench, token, strconv.FormatInt(lease.Milliseconds(), 10)).Result()
	if err != nil {
		return Claimed{}, false, fmt.Errorf("%s: %w", FunctionClaim, err)
	}
	parts, ok := raw.([]any)
	if !ok || len(parts) == 0 {
		return Claimed{}, false, fmt.Errorf("%s: reply %T", FunctionClaim, raw)
	}
	if fmt.Sprint(parts[0]) != "CLAIMED" {
		return Claimed{}, false, nil
	}
	fields := map[string]string{}
	for i := 1; i+1 < len(parts); i += 2 {
		fields[fmt.Sprint(parts[i])] = fmt.Sprint(parts[i+1])
	}
	c := Claimed{Repo: fields["repo"], SHA: fields["sha"], PR: fields["pr"], URL: fields["url"], Token: fields["token"]}
	c.Attempt, _ = strconv.Atoi(fields["attempt"])
	for _, name := range strings.Split(fields["checks"], ",") {
		if name != "" {
			c.Checks = append(c.Checks, Check{Name: name, Argv: fields["cmd:"+name]})
		}
	}
	return c, true, nil
}

// ReceiptRecord is what one finished check writes.
type ReceiptRecord struct {
	Repo, SHA, Check, Token string
	RC                      int
	WallMS                  int64
	Log                     string
	Bench                   string
	Lease                   time.Duration
}

// ReceiptResult is one receipt's reply: RECEIPT with the summary so far
// (pending until the last check, then green or red) and done/total; FENCED,
// NOTFOUND or REFUSED write nothing.
type ReceiptResult struct {
	Status  string
	Summary string
	Done    string
}

// WriteReceipt writes one check's receipt and, on the last one, the summary.
// FENCED when the token is not the live attempt's.
func WriteReceipt(ctx context.Context, st *store.Store, r ReceiptRecord) (ReceiptResult, error) {
	words, err := st.Client().FCall(ctx, FunctionReceipt, nil, r.Repo, r.SHA, r.Check, r.Token, strconv.Itoa(r.RC),
		strconv.FormatInt(r.WallMS, 10), r.Log, r.Bench, strconv.FormatInt(r.Lease.Milliseconds(), 10)).StringSlice()
	if err != nil {
		return ReceiptResult{}, fmt.Errorf("%s: %w", FunctionReceipt, err)
	}
	if len(words) == 0 {
		return ReceiptResult{}, fmt.Errorf("%s: empty reply", FunctionReceipt)
	}
	res := ReceiptResult{Status: words[0]}
	if len(words) > 1 {
		res.Summary = words[1]
	}
	if len(words) > 2 {
		res.Done = words[2]
	}
	return res, nil
}

// Release hands a claimed request back to the pool with no evidence written:
// the bench could not run the checks (its clone failed), which says nothing
// about the head.
func Release(ctx context.Context, st *store.Store, repo, sha, token, reason string) (Result, error) {
	return call(ctx, st, FunctionRelease, repo, sha, token, reason)
}

// RunOptions is one `ci run --bench <b>` pass.
type RunOptions struct {
	Bench       string
	Scratch     string // the clone lives under here for the run, then is removed
	ResultsRoot string // logs land at <ResultsRoot>/ci/<repo>/<sha>/<check>.log
	MirrorRoot  string // <MirrorRoot>/<repo>.git is the clone's --reference when present
	URLFor      func(repo string) string
	Lease       time.Duration // claim lease; each receipt renews it
	Timeout     time.Duration // per check
	Out         io.Writer     // one line per step; nil discards
	Git         string        // git binary; empty: git
}

// RunResult is what one pass did.
type RunResult struct {
	Claimed bool
	Repo    string
	SHA     string
	Attempt int
	Summary string // green, red; empty when nothing was claimed or the run was released
	Blocked string // the release reason when the clone failed
	StoreMS int64  // ms spent in the Redis calls (claim, receipts, release)
	Checks  []CheckResult
}

// CheckResult is one check as it ran.
type CheckResult struct {
	Name   string
	RC     int
	WallMS int64
	Log    string
}

// ErrBlocked is Run's answer when the clone failed and the request was
// released; the reason is on the record's blocked field.
var ErrBlocked = errors.New("ci run blocked")

// Run claims one request, runs its checks and writes the receipts. It returns
// Claimed false when the pool has nothing claimable, and ErrBlocked (with the
// request released) when the clone at the sha failed. A check that fails
// does not stop the rest: every check gets a receipt, so the red one is named.
func Run(ctx context.Context, st *store.Store, opt RunOptions) (RunResult, error) {
	var res RunResult
	if opt.Bench == "" {
		return res, errors.New("run needs a bench name")
	}
	if !filepath.IsAbs(opt.ResultsRoot) {
		return res, errors.New("run needs an absolute results root")
	}
	if opt.Scratch == "" {
		opt.Scratch = filepath.Join(os.TempDir(), "nova-ci")
	}
	if opt.Lease <= 0 {
		opt.Lease = 30 * time.Minute
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 20 * time.Minute
	}
	if opt.Git == "" {
		opt.Git = "git"
	}
	out := opt.Out
	if out == nil {
		out = io.Discard
	}
	began := time.Now()
	c, ok, err := Claim(ctx, st, opt.Bench, opt.Lease)
	res.StoreMS += time.Since(began).Milliseconds()
	if err != nil || !ok {
		return res, err
	}
	res.Claimed, res.Repo, res.SHA, res.Attempt = true, c.Repo, c.SHA, c.Attempt
	fmt.Fprintf(out, "CLAIMED %s@%s attempt=%d checks=%d\n", c.Repo, c.SHA[:8], c.Attempt, len(c.Checks))

	url := c.URL
	if url == "" && opt.URLFor != nil {
		url = opt.URLFor(c.Repo)
	}
	dir := filepath.Join(opt.Scratch, fmt.Sprintf("%s-%s-a%d", c.Repo, c.SHA[:8], c.Attempt))
	logDir := filepath.Join(opt.ResultsRoot, "ci", c.Repo, c.SHA)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return res, err
	}
	// The clone is disposable and lives only under the scratch root; the
	// remove is the guarded one, so a mis-set root can never take a tree it
	// does not own (internal/safepath).
	if err := os.MkdirAll(opt.Scratch, 0o755); err != nil {
		return res, err
	}
	if err := safepath.RemoveUnder(opt.Scratch, dir); err != nil {
		return res, err
	}
	defer func() { _ = safepath.RemoveUnder(opt.Scratch, dir) }()
	if cloneLog, err := clone(ctx, opt, url, c.Repo, c.SHA, dir); err != nil {
		reason := "clone: " + err.Error()
		_ = os.WriteFile(filepath.Join(logDir, "clone.log"), cloneLog, 0o644)
		t := time.Now()
		r, rerr := Release(ctx, st, c.Repo, c.SHA, c.Token, reason)
		res.StoreMS += time.Since(t).Milliseconds()
		if rerr != nil {
			return res, rerr
		}
		res.Blocked = reason
		fmt.Fprintf(out, "BLOCKED %s@%s %s %s\n", c.Repo, c.SHA[:8], r.Status, reason)
		return res, ErrBlocked
	}

	for _, ch := range c.Checks {
		cr := runCheck(ctx, opt, dir, logDir, ch)
		res.Checks = append(res.Checks, cr)
		t := time.Now()
		r, err := WriteReceipt(ctx, st, ReceiptRecord{Repo: c.Repo, SHA: c.SHA, Check: ch.Name, Token: c.Token,
			RC: cr.RC, WallMS: cr.WallMS, Log: cr.Log, Bench: opt.Bench, Lease: opt.Lease})
		res.StoreMS += time.Since(t).Milliseconds()
		if err != nil {
			return res, err
		}
		if r.Status != "RECEIPT" {
			return res, fmt.Errorf("receipt %s@%s %s: %s %s", c.Repo, c.SHA[:8], ch.Name, r.Status, r.Summary)
		}
		fmt.Fprintf(out, "CHECK %s rc=%d wall_ms=%d log=%s\n", ch.Name, cr.RC, cr.WallMS, cr.Log)
		res.Summary = r.Summary
	}
	fmt.Fprintf(out, "CI %s@%s %s bench=%s attempt=%d store_ms=%d\n", c.Repo, c.SHA[:8], res.Summary, opt.Bench, c.Attempt, res.StoreMS)
	return res, nil
}

// clone makes the scratch clone at the sha: a shallow single-branch clone
// borrowing the bench mirror's objects when the mirror is there, then a
// fetch of the exact sha and a detached checkout of it.
func clone(ctx context.Context, opt RunOptions, url, repo, sha, dir string) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("no clone url for %s (request --url or cfg)", repo)
	}
	args := []string{"clone", "-q", "--no-checkout", "--depth", "50", "--single-branch"}
	if opt.MirrorRoot != "" {
		if mirror := filepath.Join(opt.MirrorRoot, repo+".git"); isDir(mirror) {
			args = append(args, "--reference-if-able", mirror)
		}
	}
	args = append(args, url, dir)
	var log []byte
	steps := [][]string{
		args,
		{"-C", dir, "fetch", "-q", "--depth", "50", "origin", sha},
		{"-C", dir, "checkout", "-q", "--detach", sha},
	}
	for _, s := range steps {
		cmd := exec.CommandContext(ctx, opt.Git, s...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		b, err := cmd.CombinedOutput()
		log = append(log, b...)
		if err != nil {
			return log, fmt.Errorf("git %s: %v: %s", s[len(s)-1], err, strings.TrimSpace(string(b)))
		}
	}
	return log, nil
}

// maxLog caps one check's kept log; the tail is what names the failure.
const maxLog = 4 << 20

// runCheck runs one declared argv in the clone with the log at
// <logDir>/<name>.log. An argv that cannot start is rc 127 with the error in
// the log; a timeout is rc 124.
func runCheck(ctx context.Context, opt RunOptions, dir, logDir string, ch Check) CheckResult {
	logPath := filepath.Join(logDir, ch.Name+".log")
	cr := CheckResult{Name: ch.Name, Log: logPath}
	argv := strings.Fields(ch.Argv)
	began := time.Now()
	var body []byte
	if len(argv) == 0 {
		cr.RC = 127
		body = []byte("ci: check " + ch.Name + " has an empty argv\n")
	} else {
		cctx, cancel := context.WithTimeout(ctx, opt.Timeout)
		cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "CI=1")
		b, err := cmd.CombinedOutput()
		cancel()
		body = b
		switch {
		case err == nil:
			cr.RC = 0
		case errors.Is(cctx.Err(), context.DeadlineExceeded):
			cr.RC = 124
			body = append(body, []byte("\nci: check "+ch.Name+" timed out after "+opt.Timeout.String()+"\n")...)
		default:
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() > 0 {
				cr.RC = ee.ExitCode()
			} else {
				cr.RC = 127
				body = append(body, []byte("\nci: "+err.Error()+"\n")...)
			}
		}
	}
	cr.WallMS = time.Since(began).Milliseconds()
	if len(body) > maxLog {
		body = append([]byte("ci: log truncated to its last 4 MiB\n"), body[len(body)-maxLog:]...)
	}
	if err := os.WriteFile(logPath, body, 0o644); err != nil {
		cr.Log = "unwritable:" + logPath
	}
	return cr
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Row is one check as `ci status --repo --sha` prints it.
type Row struct {
	Check  string
	State  string // pending, green, red
	RC     string
	WallMS string
	Log    string
	Bench  string
}

// Rows is the request record and its receipts, read in two pipelined round
// trips (the record names the checks; then every receipt at once).
type Rows struct {
	Key    string
	Found  bool
	Fields map[string]string
	Rows   []Row
}

// ReadRows reads one head's request record and receipts.
func ReadRows(ctx context.Context, st *store.Store, repo, sha string) (Rows, error) {
	key := RecordKey(repo, sha)
	client := st.Client()
	fields, err := client.HGetAll(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Rows{}, fmt.Errorf("HGETALL %s: %w", key, err)
	}
	rows := Rows{Key: key, Found: len(fields) > 0, Fields: fields}
	if !rows.Found {
		return rows, nil
	}
	var names []string
	for _, n := range strings.Split(fields["checks"], ",") {
		if n != "" {
			names = append(names, n)
		}
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(names))
	for i, n := range names {
		cmds[i] = pipe.HGetAll(ctx, ReceiptKey(repo, sha, n))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Rows{}, fmt.Errorf("receipts %s: %w", key, err)
	}
	for i, n := range names {
		r := cmds[i].Val()
		row := Row{Check: n, State: SummaryPending}
		if rc, ok := r["rc"]; ok {
			row.RC, row.WallMS, row.Log, row.Bench = rc, r["wall_ms"], r["log"], r["bench"]
			row.State = SummaryRed
			if rc == "0" {
				row.State = SummaryGreen
			}
		}
		rows.Rows = append(rows.Rows, row)
	}
	return rows, nil
}

// Summary is the record's ci word, or "" when there is no record.
func (r Rows) Summary() string {
	if !r.Found {
		return ""
	}
	return r.Fields["ci"]
}

// WriteRows prints the record line and one row per check. Exit 0 with a
// record (whatever its word), 5 MISSING without one.
func WriteRows(w io.Writer, r Rows) int {
	if !r.Found {
		fmt.Fprintf(w, "%s MISSING\n", r.Key)
		return ExitMissing
	}
	f := r.Fields
	fmt.Fprintf(w, "%s %s bench=%s attempt=%s pr=%s", r.Key, f["ci"], f["bench"], f["attempt"], f["pr"])
	if f["blocked"] != "" {
		fmt.Fprintf(w, " blocked=%q", f["blocked"])
	}
	fmt.Fprintln(w)
	for _, row := range r.Rows {
		if row.State == SummaryPending {
			fmt.Fprintf(w, "  %s pending\n", row.Check)
			continue
		}
		fmt.Fprintf(w, "  %s %s rc=%s wall_ms=%s bench=%s log=%s\n", row.Check, row.State, row.RC, row.WallMS, row.Bench, row.Log)
	}
	return ExitOK
}
