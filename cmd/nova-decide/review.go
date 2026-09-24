package main

import (
	"bufio"
	"context"
	"encoding/json"
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

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/jevcalib"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// runReview is the Jev FIRST PASS over a pull request (nova-tools #2565).
//
// It fetches the diff and the card, runs four mechanical checks in Go with no
// model, asks Jev ONE question for a 1-10 score, prints one typed DISPOSITION
// line and appends the verdict to a ledger. It never lands anything: the
// lander counts a typed line only when the GitHub ACCOUNT that posted it matches
// its FRIENDS regex, and the account this pass writes under is not a friend's.
//
// Exit 0 when every pull request cleared, 3 when any HOLD (a suggestion to
// recut, never an authorization), 2 on refusal.
func runReview(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide review", flag.ContinueOnError)
	repo := fs.String("repo", "", "owner/name of the repository (required)")
	pr := fs.Int("pr", 0, "the pull request number")
	batch := fs.String("batch", "", "a file of pull request numbers, one a line; # and blank lines skipped")
	cardPath := fs.String("card", "", "the card file; when absent PATHS and SYMBOL are inferred from the pull request body")
	post := fs.Bool("post", false, "post the typed line on the pull request as a comment")
	dryRun := fs.Bool("dry-run", false, "print the typed line and post nothing (the default)")
	ledger := fs.String("ledger", "file", "where the verdict is written: file | redis | file,redis (redis is one kind=jev entry on cards:done per JEV line)")
	store := fs.String("store", os.Getenv("NOVA_REDIS_ADDR"), "the fleet Redis host:port, for --ledger redis (env NOVA_REDIS_ADDR)")
	var storeUser string
	fs.StringVar(&storeUser, "user", "", "the Redis ACL user, for --ledger redis")
	fs.StringVar(&storeUser, "store-user", "", "alias for --user")
	var storePasswordEnv string
	fs.StringVar(&storePasswordEnv, "password-env", "NOVA_REDIS_BENCH_PASSWORD", "the environment variable holding the Redis password; the password is never a flag")
	fs.StringVar(&storePasswordEnv, "store-password-env", "NOVA_REDIS_BENCH_PASSWORD", "alias for --password-env")
	stream := fs.String("stream", events.Stream, "the stream the Jev entries go to")
	ledgerPath := fs.String("ledger-path", defaultLedgerPath(), "the JSONL ledger, for --ledger file")
	noJev := fs.Bool("no-jev", false, "run the four mechanical checks only; ask no provider and spend nothing")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "the Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "the environment variable holding the key")
	record := fs.String("record", "", "write each provider response to this directory as a fixture")
	replay := fs.String("replay", "", "replay recorded fixtures from this directory instead of dialling the provider")
	ghPath := fs.String("gh", "gh", "the gh executable")
	table := fs.Bool("table", false, "also print one table row per pull request")
	def := prereview.DefaultTuning()
	passAbove := fs.Int("pass-above", def.PassAbove, "a score strictly above this can PASS; when absent, the prompt's own threshold (the default prompt's is jevcalib.DefaultPassAbove, 8), else 7")
	bounceBelow := fs.Int("bounce-below", def.BounceBelow, "a score strictly below this BOUNCEs")
	checksList := fs.String("checks", strings.Join(prereview.DefaultChecks, ","), "the checks that may decide (checks_enabled): donewhen,selfcheck,paths,claims,score; name ci to require ci-ok at the exact head")
	inRate := fs.Float64("usd-per-mtok-in", 0, "the provider's input rate, US dollars per million tokens; 0 is unknown and prints cost=$-")
	outRate := fs.Float64("usd-per-mtok-out", 0, "the provider's output rate, US dollars per million tokens; 0 is unknown")
	promptRef := fs.String("prompt", "", "the score question's prompt: a file path or an embedded sha8 (internal/jevcalib/prompts); default: prompt= in --conf, else the embedded default")
	confPath := fs.String("conf", defaultJevConf(), "the jev.conf whose prompt= key names the prompt when --prompt is absent (default $NOVA_JEV_CONF, e.g. ~/rowan-working/etc/jev.conf; unset or none reads no conf)")
	prDir := fs.String("pr-dir", "", "read each pull request from <dir>/<n>/ (view.json in the gh pr view --json shape, diff.txt, optional check-runs.json) instead of gh: the calibration dry run, no GitHub call; refused with --post")
	skipHeads := fs.String("skip-heads", "", "a file of head shas already posted on; a pull request at one of them is skipped before any call")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "review") {
			return 0
		}
		return refuse(stderr, "REVIEW", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "REVIEW", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*repo) == "" {
		return refuse(stderr, "REVIEW", "bad-arguments", "--repo owner/name is required; refusing to guess a repository")
	}
	if (*pr > 0) == (strings.TrimSpace(*batch) != "") {
		return refuse(stderr, "REVIEW", "bad-arguments", "give exactly one of --pr <n> and --batch <file>")
	}
	if *post && *dryRun {
		return refuse(stderr, "REVIEW", "bad-arguments", "--post and --dry-run are the two halves of one choice; give one")
	}
	// Posting is the only act this verb has, so it is the one that must be
	// asked for by name. The default is the dry run.
	posting := *post
	if posting && strings.TrimSpace(*prDir) != "" {
		return refuse(stderr, "REVIEW", "bad-arguments", "--pr-dir reads cached pull requests for a dry run; it never posts, so --post is refused with it")
	}
	if *batch != "" && *cardPath != "" {
		return refuse(stderr, "REVIEW", "bad-arguments", "--card names one card and --batch is many pull requests; give --card with --pr")
	}
	toFile, toRedis, err := ledgerSinks(*ledger)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-arguments", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if toRedis && strings.TrimSpace(*store) == "" {
		return refuse(stderr, "REVIEW", "no-ledger",
			"--ledger redis wants --store host:port (or NOVA_REDIS_ADDR), the fleet Redis cards:done lives on; refusing to guess one")
	}

	enabled, err := prereview.ParseEnabled(*checksList)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-arguments", "--checks: "+oneline.Cap(err.Error(), oneline.TailBytes))
	}
	skip, err := readHeads(*skipHeads)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-arguments", "--skip-heads: "+oneline.Cap(err.Error(), oneline.TailBytes))
	}

	prompt, err := reviewPrompt(*promptRef, *confPath)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-prompt", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	// A prompt and its pass threshold are tuned together (#2536): the default
	// prompt passes only above jevcalib.DefaultPassAbove, because at 7 it
	// passed 13 of the 128 heads the friends held (10.2%). An explicit
	// --pass-above always wins.
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if !explicit["pass-above"] {
		*passAbove = jevcalib.PassAboveFor(prompt, def.PassAbove)
	}
	if *bounceBelow > *passAbove+1 {
		return refuse(stderr, "REVIEW", "bad-arguments", fmt.Sprintf("--bounce-below %d is above --pass-above %d plus one; a score could both PASS and BOUNCE", *bounceBelow, *passAbove))
	}
	tune := prereview.Tuning{PassAbove: *passAbove, BounceBelow: *bounceBelow, Enabled: enabled,
		Model: decide.DefaultModel, USDPerMTokIn: *inRate, USDPerMTokOut: *outRate}
	if *noJev {
		tune.Model = "none"
	}

	numbers, err := reviewTargets(*pr, *batch)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-arguments", oneline.Cap(err.Error(), oneline.TailBytes))
	}

	var asker prereview.Asker
	usage := &decide.Usage{}
	switch {
	case *noJev:
		asker = nil
	case *replay != "":
		asker = prereview.FixtureAsker{Dir: *replay, Repo: *repo}
	default:
		client, err := decide.New(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, "REVIEW", "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		asker = prereview.ClientAsker{Client: client, Last: usage}
	}

	var jevStream events.Emitter
	if toRedis {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		em, closeStream, err := dialJevStream(ctx, events.Dial{Addr: *store, Username: storeUser,
			Password: os.Getenv(storePasswordEnv), Stream: *stream})
		cancel()
		if err != nil {
			return refuse(stderr, "REVIEW", "no-ledger", "--ledger redis: "+oneline.Cap(err.Error(), oneline.TailBytes))
		}
		defer func() { _ = closeStream() }()
		jevStream = em
	}

	var src prSource = ghRunner{path: *ghPath}
	if strings.TrimSpace(*prDir) != "" {
		src = dirSource{dir: *prDir}
	}
	held := 0
	for _, n := range numbers {
		d, err := reviewOne(src, *repo, n, *cardPath, asker, usage, tune, prompt, skip, *record, posting, stdout, stderr)
		if err == errSkipped {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "REVIEW REFUSED pr=%d reason=%s\n", n, oneline.Err(err))
			held++
			continue
		}
		if *table {
			fmt.Fprintln(stdout, tableRow(d))
		}
		if toFile {
			if err := prereview.AppendLedger(*ledgerPath, d); err != nil {
				fmt.Fprintf(stderr, "REVIEW LEDGER FAILED pr=%d reason=%s\n", n, oneline.Err(err))
			}
		}
		if jevStream != nil {
			if err := streamJevLine(jevStream, d); err != nil {
				fmt.Fprintf(stderr, "REVIEW STREAM FAILED pr=%d reason=%s\n", n, oneline.Err(err))
				held++
			}
		}
		if d.Verdict != prereview.Pass {
			held++
		}
	}
	if held > 0 {
		return 3
	}
	return 0
}

// ledgerSinks reads --ledger: file, redis, or both as a comma list.
func ledgerSinks(list string) (toFile, toRedis bool, err error) {
	for _, raw := range strings.Split(list, ",") {
		switch strings.TrimSpace(raw) {
		case "file":
			toFile = true
		case "redis":
			toRedis = true
		default:
			return false, false, fmt.Errorf("--ledger is file, redis or file,redis, got %s", oneline.Field(list))
		}
	}
	return toFile, toRedis, nil
}

// dialJevStream opens the stream the Jev lines go to. It is a variable so a
// test swaps in the in-memory events.FakeStream and dials nothing.
var dialJevStream = func(ctx context.Context, d events.Dial) (events.Emitter, func() error, error) {
	s, err := events.Open(ctx, d)
	if err != nil {
		return nil, nil, err
	}
	return s, s.Close, nil
}

// streamJevLine writes the JEV line just printed to the ledger stream: one
// kind=jev entry, the same head and the same score the line carries.
func streamJevLine(em events.Emitter, d prereview.Disposition) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := decide.WriteJevLedger(ctx, em, decide.JevVerdict{Repo: d.Repo, PR: d.PR, Head: d.Head,
		Verdict: string(d.Verdict), Score: d.Score, Scored: d.Scored, Model: d.Model})
	return err
}

// defaultJevConf is the conf review reads prompt= from when --conf is absent:
// $NOVA_JEV_CONF (on the Studio, ~/rowan-working/etc/jev.conf, the file
// bin/jev-loop is tuned by), else none. The verb never assumes a home layout.
func defaultJevConf() string {
	if v := strings.TrimSpace(os.Getenv("NOVA_JEV_CONF")); v != "" {
		return v
	}
	return "none"
}

// reviewPrompt is the prompt the score question is asked with: --prompt, else
// the conf's prompt= key, else the embedded default. A prompt that is named
// and does not resolve is a refusal, never a silent fall back to the default:
// a tuning that says prompt X must score with X or not at all.
func reviewPrompt(flagRef, conf string) (jevcalib.Prompt, error) {
	ref := strings.TrimSpace(flagRef)
	if ref == "" && conf != "" && conf != "none" {
		v, err := jevcalib.ConfPrompt(conf)
		if err != nil {
			return jevcalib.Prompt{}, fmt.Errorf("--conf %s: %w", conf, err)
		}
		ref = v
	}
	if ref == "" {
		return jevcalib.Default(), nil
	}
	return jevcalib.Resolve(ref)
}

// defaultLedgerPath is the JSONL ledger today. It is under the coordinator's
// session directory, beside the other lanes' receipts.
func defaultLedgerPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "jev-ledger.jsonl"
	}
	return filepath.Join(home, "rowan-working", "tmp", "session-0919b", "jev-ledger.jsonl")
}

// reviewTargets is the pull request numbers to pass over.
func reviewTargets(pr int, batch string) ([]int, error) {
	if pr > 0 {
		return []int{pr}, nil
	}
	f, err := os.Open(batch)
	if err != nil {
		return nil, fmt.Errorf("--batch: %w", err)
	}
	defer f.Close()
	out := make([]int, 0)
	seen := map[int]bool{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		t := strings.TrimSpace(sc.Text())
		// `#` starts a comment, EXCEPT when a digit follows it: a list of pull
		// requests written the way GitHub writes them (`#1556`) is the obvious
		// thing to paste into this file, and a parser that silently read those
		// as comments would run a short batch and say nothing about it.
		if t == "" || (strings.HasPrefix(t, "#") && !(len(t) > 1 && t[1] >= '0' && t[1] <= '9')) {
			continue
		}
		t = strings.TrimPrefix(strings.Fields(t)[0], "#")
		n, err := strconv.Atoi(t)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("--batch line %d: %q is not a pull request number", line, sc.Text())
		}
		if !seen[n] {
			seen[n], out = true, append(out, n)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("--batch: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--batch %s names no pull request", batch)
	}
	return out, nil
}

// reviewOne is the pass over one pull request.
func reviewOne(gh prSource, repo string, n int, cardPath string, asker prereview.Asker, usage *decide.Usage, tune prereview.Tuning, prompt jevcalib.Prompt, skip map[string]bool, record string, posting bool, stdout, stderr io.Writer) (prereview.Disposition, error) {
	pr, err := gh.pullRequest(repo, n)
	if err != nil {
		return prereview.Disposition{}, err
	}
	if skip[pr.Head] {
		fmt.Fprintf(stdout, "JEV SKIP pr=%d head=%s reason=already-posted-at-this-head\n", n, oneline.Field(pr.Head))
		return prereview.Disposition{}, errSkipped
	}
	card := prereview.InferCard(pr)
	if strings.TrimSpace(cardPath) != "" {
		body, err := os.ReadFile(cardPath)
		if err != nil {
			return prereview.Disposition{}, fmt.Errorf("--card: %w", err)
		}
		card = prereview.ParseCard(cardPath, string(body))
		// A card that declared no PATHS is not a card that declared "any
		// path": the inferred bound still stands under it, and the line says
		// which one answered.
		if len(card.Paths) == 0 {
			inferred := prereview.InferCard(pr)
			card.Paths, card.PathsFrom = inferred.Paths, inferred.PathsFrom
		}
	}
	checks := prereview.Mechanical(pr, card)
	base := pr.Base
	if base == "" {
		base = string(prereview.BaseGateFromGH(pr.Mergeable, pr.MergeStateStatus))
	}
	d := prereview.Disposition{
		Who:  prereview.Who,
		Repo: repo, PR: n, Head: pr.Head,
		Rubric: prereview.LevelsVersion(prompt.Levels), Prompt: prompt.Sha8, Base: base,
		Checks: checks.Field(), Reason: checks.Why(), Evidence: checks.Evidence(), Model: tune.Model,
		PathsFrom: card.PathsFrom, SymbolFrom: card.SymbolFrom, CardPath: card.Path,
		At: time.Now().UTC().Format(time.RFC3339),
	}
	if asker != nil && tune.Enabled["score"] {
		*usage = decide.Usage{}
		raw, conf, err := prereview.ScoreWith(context.Background(), asker, prompt.Question(), pr, card)
		d.InTokens, d.OutTokens, d.UsageKnown = usage.InputTokens, usage.OutputTokens, usage.Known()
		if err != nil {
			d.Reason = "score unavailable (" + oneline.Err(err) + "); " + d.Reason
		} else {
			d.RawScore, d.Conf, d.Score, d.Scored = raw, conf, tune.GateCap(checks, prereview.ScoreFromAnswer(raw)), true
			if record != "" {
				if err := prereview.RecordFixture(record, repo, n, raw, conf); err != nil {
					fmt.Fprintf(stderr, "REVIEW RECORD FAILED pr=%d reason=%s\n", n, oneline.Err(err))
				}
			}
		}
	}
	d.Verdict, d.Explain = tune.Decide(checks, d.Score, d.Scored)
	d.Checks = tune.ChecksField(checks, d.Score, d.Scored)
	if d.Scored {
		d.Evidence = append(d.Evidence, fmt.Sprintf("score: %d -- raw %.2f, confidence %.2f, one %s question (prompt %s) over the body and the diff", d.Score, d.RawScore, d.Conf, tune.Model, prompt.Sha8))
	} else {
		d.Evidence = append(d.Evidence, "score: - -- "+scoreWhy(asker, tune, d.Reason))
	}
	tune.Cost(&d)
	fmt.Fprintln(stdout, d.Line())
	if posting {
		if err := gh.comment(repo, n, d.Comment()); err != nil {
			return d, fmt.Errorf("posting the line: %w", err)
		}
		d.Posted = true
	}
	return d, nil
}

// tableRow is the DONE-WHEN run's row: one pull request across the four checks,
// the score and the verdict.
func tableRow(d prereview.Disposition) string {
	score := "-"
	if d.Scored {
		score = strconv.Itoa(d.Score)
	}
	return fmt.Sprintf("ROW\t%d\t%s\t%s\t%s", d.PR, d.Checks, score, d.Verdict)
}

// prSource is where a pass reads a pull request and, with --post, writes its
// one line: gh (the default) or a directory of cached pull requests (--pr-dir).
type prSource interface {
	pullRequest(repo string, n int) (prereview.PR, error)
	comment(repo string, n int, body string) error
}

// dirSource reads pull requests cached under dir/<n>/ (nova-tools #2536: the
// calibration set is scored through the real review path with no GitHub call).
// view.json is the `gh pr view --json` document (baseRefName and mergeable
// optional), diff.txt the unified diff, check-runs.json the commit check-runs
// document at the head; without it the ci check has nothing to read (missing,
// not a bounce). It never writes: comment refuses.
type dirSource struct{ dir string }

func (s dirSource) pullRequest(repo string, n int) (prereview.PR, error) {
	d := filepath.Join(s.dir, strconv.Itoa(n))
	raw, err := os.ReadFile(filepath.Join(d, "view.json"))
	if err != nil {
		return prereview.PR{}, fmt.Errorf("--pr-dir %d: %w", n, err)
	}
	var w prWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return prereview.PR{}, fmt.Errorf("--pr-dir %d: view.json: %w", n, err)
	}
	if w.Number != 0 && w.Number != n {
		return prereview.PR{}, fmt.Errorf("--pr-dir %d: view.json is pull request %d", n, w.Number)
	}
	diff, err := os.ReadFile(filepath.Join(d, "diff.txt"))
	if err != nil {
		return prereview.PR{}, fmt.Errorf("--pr-dir %d: %w", n, err)
	}
	pr := w.pr(repo, n, string(diff))
	runs, err := os.ReadFile(filepath.Join(d, "check-runs.json"))
	switch {
	case os.IsNotExist(err):
		pr.ChecksUnread = true
	case err != nil:
		return prereview.PR{}, fmt.Errorf("--pr-dir %d: %w", n, err)
	default:
		if pr.Checks, _, err = prereview.ParseCheckRollup(runs); err != nil {
			return prereview.PR{}, fmt.Errorf("--pr-dir %d: check-runs.json: %w", n, err)
		}
	}
	return pr, nil
}

func (s dirSource) comment(string, int, string) error {
	return fmt.Errorf("--pr-dir is a dry run: it has nowhere to post")
}

// pr is the wire document as the pass's PR, the one conversion both sources use.
func (w prWire) pr(repo string, n int, diff string) prereview.PR {
	pr := prereview.PR{
		Repo: repo, Number: n, Head: w.HeadRefOid, Title: w.Title, Body: w.Body, Diff: diff,
		BaseRef:          w.BaseRefName,
		Base:             string(prereview.BaseGateFromGH(w.Mergeable, w.MergeStateStatus)),
		Mergeable:        w.Mergeable,
		MergeStateStatus: w.MergeStateStatus,
	}
	for _, f := range w.Files {
		pr.Files = append(pr.Files, f.Path)
	}
	sort.Strings(pr.Files)
	if len(pr.Files) == 0 {
		pr.Files = prereview.DiffFiles(pr.Diff)
	}
	return pr
}

// ghRunner is the one thing this verb shells out to. It is a struct so a test
// can point it at a script and never touch the network.
type ghRunner struct{ path string }

// prWire is the subset of `gh pr view --json` this pass reads.
type prWire struct {
	Number     int    `json:"number"`
	HeadRefOid string `json:"headRefOid"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	// BaseRefName is the branch the pull request targets; the base check
	// reads a stacked base (not the repository's trunk) as a gate failure.
	BaseRefName string `json:"baseRefName"`
	Files       []struct {
		Path string `json:"path"`
	} `json:"files"`
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"mergeStateStatus"`
}

// pullRequest fetches the public facts and the diff.
func (g ghRunner) pullRequest(repo string, n int) (prereview.PR, error) {
	raw, err := g.run("pr", "view", strconv.Itoa(n), "-R", repo, "--json", "number,headRefOid,title,body,baseRefName,files,mergeable,mergeStateStatus")
	if err != nil {
		return prereview.PR{}, fmt.Errorf("gh pr view %d: %w", n, err)
	}
	var w prWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return prereview.PR{}, fmt.Errorf("gh pr view %d: decode: %w", n, err)
	}
	diff, err := g.run("pr", "diff", strconv.Itoa(n), "-R", repo)
	if err != nil {
		return prereview.PR{}, fmt.Errorf("gh pr diff %d: %w", n, err)
	}
	pr := w.pr(repo, n, string(diff))
	if err := g.attachChecks(&pr); err != nil {
		return prereview.PR{}, err
	}
	return pr, nil
}

// attachChecks reads the commit's check rollup onto pr. A page is 100 runs;
// the head's own runs are what ci decides on, never another sha's.
func (g ghRunner) attachChecks(pr *prereview.PR) error {
	sha := strings.TrimSpace(pr.Head)
	if !prereview.ValidHeadSHA(sha) {
		return fmt.Errorf("head %q is not a commit sha, so the check rollup cannot be read at the exact head", oneline.Field(sha))
	}
	var all []prereview.CheckRun
	for page := 1; page <= 20; page++ {
		raw, err := g.run("api", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100&page=%d", pr.Repo, sha, page))
		if err != nil {
			return fmt.Errorf("gh check-runs %s: %w", sha, err)
		}
		runs, total, err := prereview.ParseCheckRollup(raw)
		if err != nil {
			return fmt.Errorf("gh check-runs %s: %w", sha, err)
		}
		all = append(all, runs...)
		if len(runs) == 0 || len(all) >= total || len(runs) < 100 {
			break
		}
	}
	pr.Checks = all
	return nil
}

// comment posts the one line. It is the ONLY write this verb makes, and it is
// reached only with --post.
func (g ghRunner) comment(repo string, n int, body string) error {
	_, err := g.run("pr", "comment", strconv.Itoa(n), "-R", repo, "--body", body)
	return err
}

func (g ghRunner) run(args ...string) ([]byte, error) {
	cmd := exec.Command(g.path, args...)
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, oneline.Cap(strings.TrimSpace(errb.String()), 200))
	}
	return out, nil
}

// errSkipped is a pull request whose head this pass already posted on.
var errSkipped = fmt.Errorf("skipped: already posted at this head")

// readHeads reads a file of head shas, one a line; an absent file is empty.
func readHeads(path string) (map[string]bool, error) {
	out := map[string]bool{}
	if strings.TrimSpace(path) == "" {
		return out, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			out[f[0]] = true
		}
	}
	return out, nil
}

// scoreWhy says why a line carries no score.
func scoreWhy(asker prereview.Asker, tune prereview.Tuning, reason string) string {
	switch {
	case !tune.Enabled["score"]:
		return "the score is off in this tuning"
	case asker == nil:
		return "no provider was asked (--no-jev)"
	default:
		return "the provider did not answer: " + reason
	}
}
