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
	ledger := fs.String("ledger", "file", "where the verdict is appended: file | redis")
	ledgerPath := fs.String("ledger-path", defaultLedgerPath(), "the JSONL ledger, for --ledger file")
	noJev := fs.Bool("no-jev", false, "run the four mechanical checks only; ask no provider and spend nothing")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "the Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "the environment variable holding the key")
	record := fs.String("record", "", "write each provider response to this directory as a fixture")
	replay := fs.String("replay", "", "replay recorded fixtures from this directory instead of dialling the provider")
	ghPath := fs.String("gh", "gh", "the gh executable")
	table := fs.Bool("table", false, "also print one table row per pull request")
	def := prereview.DefaultTuning()
	passAbove := fs.Int("pass-above", def.PassAbove, "a score strictly above this can PASS")
	bounceBelow := fs.Int("bounce-below", def.BounceBelow, "a score strictly below this BOUNCEs")
	checksList := fs.String("checks", strings.Join(prereview.CheckNames, ","), "the checks that may decide: donewhen,selfcheck,paths,claims,score")
	inRate := fs.Float64("usd-per-mtok-in", 0, "the provider's input rate, US dollars per million tokens; 0 is unknown and prints cost=$-")
	outRate := fs.Float64("usd-per-mtok-out", 0, "the provider's output rate, US dollars per million tokens; 0 is unknown")
	skipHeads := fs.String("skip-heads", "", "a file of head shas already posted on; a pull request at one of them is skipped before any call")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
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
	if *batch != "" && *cardPath != "" {
		return refuse(stderr, "REVIEW", "bad-arguments", "--card names one card and --batch is many pull requests; give --card with --pr")
	}
	switch *ledger {
	case "file":
	case "redis":
		// #2563 is the ev:cards stream and its Emit. Until it is on dev there
		// is nothing to call, and a sink that silently does nothing is worse
		// than one that says so.
		return refuse(stderr, "REVIEW", "no-ledger",
			"--ledger redis wants the ev:cards Emit from nova-tools #2563, which is not on dev yet; use --ledger file (the JSONL) and re-point this when #2563 lands")
	default:
		return refuse(stderr, "REVIEW", "bad-arguments", "--ledger is file or redis, got "+oneline.Field(*ledger))
	}

	enabled, err := prereview.ParseEnabled(*checksList)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-arguments", "--checks: "+oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if *bounceBelow > *passAbove+1 {
		return refuse(stderr, "REVIEW", "bad-arguments", fmt.Sprintf("--bounce-below %d is above --pass-above %d plus one; a score could both PASS and BOUNCE", *bounceBelow, *passAbove))
	}
	tune := prereview.Tuning{PassAbove: *passAbove, BounceBelow: *bounceBelow, Enabled: enabled,
		Model: decide.DefaultModel, USDPerMTokIn: *inRate, USDPerMTokOut: *outRate}
	if *noJev {
		tune.Model = "none"
	}
	skip, err := readHeads(*skipHeads)
	if err != nil {
		return refuse(stderr, "REVIEW", "bad-arguments", "--skip-heads: "+oneline.Cap(err.Error(), oneline.TailBytes))
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

	gh := ghRunner{path: *ghPath}
	held := 0
	for _, n := range numbers {
		d, err := reviewOne(gh, *repo, n, *cardPath, asker, usage, tune, skip, *record, posting, stdout, stderr)
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
		if err := prereview.AppendLedger(*ledgerPath, d); err != nil {
			fmt.Fprintf(stderr, "REVIEW LEDGER FAILED pr=%d reason=%s\n", n, oneline.Err(err))
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
func reviewOne(gh ghRunner, repo string, n int, cardPath string, asker prereview.Asker, usage *decide.Usage, tune prereview.Tuning, skip map[string]bool, record string, posting bool, stdout, stderr io.Writer) (prereview.Disposition, error) {
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
	d := prereview.Disposition{
		Repo: repo, PR: n, Head: pr.Head,
		Checks: checks.Field(), Reason: checks.Why(), Evidence: checks.Evidence(), Model: tune.Model,
		PathsFrom: card.PathsFrom, SymbolFrom: card.SymbolFrom, CardPath: card.Path,
		At: time.Now().UTC().Format(time.RFC3339),
	}
	if asker != nil && tune.Enabled["score"] {
		*usage = decide.Usage{}
		raw, conf, err := prereview.Score(context.Background(), asker, pr, card)
		d.InTokens, d.OutTokens, d.UsageKnown = usage.InputTokens, usage.OutputTokens, usage.Known()
		if err != nil {
			d.Reason = "score unavailable (" + oneline.Err(err) + "); " + d.Reason
		} else {
			d.RawScore, d.Conf, d.Score, d.Scored = raw, conf, prereview.ScoreFromAnswer(raw), true
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
		d.Evidence = append(d.Evidence, fmt.Sprintf("score: %d -- raw %.2f, confidence %.2f, one %s question over the RESULT and the diff", d.Score, d.RawScore, d.Conf, tune.Model))
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

// ghRunner is the one thing this verb shells out to. It is a struct so a test
// can point it at a script and never touch the network.
type ghRunner struct{ path string }

// prWire is the subset of `gh pr view --json` this pass reads.
type prWire struct {
	Number     int    `json:"number"`
	HeadRefOid string `json:"headRefOid"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Files      []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// pullRequest fetches the public facts and the diff.
func (g ghRunner) pullRequest(repo string, n int) (prereview.PR, error) {
	raw, err := g.run("pr", "view", strconv.Itoa(n), "-R", repo, "--json", "number,headRefOid,title,body,files")
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
	pr := prereview.PR{Repo: repo, Number: n, Head: w.HeadRefOid, Title: w.Title, Body: w.Body, Diff: string(diff)}
	for _, f := range w.Files {
		pr.Files = append(pr.Files, f.Path)
	}
	sort.Strings(pr.Files)
	if len(pr.Files) == 0 {
		pr.Files = prereview.DiffFiles(pr.Diff)
	}
	return pr, nil
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
