package land

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CheckInboundFreshness checks ev:github:consumer:land for pending entries and heartbeat age (§3.2).
// Refuses if pending > 0 or beat is older than 10s.
func CheckInboundFreshness(ctx context.Context, c *redis.Client, now time.Time) (fresh bool, reason string, err error) {
	vals, err := c.HMGet(ctx, "ev:github:consumer:land", "pending", "at").Result()
	if err != nil {
		return false, "", err
	}
	if len(vals) < 2 || vals[0] == nil || vals[1] == nil {
		// Not yet initialized
		return true, "", nil
	}

	pendingStr, _ := vals[0].(string)
	atStr, _ := vals[1].(string)

	pending, _ := strconv.Atoi(pendingStr)
	if pending > 0 {
		return false, fmt.Sprintf("inbound-stale: %d pending", pending), nil
	}

	atUnix, _ := strconv.ParseInt(atStr, 10, 64)
	if atUnix > 100000000000 { // milliseconds
		atUnix = atUnix / 1000
	}
	age := now.Sub(time.Unix(atUnix, 0))
	if age > 10*time.Second {
		return false, fmt.Sprintf("inbound-stale: age %v > 10s", age), nil
	}

	return true, "", nil
}

// ReconcileMirror compares mirror refs against heads known in Redis (§3.2, L32).
// When a ref has moved with no corresponding delivery event, it emits INBOUND MISSED.
func ReconcileMirror(ctx context.Context, c *redis.Client, repo string, gitRefs map[string]string) ([]string, error) {
	var missed []string
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)

	for ref, sha := range gitRefs {
		// Check last seen sha recorded in Redis for this ref
		seenKey := fmt.Sprintf("land:%s:ref:%s", repo, ref)
		seenSHA := c.Get(ctx, seenKey).Val()
		if seenSHA == "" {
			// Initialize
			_ = c.Set(ctx, seenKey, sha, 0).Err()
			continue
		}

		if seenSHA != sha {
			// Ref moved without webhook delivery
			missedLine := fmt.Sprintf("INBOUND MISSED ref=%s sha=%s", ref, sha)
			missed = append(missed, missedLine)

			// Record event in land:<repo>:events stream
			_ = c.XAdd(ctx, &redis.XAddArgs{
				Stream: EventsStream(repo),
				MaxLen: 100000,
				Approx: true,
				Values: map[string]interface{}{
					"event": "INBOUND MISSED",
					"repo":  repo,
					"ref":   ref,
					"sha":   sha,
					"at":    now,
				},
			}).Err()

			// Update ref to current sha
			_ = c.Set(ctx, seenKey, sha, 0).Err()
		}
	}

	return missed, nil
}

// MirrorDiff runs git diff --name-only baseSHA..head inside mirrorDir (§3.1).
func MirrorDiff(ctx context.Context, mirrorDir, baseSHA, head string) ([]string, error) {
	if mirrorDir == "" {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", mirrorDir, "diff", "--name-only", fmt.Sprintf("%s..%s", baseSHA, head))
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff %s..%s in %s: %v: %s", baseSHA, head, mirrorDir, err, errOut.String())
	}

	var files []string
	for _, line := range strings.Split(out.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}
