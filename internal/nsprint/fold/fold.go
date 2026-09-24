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
//
// It also ranks the routes per work type (#3104, #2756 v6 4.10): PR types on $
// per landed, read types on $ per useful read, with probation and benched
// states, and replaces routes:<type> in the same call that records the fold
// (routes.go; the hash shape is route.Fold, the router reads it).
package fold

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
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
	// Jev, when set, calibrates the Jev grader at this fold (#3081).
	Jev *JevCalib
}

// Result is what the fold did.
type Result struct {
	FoldSHA string
	Made    bool // false when the commit was found or the sprint was already folded
	// PromptRefused is why a candidate Jev prompt was not adopted, or "".
	// The fold is recorded either way; Main exits 3 on a refusal.
	PromptRefused string
}

// Route is one route's line: the model cards dealt on it this sprint.
type Route struct {
	Name   string
	Cards  int
	Done   int
	Useful int
	Landed int
	Priced int
	// USDMicro is the summed cost of every priced card on the line, useful
	// or not; it is the numerator of both $ per useful and $ per landed, so
	// both are suppressed (printed as -) whenever any card on the line is
	// unpriced: the total spend is then unknown and the quotient would look
	// exact while being a lower bound.
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
	CI          CICost   // the ci cost line (ci.go, #3046)
	Land        LandCost // the lander's five numbers (land.go, #3139 B17)
	Tasks       int
	TasksDone   int
	Receipts    int64
	Jev         *Calibration // nil when the fold ran no Jev calibration
	Apart       Apart        // code and read cards apart, the unknown gate, approved-not-landed (#3107, split.go)
	// Types is routes:<type> per work type the sprint dealt (#3104), in type
	// order; the record writes each one.
	Types  []*route.Fold
	Policy Policy
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
	adopt := ""
	if jc := opt.Jev; jc != nil {
		if jc.Scorer == nil {
			return Result{}, errors.New("the Jev calibration names no scorer (--jev-eval)")
		}
		if cur, err := client.HGet(ctx, jevPromptKey, "sha").Result(); err != nil && !errors.Is(err, redis.Nil) {
			return Result{}, fmt.Errorf("read %s: %w", jevPromptKey, err)
		} else if cur != "" && cur != jc.PromptSHA {
			return Result{}, fmt.Errorf("Jev runs prompt %s (%s sha), not --prompt-sha %s; calibrate the prompt in use", cur, jevPromptKey, jc.PromptSHA)
		}
		if sum.Jev, err = Calibrate(ctx, jc, sum.UsefulMin); err != nil {
			return Result{}, fmt.Errorf("Jev calibration: %w", err)
		}
		if k := sum.Jev.Candidate; k != nil && k.Adopted {
			adopt = k.SHA
		}
	}
	PrintLines(out, sum)
	if err := foldRote(ctx, client, opt.Sprint, head, out); err != nil {
		return Result{}, err
	}
	refused := ""
	if sum.Jev != nil {
		PrintJev(out, opt.Sprint, sum.Jev)
		if k := sum.Jev.Candidate; k != nil && !k.Adopted {
			refused = k.Reason
		}
	}
	PrintApart(out, opt.Sprint, sum.Apart)
	if err := unknownGate(opt.Sprint, sum.Apart); err != nil {
		return Result{}, err
	}

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

	current := ""
	if opt.Jev != nil {
		current = opt.Jev.PromptSHA
	}
	at := opt.Now().UTC().Format(time.RFC3339)
	rkeys, rargv := recordArgs(sum, sha, at)
	res, err := record.Run(ctx, client, append([]string{key, key + ":log", jevPromptKey}, rkeys...),
		append([]any{sha, opt.Sprint, opt.Actor, at, adopt, current}, rargv...)...).StringSlice()
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
	if adopt != "" && res[0] == "RECORDED" {
		fmt.Fprintf(out, "FOLD JEV ADOPTED sprint=%s prompt_sha=%s was=%s\n", opt.Sprint, oneline.Field(adopt), oneline.Field(current))
	}
	return Result{FoldSHA: sha, Made: made, PromptRefused: refused}, nil
}

// record moves the sprint closed -> folded with its fold_sha and appends the
// one receipt, in one call. A sprint already folded returns its sha unchanged.
// ARGV[5], when not empty, is a Jev prompt the calibration adopted: it moves
// jev:prompt (KEYS[3]) from ARGV[6] in the same call, and refuses if another
// run moved it first. KEYS[4..] are routes:<type> (#3104): the fold replaces
// each one in this same call, from ARGV[7..].
var record = redis.NewScript(`
local st = redis.call('HGET', KEYS[1], 'status')
if st == 'folded' then
  return {'FOUND', redis.call('HGET', KEYS[1], 'fold_sha') or ''}
end
if st ~= 'closed' then
  return redis.error_reply('NOTCLOSED sprint ' .. ARGV[2] .. ' is ' .. tostring(st))
end
if ARGV[5] ~= '' then
  local cur = redis.call('HGET', KEYS[3], 'sha')
  if cur and cur ~= ARGV[6] then
    return redis.error_reply('PROMPTMOVED jev:prompt is ' .. cur .. ', not ' .. ARGV[6])
  end
  redis.call('HSET', KEYS[3], 'sha', ARGV[5], 'prev', ARGV[6], 'fold', ARGV[2], 'at', ARGV[4])
end
redis.call('HSET', KEYS[1], 'status', 'folded', 'fold_sha', ARGV[1])
redis.call('XADD', KEYS[2], '*', 'kind', 'sprint', 'id', ARGV[2], 'from', 'closed', 'to', 'folded',
  'attempt', '1', 'token_sha', '', 'actor', ARGV[3], 'reason', 'fold', 'evidence', ARGV[1],
  'idem', 'fold:' .. ARGV[2], 'at', ARGV[4])
-- routes:<type> (#3104): KEYS[4..] with, per key, a count and that many
-- field, value strings from ARGV[7] on (ARGV[5], ARGV[6] are the Jev
-- adoption's); the fold is the only writer, so each key is replaced.
local a = 7
for k = 4, #KEYS do
  local n = tonumber(ARGV[a])
  a = a + 1
  redis.call('DEL', KEYS[k])
  for j = 0, n - 1, 2 do
    redis.call('HSET', KEYS[k], ARGV[a + j], ARGV[a + j + 1])
  end
  a = a + n
end
return {'RECORDED', ARGV[1]}
`)

// type is the card's work type and score its own read score (#3104); neither is
// in the #2756 2.3 card row yet, and a card without them is left out of routes:<type>.
var cardFields = []string{"kind", "state", "outcome", "route", "usd", "repo", "pr", "head", "ci_for", "type", "score"}

// Read builds the sprint's Summary from the store.
func Read(ctx context.Context, client *redis.Client, sprint string) (Summary, error) {
	key := "s:" + sprint
	sum := Summary{Sprint: sprint, UsefulMin: DefaultUsefulMin}

	pipe := client.Pipeline()
	headCmd := pipe.HGetAll(ctx, key)
	policyCmd := pipe.HMGet(ctx, key+":policy", "useful_min", "route_min_landed", "route_bench_after", "probation_share")
	logCmd := pipe.XLen(ctx, key+":log")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return sum, fmt.Errorf("read %s: %w", key, err)
	}
	head := headCmd.Val()
	sum.Goal, sum.NovaWorkSHA = head["goal"], head["nova_work_sha"]
	pol := make([]string, 4)
	for i, v := range policyCmd.Val() {
		if s, ok := v.(string); ok {
			pol[i] = s
		}
	}
	var perr error
	if sum.Policy, perr = policyFrom(pol[1], pol[2], pol[3]); perr != nil {
		return sum, fmt.Errorf("%s:%w", key, perr)
	}
	if v := pol[0]; v != "" {
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
	byType := map[string]map[string]*typeTally{}
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
		useful := landed || usefulAtHead(disp[key+":disp:"+c["repo"]+":"+c["pr"]], c["head"], sum.UsefulMin) ||
			(c["pr"] == "" && scoreAtLeast(c["score"], sum.UsefulMin))
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
		if typ := c["type"]; typ != "" && c["route"] != "" {
			if !route.ValidType(typ) || !route.ValidRoute(c["route"]) {
				return sum, fmt.Errorf("card %s has type=%q route=%q; a work type is [a-z0-9_-] and a route one word", labels[i], typ, c["route"])
			}
			if byType[typ] == nil {
				byType[typ] = map[string]*typeTally{}
			}
			t := byType[typ][c["route"]]
			if t == nil {
				t = &typeTally{}
				byType[typ][c["route"]] = t
			}
			t.add(c["pr"] != "", landed, useful, priced, micro)
		}
	}
	sum.Total.Name = "all"
	for _, r := range routes {
		sum.Routes = append(sum.Routes, *r)
	}
	sort.Slice(sum.Routes, func(i, j int) bool { return sum.Routes[i].Name < sum.Routes[j].Name })
	sum.Types = rankTypes(sprint, byType, sum.Policy)
	ci, err := readCI(ctx, client, sprint, labels, cards)
	sum.CI = ci
	if err != nil {
		return sum, err
	}
	if sum.Land, err = readLand(ctx, client, sprint, head); err != nil {
		return sum, err
	}
	apart, err := readApart(ctx, client, key, sum.UsefulMin)
	if err != nil {
		return sum, err
	}
	sum.Apart = apart
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

// per is the line's total spend over n of its cards, or - when n is 0 or when
// any card on the line is unpriced. The numerator is the whole line's spend,
// not just the n cards', so an unpriced card anywhere on the line (in the n or
// not) makes the total unknown and the quotient a lower bound.
func per(r Route, n int) string {
	if n == 0 || r.Unpriced() > 0 {
		return tokens.Dash
	}
	return tokens.Usd((r.USDMicro + int64(n)/2) / int64(n))
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
			per(r, r.Useful), per(r, r.Landed), r.Unpriced())
	}
	fmt.Fprintf(out, "FOLD CI sprint=%s cards=%d done=%d\n", s.Sprint, s.CICards, s.CIDone)
	printCI(out, s.Sprint, s.CI)
	printLand(out, s.Sprint, s.Land)
	t := s.Total
	fmt.Fprintf(out, "FOLD SPRINT sprint=%s cards=%d done=%d useful=%d landed=%d usd=%s usd_per_useful=%s usd_per_landed=%s unpriced=%d tasks=%d tasks_done=%d receipts=%d useful_min=%d\n",
		s.Sprint, t.Cards, t.Done, t.Useful, t.Landed, usd(t),
		per(t, t.Useful), per(t, t.Landed), t.Unpriced(),
		s.Tasks, s.TasksDone, s.Receipts, s.UsefulMin)
	printTypes(out, s)
}

func q(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func routeSexp(head string, r Route) string {
	return fmt.Sprintf("(%s :cards %d :done %d :useful %d :landed %d :usd %s :usd-per-useful %s :usd-per-landed %s :unpriced %d)",
		head, r.Cards, r.Done, r.Useful, r.Landed, q(usd(r)),
		q(per(r, r.Useful)), q(per(r, r.Landed)), r.Unpriced())
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
	fmt.Fprintf(&b, "  :ci-cost %s\n", ciSexp(s.CI))
	fmt.Fprintf(&b, "  :total %s\n", routeSexp("all", s.Total))
	b.WriteString("  :routes (")
	for i, r := range s.Routes {
		if i > 0 {
			b.WriteString("\n            ")
		}
		b.WriteString(routeSexp("route "+q(r.Name), r))
	}
	b.WriteString(")\n")
	typesSexp(&b, s)
	b.WriteString(apartSexp(s.Apart))
	if s.Jev != nil {
		b.WriteString("\n" + strings.TrimSuffix(jevSexp(s.Jev), "\n"))
	}
	b.WriteString(")\n")
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
		s.Sprint, t.Landed, t.Done, t.Useful, t.Cards, per(t, t.Landed), trailer, s.Sprint)
	if _, err := gitCmd(ctx, work, "commit", "-q", "--only", "-m", msg, "--", rel); err != nil {
		return "", err
	}
	out, err := gitCmd(ctx, work, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// The Jev calibration at the fold (nova-tools #3081, continual-refinement-loop
// steps 3 and 4). Given the resolved PRs of the sprint (a friend's typed line at
// an exact head, the card's work type) the fold re-scores every one with the
// current Jev prompt, writes agreement, MAE, false-pass and false-bounce per
// work type into the fold record, and, when a candidate prompt is named,
// scores the date-split held-out third with it and refuses to adopt it if its
// false passes or false bounces are worse than the current prompt's on any
// type. The fold is recorded either way; adoption is the jev:prompt hash,
// moved in the same script call that records the fold.

// The promotion bar (Stella 2026-09-23; Johnny's separation): a type may be
// promoted only with at least PromoteMinHeads calibrated heads, a Wilson 95%
// lower bound on the verdict's precision of at least PromotePrecisionLB, and
// Jev's mean score on the friend-approved PRs above its mean on the held ones.
const (
	PromoteMinHeads    = 100
	PromotePrecisionLB = 0.98
	jevPromptKey       = "jev:prompt"
)

// CalibRow is one resolved PR: the friend's typed verdict and score at Head
// (Score 0 when the ruling carried none), the card's work type, the time it
// was resolved (the date split orders by it) and an optional tag naming the
// failure shape the row seeds (false-confidence, coverage, few-shot).
type CalibRow struct {
	Repo       string `json:"repo"`
	PR         int    `json:"pr"`
	Head       string `json:"head"`
	WorkType   string `json:"work_type"`
	Who        string `json:"who"`
	Verdict    string `json:"verdict"`
	Score      int    `json:"score"`
	ResolvedAt string `json:"resolved_at"`
	Tag        string `json:"tag,omitempty"`
	Note       string `json:"note,omitempty"`
}

func (r CalibRow) ref() string { return shortRepo(r.Repo) + "#" + strconv.Itoa(r.PR) }

// CalibSet is the calibration set as read, with the sha256 of its bytes.
type CalibSet struct {
	Rows []CalibRow
	SHA  string
}

// ReadCalibSet reads JSONL calibration rows. A row with no head, no PR, a
// verdict other than APPROVE or HOLD, or a score outside 0-10 is refused: a
// guessed label would train the grader on noise.
func ReadCalibSet(r io.Reader) (CalibSet, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return CalibSet{}, err
	}
	var set CalibSet
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row CalibRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return CalibSet{}, fmt.Errorf("calibration row %d: %w", i+1, err)
		}
		switch {
		case row.Repo == "" || row.PR <= 0 || len(row.Head) < 7:
			return CalibSet{}, fmt.Errorf("calibration row %d names no repo, PR or head (7+ hex)", i+1)
		case row.Verdict != "APPROVE" && row.Verdict != "HOLD":
			return CalibSet{}, fmt.Errorf("calibration row %d (%s): verdict %q is not APPROVE or HOLD", i+1, row.ref(), row.Verdict)
		case row.Score < 0 || row.Score > 10:
			return CalibSet{}, fmt.Errorf("calibration row %d (%s): score %d is not 0-10", i+1, row.ref(), row.Score)
		}
		if _, err := time.Parse(time.RFC3339, row.ResolvedAt); err != nil {
			return CalibSet{}, fmt.Errorf("calibration row %d (%s): resolved_at %q is not RFC 3339", i+1, row.ref(), row.ResolvedAt)
		}
		set.Rows = append(set.Rows, row)
	}
	if len(set.Rows) == 0 {
		return CalibSet{}, errors.New("the calibration set has no rows")
	}
	sum := sha256.Sum256(raw)
	set.SHA = hex.EncodeToString(sum[:])
	return set, nil
}

// JevLine is Jev's answer on one row under one prompt: PASS, BOUNCE or UNSURE
// and a 1-10 score, 0 when it gave none. It has the Jev ledger's shape.
type JevLine struct {
	Repo    string
	PR      int
	Head    string
	Verdict string
	Score   int
}

// Scorer re-scores calibration rows with one prompt.
type Scorer interface {
	Score(ctx context.Context, promptSHA string, rows []CalibRow) ([]JevLine, error)
}

// ExecScorer runs jev-eval (rowan-tools, REST only) as Argv plus
// --prompt-sha <sha>: the rows go in on stdin as JSONL and one ledger-shaped
// JSON line per row comes back on stdout ({"repo","pr","head","verdict","score"};
// verdict PASS|BOUNCE|UNSURE, APPROVE and HOLD read as PASS and BOUNCE; score a
// number, null or "-").
type ExecScorer struct{ Argv []string }

// Score runs the command once for all rows.
func (e ExecScorer) Score(ctx context.Context, promptSHA string, rows []CalibRow) ([]JevLine, error) {
	if len(e.Argv) == 0 {
		return nil, errors.New("--jev-eval names no command")
	}
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, e.Argv[0], append(append([]string{}, e.Argv[1:]...), "--prompt-sha", promptSHA)...)
	cmd.Stdin = &in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("jev-eval --prompt-sha %s: %v: %s", promptSHA, err, oneline.Escape(strings.TrimSpace(stderr.String())))
	}
	var lines []JevLine
	for i, s := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		var raw struct {
			Repo    string          `json:"repo"`
			PR      int             `json:"pr"`
			Head    string          `json:"head"`
			Verdict string          `json:"verdict"`
			Score   json.RawMessage `json:"score"`
		}
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return nil, fmt.Errorf("jev-eval line %d: %w", i+1, err)
		}
		score := 0
		switch v := strings.Trim(string(raw.Score), `"`); v {
		case "", "null", "-":
		default:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 10 {
				return nil, fmt.Errorf("jev-eval line %d: score %s is not 0-10", i+1, raw.Score)
			}
			score = int(math.Round(f))
		}
		lines = append(lines, JevLine{Repo: raw.Repo, PR: raw.PR, Head: raw.Head, Verdict: raw.Verdict, Score: score})
	}
	return lines, nil
}

// JevCalib asks the fold to calibrate Jev: the set, the prompt Jev runs now,
// an optional candidate, and the scorer (ExecScorer from --jev-eval).
type JevCalib struct {
	Set       CalibSet
	PromptSHA string
	Candidate string
	Scorer    Scorer
}

// JevStats is one work type's numbers under one prompt. A friend "pass" is a
// typed APPROVE scoring at least useful_min (or carrying no score); anything
// else is a hold. UNSURE is never an agreement and never an error here: the
// tag lines count it as a miss.
type JevStats struct {
	Type                                   string
	Heads, Decided, Agree                  int
	FalsePass, FalseBounce, Unsure         int
	Pass, PassRight, Bounce, BounceRight   int
	absSum, absN                           int
	approvedSum, approvedN, heldSum, heldN int
}

func (s JevStats) mae() string {
	if s.absN == 0 {
		return tokens.Dash
	}
	return strconv.FormatFloat(float64(s.absSum)/float64(s.absN), 'f', 2, 64)
}

// sep is Jev's mean score on friend-approved PRs minus its mean on held ones:
// the score gate needs it above zero (on 2026-09-22 it was below).
func (s JevStats) sep() (float64, bool) {
	if s.approvedN == 0 || s.heldN == 0 {
		return 0, false
	}
	return float64(s.approvedSum)/float64(s.approvedN) - float64(s.heldSum)/float64(s.heldN), true
}

func (s JevStats) sepText() string {
	v, ok := s.sep()
	if !ok {
		return tokens.Dash
	}
	return fmt.Sprintf("%+.2f", v)
}

// wilsonLB is the Wilson 95% lower bound of k right out of n.
func wilsonLB(k, n int) (float64, bool) {
	if n == 0 {
		return 0, false
	}
	const z = 1.96
	p, nf := float64(k)/float64(n), float64(n)
	lb := (p + z*z/(2*nf) - z*math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf))) / (1 + z*z/nf)
	return math.Max(0, lb), true
}

func lbText(k, n int) string {
	v, ok := wilsonLB(k, n)
	if !ok {
		return tokens.Dash
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// promote names the verdicts this type clears the bar for, or "none".
func (s JevStats) promote() string {
	sep, ok := s.sep()
	if s.Heads < PromoteMinHeads || !ok || sep <= 0 {
		return "none"
	}
	var p []string
	if lb, ok := wilsonLB(s.PassRight, s.Pass); ok && lb >= PromotePrecisionLB {
		p = append(p, "pass")
	}
	if lb, ok := wilsonLB(s.BounceRight, s.Bounce); ok && lb >= PromotePrecisionLB {
		p = append(p, "bounce")
	}
	if len(p) == 0 {
		return "none"
	}
	return strings.Join(p, "+")
}

// JevTag is a tagged failure shape and how many of its rows Jev missed (did
// not answer the friend's decision; UNSURE on a HOLD is a miss).
type JevTag struct {
	Tag    string
	Heads  int
	Missed []string
}

// JevCandidate is the adoption decision on the held-out third.
type JevCandidate struct {
	SHA                string
	Holdout            int
	Current, Candidate JevStats // totals on the held-out rows
	Adopted            bool
	Reason             string
}

// Calibration is the fold's Jev record.
type Calibration struct {
	PromptSHA string
	SetSHA    string
	Heads     int
	Holdout   int
	Types     []JevStats
	Tags      []JevTag
	Candidate *JevCandidate
}

func friendPass(r CalibRow, usefulMin int) bool {
	return r.Verdict == "APPROVE" && (r.Score == 0 || r.Score >= usefulMin)
}

func jevVerdict(v string) (string, error) {
	switch v {
	case "PASS", "APPROVE":
		return "PASS", nil
	case "BOUNCE", "HOLD":
		return "BOUNCE", nil
	case "UNSURE":
		return "UNSURE", nil
	}
	return "", fmt.Errorf("verdict %q is not PASS, BOUNCE or UNSURE", v)
}

func shortRepo(repo string) string { return repo[strings.LastIndexByte(repo, '/')+1:] }

// scoreRows runs the scorer and returns one line per row, in row order. A row
// with no answer, or an answer at another head, is refused: no evidence is not
// a verdict.
func scoreRows(ctx context.Context, sc Scorer, prompt string, rows []CalibRow) ([]JevLine, error) {
	lines, err := sc.Score(ctx, prompt, rows)
	if err != nil {
		return nil, err
	}
	byRef := map[string]JevLine{}
	for _, l := range lines {
		byRef[shortRepo(l.Repo)+"#"+strconv.Itoa(l.PR)] = l
	}
	out := make([]JevLine, len(rows))
	for i, r := range rows {
		l, ok := byRef[r.ref()]
		if !ok {
			return nil, fmt.Errorf("jev-eval --prompt-sha %s answered no line for %s at %s", prompt, r.ref(), r.Head)
		}
		if !sameSHA(l.Head, r.Head) {
			return nil, fmt.Errorf("jev-eval --prompt-sha %s answered %s at %s, not the friend's head %s", prompt, r.ref(), l.Head, r.Head)
		}
		if l.Verdict, err = jevVerdict(l.Verdict); err != nil {
			return nil, fmt.Errorf("jev-eval --prompt-sha %s on %s: %w", prompt, r.ref(), err)
		}
		out[i] = l
	}
	return out, nil
}

// tally folds rows and Jev's lines into per-type stats (name order) and a total.
func tally(rows []CalibRow, lines []JevLine, usefulMin int) ([]JevStats, JevStats) {
	types := map[string]*JevStats{}
	total := JevStats{Type: "all"}
	for i, r := range rows {
		name := r.WorkType
		if name == "" {
			name = tokens.Dash
		}
		s := types[name]
		if s == nil {
			s = &JevStats{Type: name}
			types[name] = s
		}
		fp, l := friendPass(r, usefulMin), lines[i]
		for _, s := range []*JevStats{s, &total} {
			s.Heads++
			switch l.Verdict {
			case "PASS":
				s.Decided++
				s.Pass++
				if fp {
					s.Agree++
					s.PassRight++
				} else {
					s.FalsePass++
				}
			case "BOUNCE":
				s.Decided++
				s.Bounce++
				if fp {
					s.FalseBounce++
				} else {
					s.Agree++
					s.BounceRight++
				}
			default:
				s.Unsure++
			}
			if l.Score > 0 {
				if r.Score > 0 {
					d := l.Score - r.Score
					if d < 0 {
						d = -d
					}
					s.absSum += d
					s.absN++
				}
				if fp {
					s.approvedSum += l.Score
					s.approvedN++
				} else {
					s.heldSum += l.Score
					s.heldN++
				}
			}
		}
	}
	out := make([]JevStats, 0, len(types))
	for _, s := range types {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out, total
}

// holdout returns the indexes of the newest third of rows by resolved time
// (#2536's date split), oldest first.
func holdout(rows []CalibRow) []int {
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	at := func(i int) time.Time { t, _ := time.Parse(time.RFC3339, rows[i].ResolvedAt); return t }
	sort.SliceStable(idx, func(a, b int) bool {
		ta, tb := at(idx[a]), at(idx[b])
		if !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return rows[idx[a]].ref() < rows[idx[b]].ref()
	})
	n := (len(rows) + 2) / 3
	return idx[len(idx)-n:]
}

// Calibrate runs the calibration: every row under the current prompt, and the
// held-out third under the candidate when one is named.
func Calibrate(ctx context.Context, jc *JevCalib, usefulMin int) (*Calibration, error) {
	if jc.PromptSHA == "" {
		return nil, errors.New("--prompt-sha names the prompt Jev runs now (etc/jev.conf prompt_sha=)")
	}
	if jc.Candidate == jc.PromptSHA {
		return nil, fmt.Errorf("--candidate %s is the current prompt", jc.Candidate)
	}
	rows := jc.Set.Rows
	lines, err := scoreRows(ctx, jc.Scorer, jc.PromptSHA, rows)
	if err != nil {
		return nil, err
	}
	c := &Calibration{PromptSHA: jc.PromptSHA, SetSHA: jc.Set.SHA, Heads: len(rows)}
	c.Types, _ = tally(rows, lines, usefulMin)
	hold := holdout(rows)
	c.Holdout = len(hold)

	tags := map[string]*JevTag{}
	for i, r := range rows {
		if r.Tag == "" {
			continue
		}
		t := tags[r.Tag]
		if t == nil {
			t = &JevTag{Tag: r.Tag}
			tags[r.Tag] = t
		}
		t.Heads++
		want := "BOUNCE"
		if friendPass(r, usefulMin) {
			want = "PASS"
		}
		if lines[i].Verdict != want {
			t.Missed = append(t.Missed, r.ref())
		}
	}
	for _, t := range tags {
		sort.Strings(t.Missed)
		c.Tags = append(c.Tags, *t)
	}
	sort.Slice(c.Tags, func(i, j int) bool { return c.Tags[i].Tag < c.Tags[j].Tag })

	if jc.Candidate == "" {
		return c, nil
	}
	hRows := make([]CalibRow, len(hold))
	hCur := make([]JevLine, len(hold))
	for i, k := range hold {
		hRows[i], hCur[i] = rows[k], lines[k]
	}
	hCand, err := scoreRows(ctx, jc.Scorer, jc.Candidate, hRows)
	if err != nil {
		return nil, err
	}
	curTypes, curTotal := tally(hRows, hCur, usefulMin)
	candTypes, candTotal := tally(hRows, hCand, usefulMin)
	cand := &JevCandidate{SHA: jc.Candidate, Holdout: len(hold), Current: curTotal, Candidate: candTotal, Adopted: true}
	var worse []string
	for i, cur := range curTypes { // same rows, so the same types in the same order
		nw := candTypes[i]
		if nw.FalsePass > cur.FalsePass {
			worse = append(worse, fmt.Sprintf("false_pass %d>%d on %s", cur.FalsePass, nw.FalsePass, cur.Type))
		}
		if nw.FalseBounce > cur.FalseBounce {
			worse = append(worse, fmt.Sprintf("false_bounce %d>%d on %s", cur.FalseBounce, nw.FalseBounce, cur.Type))
		}
	}
	if len(worse) > 0 {
		cand.Adopted, cand.Reason = false, strings.Join(worse, "; ")
	}
	c.Candidate = cand
	return c, nil
}

// PrintJev prints the set line, a line per work type and per tag, and the
// candidate line.
func PrintJev(out io.Writer, sprint string, c *Calibration) {
	fmt.Fprintf(out, "FOLD JEV SET sprint=%s prompt_sha=%s heads=%d holdout=%d set_sha=%s\n",
		sprint, oneline.Field(c.PromptSHA), c.Heads, c.Holdout, c.SetSHA[:12])
	for _, s := range c.Types {
		fmt.Fprintf(out, "FOLD JEV TYPE sprint=%s prompt_sha=%s type=%s heads=%d decided=%d agree=%d/%d false_pass=%d false_bounce=%d unsure=%d mae=%s sep=%s pass_prec_lb=%s bounce_prec_lb=%s promote=%s\n",
			sprint, oneline.Field(c.PromptSHA), oneline.Field(s.Type), s.Heads, s.Decided, s.Agree, s.Decided,
			s.FalsePass, s.FalseBounce, s.Unsure, s.mae(), s.sepText(),
			lbText(s.PassRight, s.Pass), lbText(s.BounceRight, s.Bounce), s.promote())
	}
	for _, t := range c.Tags {
		prs := strings.Join(t.Missed, ",")
		if prs == "" {
			prs = tokens.Dash
		}
		fmt.Fprintf(out, "FOLD JEV TAG sprint=%s prompt_sha=%s tag=%s heads=%d missed=%d prs=%s\n",
			sprint, oneline.Field(c.PromptSHA), oneline.Field(t.Tag), t.Heads, len(t.Missed), prs)
	}
	if k := c.Candidate; k != nil {
		adopted := "yes"
		if !k.Adopted {
			adopted = "no reason=" + oneline.Field(k.Reason)
		}
		fmt.Fprintf(out, "FOLD JEV CANDIDATE sprint=%s candidate=%s current=%s holdout=%d false_pass=%d>%d false_bounce=%d>%d agree=%d>%d adopted=%s\n",
			sprint, oneline.Field(k.SHA), oneline.Field(c.PromptSHA), k.Holdout,
			k.Current.FalsePass, k.Candidate.FalsePass, k.Current.FalseBounce, k.Candidate.FalseBounce,
			k.Current.Agree, k.Candidate.Agree, adopted)
	}
}

func jevSexp(c *Calibration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  :jev (:prompt-sha %s :set-sha %s :heads %d :holdout %d\n", q(c.PromptSHA), q(c.SetSHA), c.Heads, c.Holdout)
	b.WriteString("        :types (")
	for i, s := range c.Types {
		if i > 0 {
			b.WriteString("\n                ")
		}
		fmt.Fprintf(&b, "(type %s :heads %d :decided %d :agree %d :false-pass %d :false-bounce %d :unsure %d :mae %s :sep %s :pass-prec-lb %s :bounce-prec-lb %s :promote %s)",
			q(s.Type), s.Heads, s.Decided, s.Agree, s.FalsePass, s.FalseBounce, s.Unsure, q(s.mae()), q(s.sepText()),
			q(lbText(s.PassRight, s.Pass)), q(lbText(s.BounceRight, s.Bounce)), q(s.promote()))
	}
	b.WriteString(")\n        :tags (")
	for i, t := range c.Tags {
		if i > 0 {
			b.WriteString("\n               ")
		}
		prs := make([]string, len(t.Missed))
		for j, p := range t.Missed {
			prs[j] = q(p)
		}
		fmt.Fprintf(&b, "(tag %s :heads %d :missed %d :prs (%s))", q(t.Tag), t.Heads, len(t.Missed), strings.Join(prs, " "))
	}
	b.WriteString(")")
	if k := c.Candidate; k != nil {
		adopted := "yes"
		if !k.Adopted {
			adopted = "no"
		}
		fmt.Fprintf(&b, "\n        :candidate (:sha %s :holdout %d :current (:false-pass %d :false-bounce %d :agree %d) :candidate (:false-pass %d :false-bounce %d :agree %d) :adopted %s :reason %s)",
			q(k.SHA), k.Holdout, k.Current.FalsePass, k.Current.FalseBounce, k.Current.Agree,
			k.Candidate.FalsePass, k.Candidate.FalseBounce, k.Candidate.Agree, q(adopted), q(k.Reason))
	}
	b.WriteString(")\n")
	return b.String()
}

// scoreAtLeast: a read card's own score (a bare number or n/10) at or above min.
func scoreAtLeast(s string, min int) bool {
	score, _, _ := strings.Cut(strings.TrimSpace(s), "/")
	n, err := strconv.Atoi(score)
	return err == nil && n >= min
}
