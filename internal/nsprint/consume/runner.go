package consume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/prtoread.lua.
const (
	FunctionPRToReadRunner = "ns_prtoread_runner"
	FunctionPRToReadAdopt  = "ns_prtoread_adopt"
)

// PRToReadRule is the pr-to-read rule (#2756 10.7, 10.8.3, control 33;
// nova-tools #3040) of nova-sprint route: runner rows copied from ev:github into
// ci:<repo>:<sha>, and card-less sprint PRs adopted once.
type PRToReadRule struct {
	Store    *store.Store
	Sprint   string
	Consumer string
	Out      io.Writer
	CICut    func(context.Context, CICut) error
	Count    int64
	Block    time.Duration
}

func (r *PRToReadRule) group() string {
	return "pr-to-read:" + r.Sprint
}

// Start prepares the ev:github source: creates consumer group pr-to-read:<S>
// and claims any pending entries from a previous instance.
func (r *PRToReadRule) Start(ctx context.Context) error {
	client := r.Store.Client()
	group := r.group()
	err := client.XGroupCreateMkStream(ctx, ghevent.Stream, group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("%s: group: %w", group, err)
	}
	start := "0-0"
	for {
		_, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: ghevent.Stream, Group: group, Consumer: r.Consumer,
			MinIdle: 0, Start: start, Count: 1000,
		}).Result()
		if err != nil {
			return fmt.Errorf("%s: reclaim pending: %w", group, err)
		}
		if next == "0-0" || next == "" {
			return nil
		}
		start = next
	}
}

// Pass executes one pass: drain ev:github, read s:<S>:prs and PR records,
// adopt qualifying PRs, write proc:pr-to-read, and print PASS took_ms=.
func (r *PRToReadRule) Pass(ctx context.Context) (int, error) {
	began := time.Now()
	client := r.Store.Client()

	policy, err := client.HGetAll(ctx, "s:"+r.Sprint+":policy").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, fmt.Errorf("pr-to-read: policy: %w", err)
	}

	handledRunners, err := r.drain(ctx, policy["runner_rows"])
	if err != nil {
		return handledRunners, err
	}

	handledAdopts, err := r.adopt(ctx, policy)
	if err != nil {
		return handledRunners + handledAdopts, err
	}

	tookMs := time.Since(began).Milliseconds()
	at := time.Now().UTC().Format(time.RFC3339)
	if perr := client.HSet(ctx, "proc:pr-to-read", "pass_at", at, "took_ms", strconv.FormatInt(tookMs, 10)).Err(); perr != nil {
		return handledRunners + handledAdopts, fmt.Errorf("pr-to-read: proc: %w", perr)
	}

	if r.Out != nil {
		fmt.Fprintf(r.Out, "PASS took_ms=%d\n", tookMs)
	}

	return handledRunners + handledAdopts, nil
}

type candidateRunner struct {
	msg  redis.XMessage
	repo string
	sha  string
	row  string
	json string
}

func (r *PRToReadRule) drain(ctx context.Context, runnerRowsPolicy string) (int, error) {
	client := r.Store.Client()
	group := r.group()
	count := r.Count
	if count <= 0 {
		count = 100
	}
	wait := r.Block
	if wait == 0 {
		wait = time.Second
	}

	handled := 0
	for {
		streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: group, Consumer: r.Consumer, Streams: []string{ghevent.Stream, ">"},
			Count: count, Block: wait,
		}).Result()
		if errors.Is(err, redis.Nil) {
			break
		}
		if err != nil {
			return handled, fmt.Errorf("%s: read: %w", group, err)
		}
		if len(streams) == 0 || len(streams[0].Messages) == 0 {
			break
		}

		msgs := streams[0].Messages
		var candidates []candidateRunner
		allIDs := make([]string, len(msgs))

		for i, m := range msgs {
			allIDs[i] = m.ID
			kind, _ := m.Values["kind"].(string)
			if kind != "check_run" {
				continue
			}
			repo, _ := m.Values["repo"].(string)
			head, _ := m.Values["head"].(string)
			check, _ := m.Values["check"].(string)
			row, ok := matchRunnerRow(runnerRowsPolicy, repo, check)
			if !ok {
				continue
			}

			action, _ := m.Values["action"].(string)
			checkRunIDStr, _ := m.Values["check_run_id"].(string)
			checkRunID, _ := strconv.ParseInt(checkRunIDStr, 10, 64)
			status, _ := m.Values["status"].(string)
			conclusion, _ := m.Values["conclusion"].(string)
			at, _ := m.Values["at"].(string)

			attemptBytes, _ := json.Marshal(map[string]any{
				"action":       action,
				"check_run_id": checkRunID,
				"status":       status,
				"conclusion":   conclusion,
				"at":           at,
			})

			candidates = append(candidates, candidateRunner{
				msg:  m,
				repo: repo,
				sha:  head,
				row:  row,
				json: string(attemptBytes),
			})
		}

		// First roundtrip: pipeline of FCALLs for candidates in stream order
		if len(candidates) > 0 {
			pipe := client.Pipeline()
			cmds := make([]*redis.Cmd, len(candidates))
			for i, c := range candidates {
				ciKey := "ci:" + c.repo + ":" + c.sha
				cmds[i] = pipe.FCall(ctx, FunctionPRToReadRunner, nil, ciKey, c.row, c.json)
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return handled, fmt.Errorf("%s: runner pipeline: %w", group, err)
			}

			for i, c := range candidates {
				res, err := cmds[i].Result()
				if err != nil {
					return handled, fmt.Errorf("%s: runner fcall: %w", group, err)
				}
				parts, ok := res.([]any)
				if !ok || len(parts) < 5 {
					return handled, fmt.Errorf("%s: unexpected fcall reply: %v", group, res)
				}
				verdict := fmt.Sprint(parts[0])
				gen := fmt.Sprint(parts[1])
				attempt := fmt.Sprint(parts[2])
				st := fmt.Sprint(parts[3])
				concl := fmt.Sprint(parts[4])

				sha8 := c.sha
				if len(sha8) > 8 {
					sha8 = sha8[:8]
				}
				if r.Out != nil {
					fmt.Fprintf(r.Out, "RUNNER %s@%s %s g%s/%s %s/%s %s\n",
						c.repo, sha8, c.row, gen, attempt, st, concl, verdict)
				}
				handled++
			}
		}

		// Second roundtrip: XACK for all IDs in the batch
		if len(allIDs) > 0 {
			if err := client.XAck(ctx, ghevent.Stream, group, allIDs...).Err(); err != nil {
				return handled, fmt.Errorf("%s: ack: %w", group, err)
			}
		}

		// Subsequent reads are non-blocking
		wait = -1
	}

	return handled, nil
}

func matchRunnerRow(policyRows string, repo, check string) (string, bool) {
	if check == "" {
		return "", false
	}
	items := strings.FieldsFunc(policyRows, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
	repoPrefix := repo + ":"
	for _, item := range items {
		if strings.Contains(item, ":") {
			if strings.HasPrefix(item, repoPrefix) {
				job := strings.TrimPrefix(item, repoPrefix)
				if job == check {
					return job, true
				}
			}
		} else {
			if item == check {
				return item, true
			}
		}
	}
	return "", false
}

// RunnerReady is the single rule that the lander and land why call. It reads
// only the stored latest attempt in ci:
//   - completed/success is ready.
//   - failure and timed_out are FAIL.
//   - Every other status or conclusion is MISSING, and so is an absent field.
//     rerequested is MISSING too.
func RunnerReady(ci map[string]string, rows []string) (bool, []string) {
	if len(rows) == 0 {
		return true, nil
	}
	var missing []string
	for _, row := range rows {
		raw := ci["runner:"+row]
		if raw == "" {
			missing = append(missing, row)
			continue
		}
		var attempt struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}
		if err := json.Unmarshal([]byte(raw), &attempt); err != nil {
			missing = append(missing, row)
			continue
		}
		if attempt.Status == "completed" && attempt.Conclusion == "success" {
			continue
		}
		missing = append(missing, row)
	}
	return len(missing) == 0, missing
}
