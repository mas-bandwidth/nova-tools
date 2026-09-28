package main

import (
	"context"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
)

// ghIssueCloser closes an issue through the one GitHub client
// (internal/gh, #4343): state closed (completed), then the evidence
// comment. Closing a closed issue is a no-op, so a retry after a failed
// comment posts the comment once. Two writes, paced and counted under verb.
// (It lived beside the done-already duty until that duty left the
// reconciler pass, 2026-09-27; the review verb is its user.)
type ghIssueCloser struct {
	verb string
	rdb  redis.Cmdable
}

func (g ghIssueCloser) CloseIssue(ctx context.Context, repo string, number int, comment string) error {
	if !strings.Contains(repo, "/") {
		repo = devRedEnv("NOVA_GH_OWNER", devRedOwner) + "/" + repo
	}
	tok, err := gh.Token()
	if err != nil {
		return err
	}
	c := &gh.Client{Token: tok, Verb: g.verb, Redis: g.rdb, Log: os.Stderr}
	if err := c.CloseIssue(ctx, repo, number); err != nil {
		return err
	}
	_, err = c.Comment(ctx, repo, number, comment)
	return err
}
