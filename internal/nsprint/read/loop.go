package read

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// LoopOptions configure a review loop pass or daemon (nova-tools #4351 Item D).
type LoopOptions struct {
	Stream       string           // required work stream name
	Daemon       bool             // whether to run indefinitely
	Actor        string           // who runs the review loop (default "coordinator")
	Epoch        uint64           // sprint epoch (default 0)
	PollInterval time.Duration    // sleep interval in daemon mode (default 1s)
	Now          func() time.Time // clock injection for tests (default time.Now)
	OnPass       func()           // callback invoked after each pass
}

// FixCard records one fix card cut by the review loop from a read finding.
type FixCard struct {
	ID      string `json:"id"`
	Primary string `json:"primary"`
	Repo    string `json:"repo"`
	PR      string `json:"pr"`
	Head    string `json:"head"`
	Score   int    `json:"score"`
	Finding string `json:"finding"`
}

// LoopResult records the summary of what a single pass of the review loop did.
type LoopResult struct {
	Stream   string
	DutyCard string
	PRs      int
	Scores   int
	Fixes    []FixCard
}

// DutyCardID returns the duty card identifier for stream.
func DutyCardID(stream string) string {
	return "duty:review-loop:" + ws.Slug(stream)
}

// EnsureDutyCard ensures the review loop duty card is placed in ws:<stream>:working
// and touches its beat, so the table shows the review loop active.
func EnsureDutyCard(ctx context.Context, c *redis.Client, stream, actor string, epoch uint64, now time.Time) (string, error) {
	id := DutyCardID(stream)
	exists, err := c.Exists(ctx, "task:"+id).Result()
	if err != nil {
		return id, fmt.Errorf("check duty card %s: %w", id, err)
	}
	if exists == 0 {
		_, err = taskcard.Push(ctx, c, taskcard.PushRequest{
			ID:     id,
			Where:  ws.Ready,
			Stream: stream,
			Friend: actor,
			Kind:   "duty",
			Title:  "duty: review loop " + stream,
			By:     actor,
			Why:    "review loop active",
			Fields: []string{
				"actor", actor,
				"created_at", strconv.FormatInt(now.UnixMilli(), 10),
				"beat_at", strconv.FormatInt(now.UnixMilli(), 10),
			},
		})
		if err != nil {
			return id, fmt.Errorf("push duty card %s: %w", id, err)
		}
	}

	where := c.HGet(ctx, "task:"+id, "where").Val()
	if where != ws.Working {
		_, err = taskcard.Move(ctx, c, id, ws.Working, taskcard.Opts{
			By:  actor,
			Why: "review loop duty",
			As:  actor,
		})
		if err != nil {
			return id, fmt.Errorf("move duty card %s to working: %w", id, err)
		}
	} else {
		_ = TouchDutyCard(ctx, c, stream, now)
	}
	return id, nil
}

// TouchDutyCard updates beat_at on the duty card.
func TouchDutyCard(ctx context.Context, c *redis.Client, stream string, now time.Time) error {
	id := DutyCardID(stream)
	actor := c.HGet(ctx, "task:"+id, "friend").Val()
	if actor == "" {
		actor = c.HGet(ctx, "task:"+id, "actor").Val()
	}
	if actor == "" {
		actor = "coordinator"
	}
	_, err := taskcard.Move(ctx, c, id, ws.Working, taskcard.Opts{
		By:     actor,
		Why:    "beat",
		Fields: []string{"beat_at", strconv.FormatInt(now.UnixMilli(), 10)},
	})
	return err
}

// ClearDutyCard removes the duty card from ws:<stream>:working and marks it done.
func ClearDutyCard(ctx context.Context, c *redis.Client, stream string, epoch uint64) error {
	id := DutyCardID(stream)
	actor := c.HGet(ctx, "task:"+id, "friend").Val()
	if actor == "" {
		actor = c.HGet(ctx, "task:"+id, "actor").Val()
	}
	if actor == "" {
		actor = "coordinator"
	}
	_, err := taskcard.Done(ctx, c, id, actor, "review loop pass complete", "")
	return err
}

var bulletPrefixRx = regexp.MustCompile(`^(?:[0-9]+[.)]|-|\*)\s*`)

// ExtractFindings parses a typed SCORE or HOLD line into score, findings, who, and head.
// If the line has no valid score, score is -1.
func ExtractFindings(text string) (score int, findings []string, who, head string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return -1, nil, "", ""
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	first := strings.TrimSpace(lines[0])
	tok := strings.Fields(first)
	if len(tok) == 0 {
		return -1, nil, "", ""
	}
	kind := tok[0]
	if kind != "SCORE" && kind != "HOLD" {
		return -1, nil, "", ""
	}

	score = -1
	for _, w := range tok[1:] {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		v = strings.TrimRight(v, ":;,")
		switch k {
		case "who":
			who = strings.ToLower(v)
		case "head":
			head = strings.ToLower(v)
		case "score":
			s := strings.TrimSuffix(v, "/10")
			if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 10 {
				score = n
			}
		}
	}

	// Look for inline finding on the first line after a colon
	var inlineFinding string
	if idx := strings.Index(first, ": "); idx >= 0 {
		colonPart := first[idx+2:]
		// Ensure this isn't inside gates=
		if !strings.HasPrefix(colonPart, "base:") && !strings.HasPrefix(colonPart, "scope:") {
			inlineFinding = strings.TrimSpace(colonPart)
		}
	}

	// Extract multiline findings from subsequent lines
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		clean := l
		if m := bulletPrefixRx.FindString(clean); m != "" {
			clean = strings.TrimSpace(clean[len(m):])
		}
		if clean != "" {
			findings = append(findings, clean)
		}
	}

	// If no multiline findings were found, use the inline finding
	if len(findings) == 0 && inlineFinding != "" {
		findings = append(findings, inlineFinding)
	}

	// If score is sub-8 and still no finding text was found, provide a fallback finding
	if score >= 0 && score < 8 && len(findings) == 0 {
		h := head
		if len(h) > 8 {
			h = h[:8]
		}
		findings = append(findings, fmt.Sprintf("sub-8 score (%d/10) at head %s", score, h))
	}

	return score, findings, who, head
}

// Pass executes one pass of the review loop over stream.
func Pass(ctx context.Context, c *redis.Client, opts LoopOptions) (LoopResult, error) {
	if opts.Stream == "" {
		return LoopResult{}, errors.New("stream name required")
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	actor := opts.Actor
	if actor == "" {
		actor = "coordinator"
	}

	dutyID, err := EnsureDutyCard(ctx, c, opts.Stream, actor, opts.Epoch, now)
	if err != nil {
		return LoopResult{}, err
	}

	res := LoopResult{
		Stream:   opts.Stream,
		DutyCard: dutyID,
	}

	// 1. Gather candidate card IDs in ws:<stream>:review and ws:<stream>:working
	reviewKey := ws.KeyAt(opts.Epoch, opts.Stream, ws.Review)
	workingKey := ws.KeyAt(opts.Epoch, opts.Stream, ws.Working)
	reviewIDs, err := c.ZRange(ctx, reviewKey, 0, -1).Result()
	if err != nil {
		return res, fmt.Errorf("zrange %s: %w", reviewKey, err)
	}
	workingIDs, _ := c.ZRange(ctx, workingKey, 0, -1).Result()
	allIDs := append([]string{}, reviewIDs...)
	for _, id := range workingIDs {
		if id != dutyID {
			allIDs = append(allIDs, id)
		}
	}

	seenPRs := make(map[string]bool)

	for _, id := range allIDs {
		if id == dutyID {
			continue
		}
		// Read card hash
		cardRec, err := c.HGetAll(ctx, "task:"+id).Result()
		if err != nil || len(cardRec) == 0 {
			continue
		}

		prNum := strings.TrimSpace(cardRec["pr"])
		repo := strings.TrimSpace(cardRec["repo"])
		if repo == "" {
			repo = "nova-tools"
		}
		if prNum == "" || prNum == "0" {
			// Check if ref has PR URL or repo#n
			if ref := strings.TrimSpace(cardRec["ref"]); ref != "" {
				if r, n, ok := parseRef(ref); ok {
					repo = r
					prNum = strconv.Itoa(n)
				}
			}
		}
		if prNum == "" || prNum == "0" {
			continue
		}

		prKey := repo + "#" + prNum
		if seenPRs[prKey] {
			continue
		}
		seenPRs[prKey] = true
		res.PRs++

		// Check if coordinator posted a verdict: if verdict is set or review indicates verdict
		verdict := strings.TrimSpace(cardRec["verdict"])
		reviewField := strings.TrimSpace(cardRec["review"])
		if verdict != "" || strings.Contains(reviewField, "verdict=") {
			// Coordinator has posted a verdict, stop loop for this card
			continue
		}

		// Read PR lines from pr:<repo>:<n>:lines and pr hash
		prHashKey := prkey.KeyText(repo, prNum)
		linesKey := prHashKey + ":lines"
		lines, err := c.LRange(ctx, linesKey, 0, -1).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			continue
		}

		// Also check "reads" field on pr record if lines list is empty
		if len(lines) == 0 {
			if rds := c.HGet(ctx, prHashKey, "reads").Val(); rds != "" {
				lines = strings.Split(rds, "\n")
			}
		}

		// Find the latest read score and findings
		var latestScore = -1
		var latestFindings []string
		var latestHead string

		for _, l := range lines {
			s, findings, _, h := ExtractFindings(l)
			if s >= 0 {
				res.Scores++
				latestScore = s
				latestFindings = findings
				latestHead = h
			}
		}

		// If card itself has a score field and no lines yielded one:
		if latestScore < 0 {
			if sStr := strings.TrimSpace(cardRec["score"]); sStr != "" {
				sStr = strings.TrimSuffix(sStr, "/10")
				if n, err := strconv.Atoi(sStr); err == nil {
					latestScore = n
					if f := strings.TrimSpace(cardRec["finding"]); f != "" {
						latestFindings = []string{f}
					}
					latestHead = strings.TrimSpace(cardRec["head"])
				}
			}
		}

		// Check termination: until read scores reach 10
		if latestScore >= 10 {
			// Reached 10/10: review passed!
			continue
		}

		// If score is sub-8 (0..7):
		if latestScore >= 0 && latestScore < 8 {
			if latestHead == "" {
				latestHead = strings.TrimSpace(cardRec["head"])
			}
			if len(latestFindings) == 0 {
				latestFindings = []string{fmt.Sprintf("sub-8 read (%d/10)", latestScore)}
			}

			// Get original card's score on the stream to deal behind it
			origScore, _ := c.ZScore(ctx, reviewKey, id).Result()
			if origScore <= 0 {
				origScore, _ = c.ZScore(ctx, workingKey, id).Result()
			}
			if origScore <= 0 {
				origScore = float64(now.UnixMilli())
			}

			for i, finding := range latestFindings {
				fixID := fmt.Sprintf("fix-%s-%s-%d", id, short(latestHead), i+1)
				// Check if fix card already exists
				if c.Exists(ctx, "task:"+fixID).Val() > 0 {
					continue
				}

				fixScore := origScore + float64((i+1)*1000)

				pushReq := taskcard.PushRequest{
					ID:     fixID,
					Where:  ws.Ready,
					Stream: opts.Stream,
					Kind:   "fix",
					Repo:   repo,
					PR:     prNum,
					Head:   latestHead,
					Title:  fmt.Sprintf("fix(%s#%s): %s", repo, prNum, finding),
					By:     actor,
					Why:    fmt.Sprintf("review loop sub-8 read (%d/10)", latestScore),
					Front:  false,
					Fields: []string{
						"primary", id,
						"leg", "fix",
						"finding", finding,
						"score", strconv.Itoa(latestScore),
						"created_at", strconv.FormatInt(int64(fixScore), 10),
					},
				}
				if p := strings.TrimSpace(cardRec["paths"]); p != "" {
					pushReq.Fields = append(pushReq.Fields, "paths", p)
				}
				if dw := strings.TrimSpace(cardRec["done_when"]); dw != "" {
					pushReq.Fields = append(pushReq.Fields, "done_when", dw)
				}
				if b := strings.TrimSpace(cardRec["base"]); b != "" {
					pushReq.Fields = append(pushReq.Fields, "base", b)
				}
				consumer := strings.TrimSpace(cardRec["consumer"])
				if consumer != "" {
					pushReq.Friend = strings.TrimPrefix(consumer, "friend:")
					pushReq.Fields = append(pushReq.Fields, "consumer", consumer)
				}

				if _, err := taskcard.Push(ctx, c, pushReq); err != nil {
					return res, fmt.Errorf("cut fix card %s: %w", fixID, err)
				}

				origWhere := strings.TrimSpace(cardRec["where"])
				if origWhere == "" {
					origWhere = ws.Review
				}
				_, _ = taskcard.Move(ctx, c, id, origWhere, taskcard.Opts{
					By:     actor,
					Why:    "link fix card",
					Fields: []string{"copy", fixID},
				})

				fixCard := FixCard{
					ID:      fixID,
					Primary: id,
					Repo:    repo,
					PR:      prNum,
					Head:    latestHead,
					Score:   latestScore,
					Finding: finding,
				}
				res.Fixes = append(res.Fixes, fixCard)
			}
		}
	}

	return res, nil
}

// RunLoop executes the review loop with options, printing receipt lines to out and errOut.
func RunLoop(ctx context.Context, c *redis.Client, opts LoopOptions, out, errOut io.Writer) (int, error) {
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	if opts.Daemon {
		defer func() {
			_ = ClearDutyCard(context.Background(), c, opts.Stream, opts.Epoch)
		}()
	}

	for {
		if ctx.Err() != nil {
			return 0, nil
		}
		start := nowFn()
		res, err := Pass(ctx, c, opts)
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil
			}
			fmt.Fprintf(errOut, "REVIEW LOOP REFUSED stream=%s err=%v\n", opts.Stream, err)
			return 1, err
		}

		for _, fix := range res.Fixes {
			fmt.Fprintf(out, "FIX CARD id=%s primary=%s pr=%s#%s head=%s score=%d/10 finding=%q\n",
				fix.ID, fix.Primary, fix.Repo, fix.PR, short(fix.Head), fix.Score, fix.Finding)
		}

		fmt.Fprintf(out, "REVIEW LOOP stream=%s duty=%s prs=%d scores=%d fixes=%d ms=%d\n",
			res.Stream, res.DutyCard, res.PRs, res.Scores, len(res.Fixes), time.Since(start).Milliseconds())

		if opts.OnPass != nil {
			opts.OnPass()
		}

		if !opts.Daemon {
			break
		}

		select {
		case <-ctx.Done():
			return 0, nil
		case <-time.After(opts.PollInterval):
		}
	}
	return 0, nil
}

func parseRef(s string) (repo string, n int, ok bool) {
	s = strings.TrimSpace(s)
	// Try URL: https://github.com/owner/name/pull/123
	if parts := strings.Split(s, "/"); len(parts) >= 4 {
		for i, p := range parts {
			if (p == "pull" || p == "issues") && i+1 < len(parts) && i >= 2 {
				if num, err := strconv.Atoi(parts[i+1]); err == nil && num > 0 {
					return parts[i-2] + "/" + parts[i-1], num, true
				}
			}
		}
	}
	// Try repo#n
	if r, numStr, hasHash := strings.Cut(s, "#"); hasHash {
		if num, err := strconv.Atoi(numStr); err == nil && num > 0 {
			return r, num, true
		}
	}
	return "", 0, false
}
