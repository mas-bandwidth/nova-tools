// Package fold is `nova-sprint fold <S>` (nova-tools #2618, #2756 sections 0.5
// and 4.1): it reads a closed sprint from the store, counts landed, done and
// useful cards, prices them per route in dollars per useful card and per landed
// card, writes that record into nova-work as ONE commit, and records the
// commit's sha on the sprint hash with status folded.
//
// The fold is idempotent by sprint name (control 20). The commit carries the
// trailer `Sprint-Fold: <S>`; a re-run looks for it first and finds the commit
// instead of making a second one. The store says folded only after the commit
// exists, in one script call that also appends the closed -> folded receipt,
// so a fold killed at any step and run again makes one commit and one receipt.
//
// Reads are batched: the sprint hash, its policy and the log length in one
// pipeline, a SCAN for the index sets, then one pipeline each for the index
// members, the card hashes and the dispositions.
package fold

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// The step boundaries a kill can land between; Options.Hook sees each one.
const (
	StepWritten   = "written"   // the fold file is on disk, not staged
	StepStaged    = "staged"    // the fold file is in the index, not committed
	StepCommitted = "committed" // the commit exists, the store does not know it
)

// DefaultUsefulMin is the score a typed APPROVE at head needs for the card to
// count as useful when the sprint policy names no useful_min (nova-tools 8+).
const DefaultUsefulMin = 8

// Options names one fold. Work is the nova-work checkout the commit goes into;
// Path, relative to it, defaults to docs/roadmaps/folds/<S>.sexp.
type Options struct {
	Sprint string
	Work   string
	Path   string
	Actor  string
	Now    func() time.Time
	// Hook runs at each step boundary; an error stops the fold there, as a
	// kill would, with nothing undone. Tests use it for control 20.
	Hook func(step string) error
}

// Result is what the fold did.
type Result struct {
	FoldSHA string
	Made    bool // false when the commit was found or the sprint was already folded
}

// Route is one route's line: the model cards dealt on it this sprint.
type Route struct {
	Name     string
	Cards    int
	Done     int
	Useful   int
	Landed   int
	Priced   int
	USDMicro int64
}

// Unpriced counts the cards whose cost was never measured; their cost is an
// absence, never a zero, so usd is a lower bound when this is not 0.
func (r Route) Unpriced() int { return r.Cards - r.Priced }

// Summary is the sprint's fold record.
type Summary struct {
	Sprint      string
	Goal        string
	NovaWorkSHA string
	UsefulMin   int
	Total       Route // every model card, as one line
	Routes      []Route
	CICards     int
	CIDone      int
	Tasks       int
	TasksDone   int
	Receipts    int64
}

var sprintName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Run folds opt.Sprint. It prints one line per route, the ci line, the sprint
// line, the commit and the record, and returns an error for any refusal.
func Run(ctx context.Context, client *redis.Client, opt Options, out io.Writer) (Result, error) {
	if !sprintName.MatchString(opt.Sprint) {
		return Result{}, fmt.Errorf("sprint name %q is not a sprint name ([A-Za-z0-9._-], starting with a letter or digit)", opt.Sprint)
	}
	if opt.Work == "" {
		return Result{}, errors.New("--work names the nova-work checkout the fold commits into")
	}
	rel := opt.Path
	if rel == "" {
		rel = filepath.Join("docs", "roadmaps", "folds", opt.Sprint+".sexp")
	}
	if !filepath.IsLocal(rel) {
		return Result{}, fmt.Errorf("--path %q is not a path inside the nova-work checkout", rel)
	}
	if opt.Actor == "" {
		opt.Actor = "nova-sprint"
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	hook := opt.Hook
	if hook == nil {
		hook = func(string) error { return nil }
	}

	key := "s:" + opt.Sprint
	head, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", key, err)
	}
	if len(head) == 0 {
		return Result{}, fmt.Errorf("no sprint %s in the store (%s is absent)", opt.Sprint, key)
	}
	switch head["status"] {
	case "folded":
		fmt.Fprintf(out, "FOLD DONE sprint=%s fold_sha=%s status=folded\n", opt.Sprint, oneline.Field(head["fold_sha"]))
		return Result{FoldSHA: head["fold_sha"]}, nil
	case "closed":
	default:
		return Result{}, fmt.Errorf("sprint %s is %s, not closed; fold reads a closed sprint (nova-sprint sprint close %s)", opt.Sprint, oneline.Field(head["status"]), opt.Sprint)
	}

	sum, err := Read(ctx, client, opt.Sprint)
	if err != nil {
		return Result{}, err
	}
	PrintLines(out, sum)

	sha, err := findCommit(ctx, opt.Work, opt.Sprint)
	if err != nil {
		return Result{}, err
	}
	made := false
	if sha == "" {
		if sha, err = commit(ctx, opt.Work, rel, sum, hook); err != nil {
			return Result{}, err
		}
		made = true
	}
	verb := "found"
	if made {
		verb = "made"
	}
	fmt.Fprintf(out, "FOLD COMMIT sprint=%s sha=%s path=%s %s\n", opt.Sprint, sha, oneline.Field(filepath.ToSlash(rel)), verb)
	if err := hook(StepCommitted); err != nil {
		return Result{FoldSHA: sha, Made: made}, err
	}

	res, err := record.Run(ctx, client, []string{key, key + ":log"},
		sha, opt.Sprint, opt.Actor, opt.Now().UTC().Format(time.RFC3339)).StringSlice()
	if err != nil {
		return Result{FoldSHA: sha, Made: made}, fmt.Errorf("record the fold on %s: %w", key, err)
	}
	if len(res) != 2 {
		return Result{FoldSHA: sha, Made: made}, fmt.Errorf("record the fold on %s: reply %v", key, res)
	}
	if res[0] == "FOUND" && res[1] != sha {
		return Result{FoldSHA: res[1]}, fmt.Errorf("sprint %s was folded at %s by another run while this one found %s", opt.Sprint, res[1], sha)
	}
	fmt.Fprintf(out, "FOLD RECORDED sprint=%s fold_sha=%s status=folded\n", opt.Sprint, sha)
	return Result{FoldSHA: sha, Made: made}, nil
}

// record moves the sprint closed -> folded with its fold_sha and appends the
// one receipt, in one call. A sprint already folded returns its sha unchanged.
var record = redis.NewScript(`
local st = redis.call('HGET', KEYS[1], 'status')
if st == 'folded' then
  return {'FOUND', redis.call('HGET', KEYS[1], 'fold_sha') or ''}
end
if st ~= 'closed' then
  return redis.error_reply('NOTCLOSED sprint ' .. ARGV[2] .. ' is ' .. tostring(st))
end
redis.call('HSET', KEYS[1], 'status', 'folded', 'fold_sha', ARGV[1])
redis.call('XADD', KEYS[2], '*', 'kind', 'sprint', 'id', ARGV[2], 'from', 'closed', 'to', 'folded',
  'attempt', '1', 'token_sha', '', 'actor', ARGV[3], 'reason', 'fold', 'evidence', ARGV[1],
  'idem', 'fold:' .. ARGV[2], 'at', ARGV[4])
return {'RECORDED', ARGV[1]}
`)

var cardFields = []string{"kind", "state", "outcome", "route", "usd", "repo", "pr", "head", "ci_for"}

// Read builds the sprint's Summary from the store.
func Read(ctx context.Context, client *redis.Client, sprint string) (Summary, error) {
	key := "s:" + sprint
	sum := Summary{Sprint: sprint, UsefulMin: DefaultUsefulMin}

	pipe := client.Pipeline()
	headCmd := pipe.HGetAll(ctx, key)
	policyCmd := pipe.HGet(ctx, key+":policy", "useful_min")
	logCmd := pipe.XLen(ctx, key+":log")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return sum, fmt.Errorf("read %s: %w", key, err)
	}
	head := headCmd.Val()
	sum.Goal, sum.NovaWorkSHA = head["goal"], head["nova_work_sha"]
	if v := policyCmd.Val(); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10 {
			return sum, fmt.Errorf("%s:policy useful_min=%q is not a score 1-10", key, v)
		}
		sum.UsefulMin = n
	}
	sum.Receipts = logCmd.Val()

	cardIdx, err := scanKeys(ctx, client, key+":idx:card:*")
	if err != nil {
		return sum, err
	}
	taskIdx, err := scanKeys(ctx, client, key+":idx:task:*")
	if err != nil {
		return sum, err
	}
	pipe = client.Pipeline()
	members := make([]*redis.StringSliceCmd, len(cardIdx))
	for i, k := range cardIdx {
		members[i] = pipe.SMembers(ctx, k)
	}
	counts := make([]*redis.IntCmd, len(taskIdx))
	for i, k := range taskIdx {
		counts[i] = pipe.SCard(ctx, k)
	}
	if len(cardIdx)+len(taskIdx) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return sum, fmt.Errorf("read the index sets of %s: %w", key, err)
		}
	}
	for i, k := range taskIdx {
		n := counts[i].Val()
		sum.Tasks += int(n)
		if strings.TrimPrefix(k, key+":idx:task:") == "closed" {
			sum.TasksDone += int(n)
		}
	}
	labelSet := map[string]bool{}
	for _, m := range members {
		for _, label := range m.Val() {
			labelSet[label] = true
		}
	}
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	cards := make([]map[string]string, len(labels))
	if len(labels) > 0 {
		pipe = client.Pipeline()
		cmds := make([]*redis.SliceCmd, len(labels))
		for i, label := range labels {
			cmds[i] = pipe.HMGet(ctx, key+":card:"+label, cardFields...)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return sum, fmt.Errorf("read the cards of %s: %w", key, err)
		}
		for i, cmd := range cmds {
			card := map[string]string{}
			for j, v := range cmd.Val() {
				if s, ok := v.(string); ok {
					card[cardFields[j]] = s
				}
			}
			cards[i] = card
		}
	}

	// The dispositions of every PR a card produced, one pipeline.
	dispKeys := map[string]bool{}
	for _, c := range cards {
		if c["repo"] != "" && c["pr"] != "" && c["state"] != "landed" {
			dispKeys[key+":disp:"+c["repo"]+":"+c["pr"]] = true
		}
	}
	disp := map[string]map[string]string{}
	if len(dispKeys) > 0 {
		pipe = client.Pipeline()
		cmds := map[string]*redis.MapStringStringCmd{}
		for k := range dispKeys {
			cmds[k] = pipe.HGetAll(ctx, k)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return sum, fmt.Errorf("read the dispositions of %s: %w", key, err)
		}
		for k, cmd := range cmds {
			disp[k] = cmd.Val()
		}
	}

	routes := map[string]*Route{}
	for i, c := range cards {
		if c["ci_for"] != "" {
			sum.CICards++
			if c["outcome"] == "DONE" {
				sum.CIDone++
			}
			continue
		}
		name := c["route"]
		if name == "" {
			name = "-"
		}
		r := routes[name]
		if r == nil {
			r = &Route{Name: name}
			routes[name] = r
		}
		landed := c["state"] == "landed"
		useful := landed || usefulAtHead(disp[key+":disp:"+c["repo"]+":"+c["pr"]], c["head"], sum.UsefulMin)
		priced, micro := false, int64(0)
		switch v := strings.TrimSpace(c["usd"]); v {
		case "", tokens.Dash:
		default:
			m, ok := tokens.ParseMicro(v)
			if !ok {
				return sum, fmt.Errorf("card %s has usd=%q, not a dollar amount", labels[i], v)
			}
			priced, micro = true, m
		}
		for _, line := range []*Route{r, &sum.Total} {
			line.Cards++
			if c["outcome"] == "DONE" {
				line.Done++
			}
			if useful {
				line.Useful++
			}
			if landed {
				line.Landed++
			}
			if priced {
				line.Priced++
				line.USDMicro += micro
			}
		}
	}
	sum.Total.Name = "all"
	for _, r := range routes {
		sum.Routes = append(sum.Routes, *r)
	}
	sort.Slice(sum.Routes, func(i, j int) bool { return sum.Routes[i].Name < sum.Routes[j].Name })
	return sum, nil
}

func scanKeys(ctx context.Context, client *redis.Client, match string) ([]string, error) {
	var keys []string
	iter := client.Scan(ctx, 0, match, 1000).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", match, err)
	}
	sort.Strings(keys)
	return keys, nil
}

// usefulAtHead: a typed APPROVE at exactly the card's head with a score at or
// above min. A read at another head, or a HOLD, is not.
func usefulAtHead(d map[string]string, head string, min int) bool {
	if head == "" {
		return false
	}
	for field, value := range d {
		at := strings.LastIndexByte(field, '@')
		if at < 0 || !sameSHA(field[at+1:], head) {
			continue
		}
		parts := strings.Fields(value)
		if len(parts) < 2 || parts[0] != "APPROVE" {
			continue
		}
		score, _, _ := strings.Cut(parts[1], "/")
		if n, err := strconv.Atoi(score); err == nil && n >= min {
			return true
		}
	}
	return false
}

// sameSHA compares a full and an abbreviated sha, at least 7 hex digits.
func sameSHA(a, b string) bool {
	if a == b {
		return a != ""
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 7 && strings.HasPrefix(b, a)
}

func per(micro int64, n, priced int) string {
	if n == 0 || priced == 0 {
		return tokens.Dash
	}
	return tokens.Usd((micro + int64(n)/2) / int64(n))
}

func usd(r Route) string {
	if r.Priced == 0 {
		return tokens.Dash
	}
	return tokens.Usd(r.USDMicro)
}

// PrintLines prints the route lines in name order, the ci line and the sprint line.
func PrintLines(out io.Writer, s Summary) {
	for _, r := range s.Routes {
		fmt.Fprintf(out, "FOLD ROUTE sprint=%s route=%s cards=%d done=%d useful=%d landed=%d usd=%s usd_per_useful=%s usd_per_landed=%s unpriced=%d\n",
			s.Sprint, oneline.Field(r.Name), r.Cards, r.Done, r.Useful, r.Landed, usd(r),
			per(r.USDMicro, r.Useful, r.Priced), per(r.USDMicro, r.Landed, r.Priced), r.Unpriced())
	}
	fmt.Fprintf(out, "FOLD CI sprint=%s cards=%d done=%d\n", s.Sprint, s.CICards, s.CIDone)
	t := s.Total
	fmt.Fprintf(out, "FOLD SPRINT sprint=%s cards=%d done=%d useful=%d landed=%d usd=%s usd_per_useful=%s usd_per_landed=%s unpriced=%d tasks=%d tasks_done=%d receipts=%d useful_min=%d\n",
		s.Sprint, t.Cards, t.Done, t.Useful, t.Landed, usd(t),
		per(t.USDMicro, t.Useful, t.Priced), per(t.USDMicro, t.Landed, t.Priced), t.Unpriced(),
		s.Tasks, s.TasksDone, s.Receipts, s.UsefulMin)
}

func q(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func routeSexp(head string, r Route) string {
	return fmt.Sprintf("(%s :cards %d :done %d :useful %d :landed %d :usd %s :usd-per-useful %s :usd-per-landed %s :unpriced %d)",
		head, r.Cards, r.Done, r.Useful, r.Landed, q(usd(r)),
		q(per(r.USDMicro, r.Useful, r.Priced)), q(per(r.USDMicro, r.Landed, r.Priced)), r.Unpriced())
}

// Sexp is the fold record committed into nova-work. It holds no clock, so a
// fold re-run after a kill writes the same bytes.
func Sexp(s Summary) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, ";; nova-sprint fold %s (nova-tools #2618): outcomes read from the store at close\n", s.Sprint)
	fmt.Fprintf(&b, "(sprint-fold %s\n", q(s.Sprint))
	fmt.Fprintf(&b, "  :goal %s\n  :nova-work-sha %s\n  :useful-min %d\n", q(s.Goal), q(s.NovaWorkSHA), s.UsefulMin)
	fmt.Fprintf(&b, "  :tasks %d :tasks-done %d :receipts %d\n", s.Tasks, s.TasksDone, s.Receipts)
	fmt.Fprintf(&b, "  :ci (:cards %d :done %d)\n", s.CICards, s.CIDone)
	fmt.Fprintf(&b, "  :total %s\n", routeSexp("all", s.Total))
	b.WriteString("  :routes (")
	for i, r := range s.Routes {
		if i > 0 {
			b.WriteString("\n            ")
		}
		b.WriteString(routeSexp("route "+q(r.Name), r))
	}
	b.WriteString("))\n")
	return []byte(b.String())
}

const trailer = "Sprint-Fold: "

func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, oneline.Escape(strings.TrimSpace(string(out))))
	}
	return string(out), nil
}

// findCommit returns the commit on HEAD whose message carries the exact
// trailer line for this sprint, or "".
func findCommit(ctx context.Context, work, sprint string) (string, error) {
	if _, err := gitCmd(ctx, work, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		if _, err2 := gitCmd(ctx, work, "rev-parse", "--git-dir"); err2 != nil {
			return "", fmt.Errorf("--work %s is not a git checkout: %w", work, err2)
		}
		return "", nil // no commits yet: nothing to find
	}
	out, err := gitCmd(ctx, work, "log", "-F", "--grep="+trailer+sprint, "--format=%H%x00%B%x1e", "HEAD")
	if err != nil {
		return "", err
	}
	for _, rec := range strings.Split(out, "\x1e") {
		sha, body, ok := strings.Cut(strings.TrimLeft(rec, "\n"), "\x00")
		if !ok {
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == trailer+sprint {
				return sha, nil
			}
		}
	}
	return "", nil
}

func commit(ctx context.Context, work, rel string, s Summary, hook func(string) error) (string, error) {
	path := filepath.Join(work, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, Sexp(s), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	if err := hook(StepWritten); err != nil {
		return "", err
	}
	if _, err := gitCmd(ctx, work, "add", "--", rel); err != nil {
		return "", err
	}
	if err := hook(StepStaged); err != nil {
		return "", err
	}
	t := s.Total
	msg := fmt.Sprintf("fold %s: landed %d, done %d, useful %d of %d cards, usd per landed %s\n\n%s%s\n",
		s.Sprint, t.Landed, t.Done, t.Useful, t.Cards, per(t.USDMicro, t.Landed, t.Priced), trailer, s.Sprint)
	if _, err := gitCmd(ctx, work, "commit", "-q", "--only", "-m", msg, "--", rel); err != nil {
		return "", err
	}
	out, err := gitCmd(ctx, work, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
