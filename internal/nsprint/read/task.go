package read

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// A read task names its PR one of three ways, depending on who pushed it:
// repo + pr fields (task push, route, redistribute), a PR URL in ref (the
// first read, ns_pr_first_read through the friend queue) or <repo>#<n> in
// ref (the duty and hold routes). Target resolves all three, so the brief a
// read child gets is the same whichever path queued it (nova-tools#3599).

// Target is the PR a read task reads: the bare repo name, the PR number and
// the head the task was queued at.
type Target struct {
	Repo, N, Head string
}

var (
	refURLRx  = regexp.MustCompile(`^https?://[^/\s]+/[^/\s]+/([^/\s]+)/pull/(\d+)(?:[/?#].*)?$`)
	refHashRx = regexp.MustCompile(`^(?:[^/\s#]+/)?([^/\s#]+)#(\d+)$`)
)

// TargetOf reads the target from a read task's hash fields. It refuses a
// task that is not a read (kind read or review), one that names no PR and
// one with no head: a read is always at one exact head.
func TargetOf(f map[string]string) (Target, error) {
	var t Target
	if k := f["kind"]; k != "read" && k != "review" {
		return t, fmt.Errorf("kind %q is not a read (want read or review)", k)
	}
	repo, n := strings.TrimSpace(f["repo"]), strings.TrimSpace(f["pr"])
	if repo == "" || n == "" || n == "0" {
		ref := strings.TrimSpace(f["ref"])
		if m := refURLRx.FindStringSubmatch(ref); m != nil {
			repo, n = m[1], m[2]
		} else if m := refHashRx.FindStringSubmatch(ref); m != nil {
			repo, n = m[1], m[2]
		} else {
			return t, fmt.Errorf("names no PR: no repo+pr fields and ref %q is neither a PR URL nor <repo>#<n>", ref)
		}
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	if v, err := strconv.Atoi(n); err != nil || v <= 0 {
		return t, fmt.Errorf("PR number %q is not a positive number", n)
	}
	t.Repo, t.N, t.Head = repo, n, strings.TrimSpace(f["head"])
	if t.Head == "" {
		return t, fmt.Errorf("has no head; a read is at one exact head")
	}
	return t, nil
}

// TaskKeys are the two hashes a read task lives in today: the friend queue's
// task:<id> and, when the sprint is named, the sprint's s:<S>:task:<id>.
func TaskKeys(sprint, id string) []string {
	keys := []string{"task:" + id}
	if sprint != "" {
		keys = append(keys, "s:"+sprint+":task:"+id)
	}
	return keys
}

// TaskFields reads a task's hash in one pipeline over TaskKeys, the sprint's
// hash first when both exist. Empty when neither does.
func TaskFields(ctx context.Context, c *redis.Client, sprint, id string) (map[string]string, error) {
	keys := TaskKeys(sprint, id)
	pipe := c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.HGetAll(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("read task %s: %w", id, err)
	}
	for i := len(cmds) - 1; i >= 0; i-- {
		if f := cmds[i].Val(); len(f) > 0 {
			return f, nil
		}
	}
	return map[string]string{}, nil
}

// BriefTask writes the read brief of one read task: the task names the PR
// and the head, the record, lines and CI come from Redis and the diff from
// the mirror, exactly as Brief. mirror "" is the bench mirror of the task's
// repo. Exit codes as Brief; a task that is not a read of one PR at one head
// is refused (1).
func BriefTask(ctx context.Context, c *redis.Client, id string, fields map[string]string, mirror, outDir string, stdout, stderr io.Writer) int {
	t, err := TargetOf(fields)
	if err != nil {
		fmt.Fprintf(stderr, "READ BRIEF REFUSED task=%s why=task %v\n", id, err)
		return 1
	}
	return brief(ctx, c, t.Repo, t.N, t.Head, id, mirror, outDir, stdout, stderr)
}
