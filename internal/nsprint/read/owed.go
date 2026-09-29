package read

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// OwedOptions is the inputs to `read owed --pr <n>`.
type OwedOptions struct {
	Repo string // owner/name or bare name (default "nova-tools")
	PR   int    // PR number
	By   string // actor / creator
}

// OwedResult is what `read owed` did.
type OwedResult struct {
	Repo   string
	PR     int
	Score  int
	Card   string // the fix card cut
	Stream string
	Behind string // the original card it's behind
	Body   string // the parsed Owed: sentence
}

var owedRx = regexp.MustCompile(`(?i)\bowed\s*:\s*([^\n\r]+)`)

// FindOwedSentence parses the "Owed: <sentence>" from typed lines or review text.
func FindOwedSentence(lines []string) (string, error) {
	for i := len(lines) - 1; i >= 0; i-- {
		if m := owedRx.FindStringSubmatch(lines[i]); len(m) > 1 {
			s := strings.TrimSpace(m[1])
			s = strings.TrimRight(s, " \t\r\n\"'")
			s = strings.TrimLeft(s, " \t\r\n\"'")
			if s != "" {
				return s, nil
			}
		}
	}
	return "", errors.New("no Owed: sentence found in read")
}

// Owed executes `read owed --pr <n>` (issue #4397):
// A read under 8 on a PR already merged cuts the owed work as a fix card
// behind the original on the same stream.
func Owed(ctx context.Context, c redis.Cmdable, o OwedOptions) (OwedResult, error) {
	if o.PR <= 0 {
		return OwedResult{}, errors.New("wants a positive PR number")
	}
	repo := o.Repo
	if repo == "" {
		repo = "nova-tools"
	}
	by := o.By

	recs, err := stream.LoadPRs(ctx, c, repo, []int{o.PR})
	if err != nil {
		return OwedResult{}, err
	}
	if len(recs) == 0 || !recs[0].Exists {
		return OwedResult{}, fmt.Errorf("no record for %s#%d", repo, o.PR)
	}
	r := recs[0]

	// Must be merged
	isMerged := r.State == "merged" || r.State == "landed"
	if !isMerged {
		m, err := c.HGetAll(ctx, stream.PRKey(repo, o.PR)).Result()
		if err == nil {
			if m["state"] == "merged" || m["state"] == "landed" || m["merge_sha"] != "" || m["merged"] == "true" {
				isMerged = true
			}
		}
	}
	if !isMerged {
		return OwedResult{}, fmt.Errorf("pr %s#%d is not merged (state=%s)", repo, o.PR, r.State)
	}

	head := r.Head
	if head == "" {
		head = c.HGet(ctx, stream.PRKey(repo, o.PR), "merged_head").Val()
	}
	score, _, ok := stream.NewestScoreAt(r.Reads, head)
	if !ok {
		return OwedResult{}, fmt.Errorf("pr %s#%d has no score recorded at head %s", repo, o.PR, stream.Short(head))
	}
	if score >= 8 {
		return OwedResult{}, fmt.Errorf("pr %s#%d newest score is %d/10 (>= 8); no owed work", repo, o.PR, score)
	}

	owedSentence, err := FindOwedSentence(r.Reads)
	if err != nil {
		return OwedResult{}, fmt.Errorf("pr %s#%d: %w", repo, o.PR, err)
	}

	origCardID := r.Task
	streamName := r.Stream
	if origCardID == "" {
		origCardID = c.HGet(ctx, stream.PRKey(repo, o.PR), "task").Val()
	}
	if streamName == "" {
		streamName = c.HGet(ctx, stream.PRKey(repo, o.PR), "stream").Val()
	}
	if (streamName == "" || streamName == "-") && origCardID != "" {
		streamName = c.HGet(ctx, "task:"+origCardID, "stream").Val()
	}
	if streamName == "" || streamName == "-" {
		streamName = "landing"
	}

	fixID := "fix-" + origCardID
	if origCardID == "" {
		fixID = fmt.Sprintf("fix-pr-%d", o.PR)
		origCardID = fmt.Sprintf("pr-%d", o.PR)
	}

	// Avoid collision if fixID already exists
	if c.Exists(ctx, "task:"+fixID).Val() > 0 {
		for k := 2; ; k++ {
			alt := fmt.Sprintf("%s-%d", fixID, k)
			if c.Exists(ctx, "task:"+alt).Val() == 0 {
				fixID = alt
				break
			}
		}
	}

	sprint := ""
	if origCardID != "" {
		sprint = c.HGet(ctx, "task:"+origCardID, "sprint").Val()
	}
	if sprint == "" {
		sprints, _ := c.ZRange(ctx, "sprint:order", 0, 0).Result()
		if len(sprints) > 0 {
			sprint = sprints[0]
		}
	}

	req := taskcard.PushRequest{
		ID:        fixID,
		Where:     "waiting",
		DependsOn: origCardID,
		Stream:    streamName,
		Sprint:    sprint,
		Kind:      "fix",
		Repo:      repo,
		PR:        strconv.Itoa(o.PR),
		Title:     fmt.Sprintf("STREAM: %s | fix owed on %s: %s", streamName, origCardID, owedSentence),
		By:        by,
		Why:       fmt.Sprintf("read owed on pr #%d (score %d)", o.PR, score),
		Fields: []string{
			"body", owedSentence,
			"owed_pr", strconv.Itoa(o.PR),
			"owed_score", strconv.Itoa(score),
		},
	}
	_, err = taskcard.Push(ctx, c, req)
	if err != nil {
		return OwedResult{}, fmt.Errorf("cut fix card %s: %w", fixID, err)
	}

	return OwedResult{
		Repo:   repo,
		PR:     o.PR,
		Score:  score,
		Card:   fixID,
		Stream: streamName,
		Behind: origCardID,
		Body:   owedSentence,
	}, nil
}
