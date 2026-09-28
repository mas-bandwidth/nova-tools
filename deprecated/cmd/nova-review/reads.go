package main

import (
	"context"
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

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The reads ledger (nova-tools #2063): who must read which PR at which exact
// head, and what each reader has said at THAT head. Before this it lived in
// bus prose and the coordinator's memory; the ledger ingests the two places a
// verdict is actually said — GitHub reviews and bus notes carrying a typed
// line — and derives the required reader roles from the paths touched plus
// typed ASK lines. A push that moves a head marks its reads stale and names
// who must re-read what delta. The verb is a survey: it writes nothing.

// The three reader roles, in the one order every listing prints them.
const (
	roleContract = "contract"
	roleCode     = "code"
	roleSecurity = "security"
)

var readRoleOrder = []string{roleContract, roleCode, roleSecurity}

// securityPathMarkers name the security-sensitive paths: a read of a path that
// holds one of them is a security read. Judgment, recorded here: the markers
// are the words this repository's own risk question uses for its highest tier.
var securityPathMarkers = []string{"secret", "auth", "acl", "permission", "sandbox", "crypto", "credential", "token"}

// prReview is one GitHub review as the ledger ingests it, whatever delivered it.
type prReview struct {
	Author      string
	State       string
	CommitID    string
	Body        string
	SubmittedAt string
}

// viewPRReviews is the seam through which `reads` asks GitHub for a PR's
// author and reviews. The real implementation is ghPRReviews, below; tests
// inject a fake or pass --reviews snapshot files so no test reaches the
// network (the viewPRIntent pattern).
var viewPRReviews = ghPRReviews

// ghPRReviews is the real viewPRReviews: `gh pr view <n> --json author,reviews`.
// Any failure is an error naming what failed, never an empty review list: a
// GitHub read that did not happen is not "no reviews", and a ledger built on
// the bus alone could report ready past an existing GitHub hold and could not
// tell the author's own bus read from a friend's (Stella's hold, #2863).
func ghPRReviews(ctx context.Context, pr int, hostRepo string) (string, []prReview, error) {
	c := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(pr), "--repo", hostRepo, "--json", "author,reviews")
	var stderr strings.Builder
	c.Stderr = &limitedWriter{w: &stderr, n: 512}
	// Backstop: once gh has exited, a descendant still holding stderr must
	// not hold Wait open.
	c.WaitDelay = 5 * time.Second
	stdout, err := c.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	if err := c.Start(); err != nil {
		return "", nil, fmt.Errorf("gh did not start: %w", err)
	}
	const maxRead = 4 << 20
	b, rerr := io.ReadAll(io.LimitReader(stdout, maxRead+1))
	if rerr != nil || len(b) > maxRead {
		// We stopped reading: a gh with more to say would block on the full
		// pipe while Wait blocks on it, until the caller's timeout (Stella's
		// hold, #2863). Close our end so any writer (gh, or a child it left
		// holding the pipe and stderr) gets EPIPE, and kill gh itself, so the
		// refusal is prompt.
		_ = stdout.Close()
		_ = c.Process.Kill()
	}
	werr := c.Wait()
	switch {
	case rerr != nil:
		return "", nil, fmt.Errorf("reading gh output: %w", rerr)
	case len(b) > maxRead:
		return "", nil, fmt.Errorf("gh output over %d bytes", maxRead)
	case werr != nil:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", nil, fmt.Errorf("gh: %w", werr)
		}
		return "", nil, fmt.Errorf("gh: %w: %s", werr, msg)
	}
	var v reviewsFile
	if err := json.Unmarshal(b, &v); err != nil {
		return "", nil, fmt.Errorf("gh printed JSON that is not author,reviews: %w", err)
	}
	if v.Author.Login == "" {
		return "", nil, errors.New("gh printed no PR author")
	}
	revs := make([]prReview, 0, len(v.Reviews))
	for _, r := range v.Reviews {
		revs = append(revs, prReview{Author: r.Author.Login, State: r.State, CommitID: r.CommitID, Body: r.Body, SubmittedAt: r.SubmittedAt})
	}
	return v.Author.Login, revs, nil
}

// limitedWriter keeps the first n bytes written to it and drops the rest, so a
// chatty gh cannot grow a refusal reason without bound.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := len(p)
		if k > l.n {
			k = l.n
		}
		if _, err := l.w.Write(p[:k]); err != nil {
			return 0, err
		}
		l.n -= k
	}
	return len(p), nil
}

// reviewsFile is the shape `gh pr view <n> --json author,reviews` prints, and
// the shape a --reviews snapshot file holds.
type reviewsFile struct {
	Author  ghLogin    `json:"author"`
	Reviews []ghReview `json:"reviews"`
}

type ghLogin struct {
	Login string `json:"login"`
}

type ghReview struct {
	Author      ghLogin `json:"author"`
	State       string  `json:"state"`
	CommitID    string  `json:"commit_id"`
	Body        string  `json:"body"`
	SubmittedAt string  `json:"submitted_at"`
}

// reviewsFlags is the repeatable --reviews <pr>:<file>: the snapshot of that
// PR's GitHub reviews, in gh's own JSON shape, read instead of calling gh.
type reviewsFlags map[int]string

func (r reviewsFlags) String() string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, strconv.Itoa(k))
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func (r *reviewsFlags) Set(v string) error {
	n, path, ok := strings.Cut(v, ":")
	if !ok {
		return errors.New("--reviews wants <pr>:<file>")
	}
	pr, err := strconv.Atoi(n)
	if err != nil || pr <= 0 || strings.TrimSpace(path) == "" {
		return errors.New("--reviews wants <pr>:<file>")
	}
	(*r)[pr] = path
	return nil
}

// ledgerRead is one reader's verdict at one exact head, from one of the two
// sources a verdict is said at. Per (who, head) the newest at decides.
type ledgerRead struct {
	Who     string
	Verdict string // approve | hold
	Scope   string // "" (the whole PR) | contract | code | security
	Head    string // the full sha the reader read
	At      string
	Source  string // bus | github
}

// ledgerAsk is one typed ask: a required role somebody named a reader for.
type ledgerAsk struct {
	PR   int
	Role string
	Who  string
	At   string
}

// entryLedger is one entry's record: the live head, the roles required, every
// ingested read, the ones a push made stale, and the reads still owed.
type entryLedger struct {
	entry  *merge.Entry
	head   string
	author string
	roles  []string
	asks   []ledgerAsk
	reads  []ledgerRead
	stales []ledgerRead
	owed   []owedItem
	ready  bool
}

type owedItem struct {
	Role string
	Who  string
}

// parseReadTypedLine parses `READ #<pr> <40-character sha> APPROVE|HOLD
// [scope=<role>]`, the typed line a friend's verdict is said in. The sha is
// the exact head the reader had open and is never shortened: a truncated sha
// might name the wrong commit.
func parseReadTypedLine(f []string) (pr int, sha, verdict, scope string, err error) {
	if len(f) < 4 || len(f) > 5 {
		return 0, "", "", "", errors.New("a typed READ line is READ #<pr> <40-character sha> APPROVE|HOLD [scope=contract|code|security]")
	}
	pr, err = typedPR(f[1])
	if err != nil {
		return 0, "", "", "", err
	}
	sha = f[2]
	if !merge.IsSHA(sha) {
		return 0, "", "", "", fmt.Errorf("a typed READ line wants the full 40-character sha the reader had open, got %q", sha)
	}
	switch strings.ToLower(f[3]) {
	case "approve", "hold":
		verdict = strings.ToLower(f[3])
	default:
		return 0, "", "", "", fmt.Errorf("a typed READ line's verdict is APPROVE or HOLD, got %q", f[3])
	}
	if len(f) == 5 {
		name, value, ok := strings.Cut(f[4], "=")
		if !ok || name != "scope" || !validRole(value) {
			return 0, "", "", "", fmt.Errorf("a typed READ line's scope is scope=contract|code|security, got %q", f[4])
		}
		scope = value
	}
	return pr, sha, verdict, scope, nil
}

// parseAskTypedLine parses `ASK #<pr> <role> <who>` — an explicit ask for one
// required read, naming the reader who owes it.
func parseAskTypedLine(f []string) (pr int, role, who string, err error) {
	if len(f) != 4 {
		return 0, "", "", errors.New("a typed ASK line is ASK #<pr> <role> <who>")
	}
	pr, err = typedPR(f[1])
	if err != nil {
		return 0, "", "", err
	}
	if !validRole(f[2]) {
		return 0, "", "", fmt.Errorf("a typed ASK line's role is contract, code or security, got %q", f[2])
	}
	if strings.TrimSpace(f[3]) == "" {
		return 0, "", "", errors.New("a typed ASK line names who owes the read")
	}
	return pr, f[2], f[3], nil
}

func typedPR(field string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(field, "#"))
	if err != nil || n <= 0 || field == strconv.Itoa(n) {
		return 0, fmt.Errorf("a typed line names the pull request as #<n>, got %q", field)
	}
	return n, nil
}

func validRole(r string) bool {
	return r == roleContract || r == roleCode || r == roleSecurity
}

// scanBusTypedLines walks a directory of bus notes and folds every typed READ
// and ASK line. A note that does not parse is skipped — naming those is
// nova-bus's job — but a typed line that is ours and mistyped is a refusal,
// because silently dropping a friend's verdict is the failure this ledger
// exists to close. A line is ours only when its second field is #<pr> and the
// lane holds that pull request (held). Every other line that merely starts
// with READ or ASK — prose ("Read the spec first"), a line about another
// lane's PR, a malformed line naming no PR — is stepped over: the bus is
// shared, and one stranger's typo must not refuse this lane's whole ledger
// (Emma's HOLD on #2863).
func scanBusTypedLines(dir string, held map[int]bool) (map[int][]ledgerRead, map[int][]ledgerAsk, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	reads := map[int][]ledgerRead{}
	asks := map[int][]ledgerAsk{}
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			return nil, nil, err
		}
		note, perr := bus.ParseNote(de.Name(), string(b))
		if perr != nil {
			continue
		}
		at, resolved := "", false
		noteAt := func() (string, error) {
			if resolved {
				return at, nil
			}
			if strings.TrimSpace(note.Header.Date) == "" {
				return "", fmt.Errorf("%s: a typed line needs the note's Date line to order the record", de.Name())
			}
			t, err := time.Parse(bus.DateLayout, note.Header.Date)
			if err != nil {
				return "", fmt.Errorf("%s: the note's Date line %q does not parse as %q", de.Name(), note.Header.Date, bus.DateLayout)
			}
			at, resolved = t.UTC().Format(time.RFC3339), true
			return at, nil
		}
		for i, ln := range strings.Split(strings.ReplaceAll(note.Body, "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(ln)
			f := strings.Fields(trimmed)
			if len(f) == 0 {
				continue
			}
			isRead, isAsk := strings.EqualFold(f[0], "READ"), strings.EqualFold(f[0], "ASK")
			if !isRead && !isAsk {
				continue
			}
			if len(f) < 2 {
				continue
			}
			if pr, perr := typedPR(f[1]); perr != nil || !held[pr] {
				continue
			}
			where := fmt.Sprintf("%s body line %d", de.Name(), i+1)
			switch {
			case isRead:
				if strings.TrimSpace(note.Header.From) == "" {
					return nil, nil, fmt.Errorf("%s: a typed READ line in a note with no From line names no reader", where)
				}
				at, err := noteAt()
				if err != nil {
					return nil, nil, err
				}
				pr, sha, verdict, scope, perr := parseReadTypedLine(f)
				if perr != nil {
					return nil, nil, fmt.Errorf("%s: %w; got %q", where, perr, oneline.Cap(trimmed, 120))
				}
				reads[pr] = append(reads[pr], ledgerRead{Who: note.Header.From, Verdict: verdict, Scope: scope, Head: sha, At: at, Source: "bus"})
			case isAsk:
				at, err := noteAt()
				if err != nil {
					return nil, nil, err
				}
				pr, role, who, perr := parseAskTypedLine(f)
				if perr != nil {
					return nil, nil, fmt.Errorf("%s: %w; got %q", where, perr, oneline.Cap(trimmed, 120))
				}
				asks[pr] = append(asks[pr], ledgerAsk{PR: pr, Role: role, Who: who, At: at})
			}
		}
	}
	return reads, asks, nil
}

// ingestPRReviews folds a PR's GitHub reviews into ledger reads. A typed READ
// line in a review body is the record — the sha the reader says they read, not
// the review's commit_id, which is not the head read (#2037) — and otherwise
// APPROVED is an approve and CHANGES_REQUESTED a hold at the commit_id. A
// DISMISSED review authorizes nothing (#1831), and a review with no commit_id
// and no typed line names no exact head and is not a record.
func ingestPRReviews(pr int, revs []prReview) ([]ledgerRead, error) {
	var out []ledgerRead
	for _, r := range revs {
		state := strings.ToUpper(strings.TrimSpace(r.State))
		if state == "DISMISSED" || state == "" {
			continue
		}
		typed := false
		for _, ln := range strings.Split(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n") {
			f := strings.Fields(strings.TrimSpace(ln))
			if len(f) < 2 || !strings.EqualFold(f[0], "READ") {
				continue
			}
			// Only READ #<n> ... is a typed line; prose whose first word is
			// "Read" is stepped over, as scanBusTypedLines does (Johnny's nit
			// on #2863).
			if _, perr := typedPR(f[1]); perr != nil {
				continue
			}
			if strings.TrimSpace(r.SubmittedAt) == "" {
				return nil, fmt.Errorf("pr #%d review by %s: a typed READ line needs the review's submitted_at to order the record", pr, oneline.Field(r.Author))
			}
			linePR, sha, verdict, scope, err := parseReadTypedLine(f)
			if err != nil {
				return nil, fmt.Errorf("pr #%d review by %s: %w; got %q", pr, oneline.Field(r.Author), err, oneline.Cap(strings.TrimSpace(ln), 120))
			}
			if linePR != pr {
				return nil, fmt.Errorf("pr #%d review by %s: a typed READ line names pr #%d, not #%d", pr, oneline.Field(r.Author), linePR, pr)
			}
			out = append(out, ledgerRead{Who: r.Author, Verdict: verdict, Scope: scope, Head: sha, At: r.SubmittedAt, Source: "github"})
			typed = true
		}
		if typed {
			continue
		}
		var verdict string
		switch state {
		case "APPROVED":
			verdict = "approve"
		case "CHANGES_REQUESTED":
			verdict = "hold"
		default:
			continue
		}
		commit := strings.TrimSpace(r.CommitID)
		if !merge.IsSHA(commit) || strings.TrimSpace(r.SubmittedAt) == "" {
			continue
		}
		out = append(out, ledgerRead{Who: r.Author, Verdict: verdict, Head: commit, At: r.SubmittedAt, Source: "github"})
	}
	return out, nil
}

// foldLedgerReads keeps, per (who, head), the newest record — and a tie
// between an approve and a hold folds HOLD-LAST, so a hold never loses a tie.
func foldLedgerReads(rs []ledgerRead) []ledgerRead {
	best := map[string]int{}
	for i, r := range rs {
		k := strings.ToLower(strings.TrimSpace(r.Who)) + "@" + r.Head
		j, ok := best[k]
		if !ok {
			best[k] = i
			continue
		}
		b := rs[j]
		if r.At > b.At || (r.At == b.At && r.Verdict == "hold" && b.Verdict != "hold") {
			best[k] = i
		}
	}
	out := make([]ledgerRead, 0, len(best))
	for _, i := range best {
		out = append(out, rs[i])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].Who < out[j].Who
	})
	return out
}

// rolesForPaths derives the required reader roles from the paths a PR touches:
// a contract document is a spec or any markdown; anything else is code; and a
// path that names secrets, auth, permissions, a sandbox, crypto or CI is a
// security read on top. Judgment, recorded here, from this repository's own
// risk tiers.
func rolesForPaths(paths []string) []string {
	var contract, code, security bool
	for _, p := range paths {
		p = strings.ToLower(filepath.ToSlash(strings.TrimSpace(p)))
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, ".md") || strings.HasPrefix(p, "docs/") {
			contract = true
		} else {
			code = true
		}
		if strings.HasPrefix(p, ".github/workflows/") {
			security = true
			continue
		}
		for _, m := range securityPathMarkers {
			if strings.Contains(p, m) {
				security = true
				break
			}
		}
	}
	var roles []string
	for _, role := range readRoleOrder {
		switch role {
		case roleContract:
			if contract {
				roles = append(roles, role)
			}
		case roleCode:
			if code {
				roles = append(roles, role)
			}
		case roleSecurity:
			if security {
				roles = append(roles, role)
			}
		}
	}
	return roles
}

// withAskRoles adds the explicitly asked roles to the derived ones, keeping
// the one role order.
func withAskRoles(derived []string, asks []ledgerAsk) []string {
	out := derived
	for _, role := range readRoleOrder {
		if roleIn(out, role) {
			continue
		}
		for _, a := range asks {
			if a.Role == role {
				out = append(out, role)
				break
			}
		}
	}
	return out
}

func roleIn(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// scopeCovers: an unscoped read is the whole PR; a scoped one its scope alone.
func scopeCovers(scope, role string) bool {
	return scope == "" || scope == role
}

// isSelfRead: nobody reads their own work, so the author's own verdict is a
// record and never a satisfying one.
func isSelfRead(who, author string) bool {
	return author != "" && strings.EqualFold(strings.TrimSpace(who), strings.TrimSpace(author))
}

func newestAsk(asks []ledgerAsk, role string) (ledgerAsk, bool) {
	var best ledgerAsk
	found := false
	for _, a := range asks {
		if a.Role != role {
			continue
		}
		if !found || a.At >= best.At {
			best, found = a, true
		}
	}
	return best, found
}

// newestStaleCovering is the newest read at a head that is no longer live whose
// scope covers the role: the reader whose re-read the push made owed.
func newestStaleCovering(reads []ledgerRead, live, role, author string) *ledgerRead {
	var best *ledgerRead
	for i := range reads {
		r := &reads[i]
		if r.Head == live || !scopeCovers(r.Scope, role) || isSelfRead(r.Who, author) {
			continue
		}
		if best == nil || r.At >= best.At {
			best = r
		}
	}
	return best
}

// buildEntryLedger computes one entry's ledger: the live head (fetched, never
// guessed), the required roles, every ingested read folded per (who, head),
// the stale ones, the owed ones, and whether the entry is ready.
func buildEntryLedger(ctx context.Context, st *merge.State, repo string, e *merge.Entry, reviewFiles reviewsFlags, busReads map[int][]ledgerRead, busAsks map[int][]ledgerAsk, errOut io.Writer) (*entryLedger, int) {
	live, err := fetchEntryHead(ctx, repo, e.PR, e.Branch, st.Repo)
	if err != nil {
		return nil, readsRefuse(errOut, fmt.Sprintf("could not fetch the head of %s %s: %v", e.Kind(), oneline.Field(e.ID()), err))
	}
	live = strings.TrimSpace(live)

	var reads []ledgerRead
	var asks []ledgerAsk
	author := ""
	if e.IsPR() {
		var revs []prReview
		if file, ok := reviewFiles[e.PR]; ok {
			b, rerr := os.ReadFile(file)
			if rerr != nil {
				return nil, readsRefuse(errOut, fmt.Sprintf("--reviews %d: %v", e.PR, rerr))
			}
			var v reviewsFile
			if jerr := json.Unmarshal(b, &v); jerr != nil {
				return nil, readsRefuse(errOut, fmt.Sprintf("--reviews %d: not the JSON `gh pr view %d --json author,reviews` prints: %v", e.PR, e.PR, jerr))
			}
			author = v.Author.Login
			for _, r := range v.Reviews {
				revs = append(revs, prReview{Author: r.Author.Login, State: r.State, CommitID: r.CommitID, Body: r.Body, SubmittedAt: r.SubmittedAt})
			}
		} else {
			a, ghRevs, verr := viewPRReviews(ctx, e.PR, st.Repo)
			if verr != nil {
				return nil, readsRefuse(errOut, fmt.Sprintf("could not read the GitHub reviews of pr %d, so no ledger for it (a failed read is not \"no reviews\"; pass --reviews %d:<file> to read a snapshot): %v", e.PR, e.PR, verr))
			}
			author, revs = a, ghRevs
		}
		if author == "" {
			return nil, readsRefuse(errOut, fmt.Sprintf("the GitHub reviews of pr %d name no author, so the author's own reads cannot be told from a friend's", e.PR))
		}
		ghReads, gerr := ingestPRReviews(e.PR, revs)
		if gerr != nil {
			return nil, readsRefuse(errOut, gerr.Error())
		}
		reads = append(reads, ghReads...)
		reads = append(reads, busReads[e.PR]...)
		asks = busAsks[e.PR]
	}
	folded := foldLedgerReads(reads)

	baseSHA := st.Base
	if !merge.IsSHA(baseSHA) {
		baseSHA, err = fetchBase(ctx, repo, e.PR, st.Base, st.Repo)
		if err != nil {
			return nil, readsRefuse(errOut, err.Error())
		}
	}
	diffOut, derr := gitOut(ctx, repo, "diff", "--name-only", baseSHA+"..."+live)
	if derr != nil {
		return nil, readsRefuse(errOut, fmt.Sprintf("could not read the paths %s touches: %v", oneline.Field(e.ID()), derr))
	}
	var paths []string
	for _, ln := range strings.Split(diffOut, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			paths = append(paths, t)
		}
	}
	roles := withAskRoles(rolesForPaths(paths), asks)

	satisfied := map[string]bool{}
	held := map[string]bool{}
	for _, r := range folded {
		if r.Head != live || isSelfRead(r.Who, author) {
			continue
		}
		for _, role := range roles {
			if scopeCovers(r.Scope, role) {
				if r.Verdict == "approve" {
					satisfied[role] = true
				}
				if r.Verdict == "hold" {
					held[role] = true
				}
			}
		}
	}
	ready := len(roles) > 0
	for _, role := range roles {
		if !satisfied[role] || held[role] {
			ready = false
		}
	}

	l := &entryLedger{entry: e, head: live, author: author, roles: roles, asks: asks, reads: folded, ready: ready}
	for _, r := range folded {
		if r.Head != live {
			l.stales = append(l.stales, r)
		}
	}
	for _, role := range roles {
		if satisfied[role] {
			continue
		}
		item := owedItem{Role: role, Who: "-"}
		if a, ok := newestAsk(asks, role); ok {
			item.Who = a.Who
		} else if r := newestStaleCovering(folded, live, role, author); r != nil {
			item.Who = r.Who
		}
		l.owed = append(l.owed, item)
	}
	return l, 0
}

func readsRefuse(w io.Writer, reason string) int {
	fmt.Fprintf(w, "READS REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

func readsRolesField(roles []string) string {
	if len(roles) == 0 {
		return "-"
	}
	return strings.Join(roles, ",")
}

func readsScopeField(scope string) string {
	if scope == "" {
		return "-"
	}
	return scope
}

// reads is the reads ledger verb: `nova-review reads --lane <dir>` prints the
// ledger; --waiting-on <friend> is that friend's queue in order; --ready lists
// the PRs whose every required read is an approve at the live head.
func reads(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("reads", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	lane := fs.String("lane", "", "")
	busDir := fs.String("bus", "", "")
	waitingOn := fs.String("waiting-on", "", "")
	readyOn := fs.Bool("ready", false, "")
	maxFlag := fs.Int("max", 20, "")
	timeout := fs.Int("timeout", 120, "")
	reviewFiles := reviewsFlags{}
	fs.Var(&reviewFiles, "reviews", "")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return readsRefuse(errOut, "bad reads flags; run: nova-review help")
	}
	if *lane == "" {
		return readsRefuse(errOut, "--lane <dir> is required; the reads ledger reads a nova-merge lane; run: nova-review help")
	}
	if strings.TrimSpace(*waitingOn) != "" && *readyOn {
		return readsRefuse(errOut, "--waiting-on and --ready answer different questions; give one")
	}
	if *maxFlag < 0 {
		return readsRefuse(errOut, "--max must be non-negative")
	}
	if *timeout <= 0 {
		return readsRefuse(errOut, "--timeout must be positive")
	}
	if *busDir != "" {
		if fi, err := os.Stat(*busDir); err != nil {
			return readsRefuse(errOut, fmt.Sprintf("--bus %s: %v", oneline.Field(*busDir), err))
		} else if !fi.IsDir() {
			return readsRefuse(errOut, fmt.Sprintf("--bus %s is not a directory of bus notes", oneline.Field(*busDir)))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	st, err := merge.Load(*lane)
	if err != nil {
		if errors.Is(err, merge.ErrNotALane) {
			return readsRefuse(errOut, fmt.Sprintf("--lane %s is not a lane; a lane is a directory made by nova-merge init --lane <dir> --repo <owner/name> --base <branch> --lane-branch <name>", oneline.Field(*lane)))
		}
		return readsRefuse(errOut, fmt.Sprintf("could not read lane: %v", err))
	}
	repo := filepath.Join(*lane, merge.RepoDir)
	for pr := range reviewFiles {
		if e := st.Find(strconv.Itoa(pr)); e == nil || !e.IsPR() {
			return readsRefuse(errOut, fmt.Sprintf("--reviews %d: the lane holds no pull request %d", pr, pr))
		}
	}

	var busReads map[int][]ledgerRead
	var busAsks map[int][]ledgerAsk
	if *busDir != "" {
		held := map[int]bool{}
		for _, e := range st.Entries() {
			if e.IsPR() {
				held[e.PR] = true
			}
		}
		busReads, busAsks, err = scanBusTypedLines(*busDir, held)
		if err != nil {
			return readsRefuse(errOut, err.Error())
		}
	}

	var ledgers []*entryLedger
	for _, e := range st.Entries() {
		l, code := buildEntryLedger(ctx, st, repo, e, reviewFiles, busReads, busAsks, errOut)
		if code != 0 {
			return code
		}
		ledgers = append(ledgers, l)
	}

	switch {
	case *readyOn:
		shown, total := 0, 0
		for _, l := range ledgers {
			if !l.ready {
				continue
			}
			total++
			if *maxFlag > 0 && shown >= *maxFlag {
				continue
			}
			shown++
			fmt.Fprintf(out, "READS READY kind=%s id=%s head=%s roles=%s\n",
				l.entry.Kind(), oneline.Field(l.entry.ID()), l.head, readsRolesField(l.roles))
		}
		if total == 0 {
			fmt.Fprintln(out, "READS READY none")
		}
		if total > shown {
			fmt.Fprintf(out, "READS MORE kind=ready shown=%d total=%d nova-review reads --lane %s --ready --max 0\n",
				shown, total, shellQuote(*lane))
		}
	case strings.TrimSpace(*waitingOn) != "":
		friend := strings.TrimSpace(*waitingOn)
		type queueItem struct {
			kind, id, role, why, at, delta string
		}
		var queue []queueItem
		for _, l := range ledgers {
			for _, o := range l.owed {
				if !strings.EqualFold(strings.TrimSpace(o.Who), friend) {
					continue
				}
				item := queueItem{kind: l.entry.Kind(), id: l.entry.ID(), role: o.Role}
				staleRead := newestStaleCovering(l.reads, l.head, o.Role, l.author)
				if a, ok := newestAsk(l.asks, o.Role); ok && strings.EqualFold(strings.TrimSpace(a.Who), friend) {
					item.why, item.at = "ask", a.At
				} else if staleRead != nil {
					item.why, item.at = "stale", staleRead.At
				} else {
					continue
				}
				if staleRead != nil {
					item.delta = merge.Short(staleRead.Head) + ".." + merge.Short(l.head)
				}
				queue = append(queue, item)
			}
		}
		sort.Slice(queue, func(i, j int) bool {
			if queue[i].at != queue[j].at {
				return queue[i].at < queue[j].at
			}
			return queue[i].id < queue[j].id
		})
		shown := len(queue)
		if *maxFlag > 0 && shown > *maxFlag {
			shown = *maxFlag
		}
		for _, q := range queue[:shown] {
			line := fmt.Sprintf("READS WAITING who=%s kind=%s id=%s role=%s why=%s at=%s",
				oneline.Field(friend), q.kind, oneline.Field(q.id), q.role, q.why, q.at)
			if q.delta != "" {
				line += " delta=" + q.delta
			}
			fmt.Fprintln(out, line)
		}
		if len(queue) == 0 {
			fmt.Fprintf(out, "READS WAITING who=%s none\n", oneline.Field(friend))
		}
		if len(queue) > shown {
			fmt.Fprintf(out, "READS MORE kind=waiting who=%s shown=%d total=%d nova-review reads --lane %s --waiting-on %s --max 0\n",
				oneline.Field(friend), shown, len(queue), shellQuote(*lane), shellQuote(friend))
		}
	default:
		for _, l := range ledgers {
			authorField := l.author
			if authorField == "" {
				authorField = "-"
			}
			fmt.Fprintf(out, "READS ENTRY kind=%s id=%s head=%s roles=%s author=%s\n",
				l.entry.Kind(), oneline.Field(l.entry.ID()), l.head, readsRolesField(l.roles), oneline.Field(authorField))
			shown := len(l.reads)
			if *maxFlag > 0 && shown > *maxFlag {
				shown = *maxFlag
			}
			for _, r := range l.reads[:shown] {
				line := fmt.Sprintf("READ kind=%s id=%s who=%s verdict=%s scope=%s sha=%s at=%s source=%s",
					l.entry.Kind(), oneline.Field(l.entry.ID()), oneline.Field(r.Who), r.Verdict, readsScopeField(r.Scope), r.Head, r.At, r.Source)
				if isSelfRead(r.Who, l.author) {
					line += " self=yes"
				}
				fmt.Fprintln(out, line)
			}
			if len(l.reads) > shown {
				fmt.Fprintf(out, "READS MORE kind=read id=%s shown=%d total=%d nova-review reads --lane %s --max 0\n",
					oneline.Field(l.entry.ID()), shown, len(l.reads), shellQuote(*lane))
			}
			for _, r := range l.stales {
				fmt.Fprintf(out, "READS STALE kind=%s id=%s who=%s read=%s live=%s delta=%s\n",
					l.entry.Kind(), oneline.Field(l.entry.ID()), oneline.Field(r.Who),
					merge.Short(r.Head), merge.Short(l.head), merge.Short(r.Head)+".."+merge.Short(l.head))
			}
			for _, o := range l.owed {
				fmt.Fprintf(out, "READS OWED kind=%s id=%s role=%s who=%s\n",
					l.entry.Kind(), oneline.Field(l.entry.ID()), o.Role, oneline.Field(o.Who))
			}
		}
	}
	return 0
}
