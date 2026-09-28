package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The Redis copy of a reference's state (#4343 BUILD 4): what the dealer
// asks of <repo>#<n> (a PR merged or closed, an issue closed) is read from
// gh:ref:<repo>:<n> once GitHub answered it; a terminal answer stands
// forever, an open one until the webhook ingest folds the closing delivery
// (an issues entry writes the copy; a pull_request closed entry deletes it,
// since the delivery does not say whether it merged, and the next read is
// one counted call).

// Ref is the state of one reference.
type Ref struct {
	IsPR   bool
	Merged bool
	State  string // open or closed
	Base   string // the PR's base branch
	At     string // when the copy was written (unix seconds)
}

// Terminal is true when the answer cannot change: a merged or closed PR,
// a closed issue.
func (r Ref) Terminal() bool { return r.Merged || r.State == "closed" }

// RefKey is the copy of <repo>#<n>, keyed by the repository name without
// its owner (ev:github carries owner/name, a card's DEPENDS-ON may not).
func RefKey(repo string, n int) string {
	repo = strings.TrimSpace(repo)
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	return "gh:ref:" + repo + ":" + strconv.Itoa(n)
}

// ReadRef reads the copy; found is false when there is none.
func ReadRef(ctx context.Context, rdb redis.Cmdable, repo string, n int) (Ref, bool, error) {
	m, err := rdb.HGetAll(ctx, RefKey(repo, n)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Ref{}, false, fmt.Errorf("HGETALL %s: %w", RefKey(repo, n), err)
	}
	if len(m) == 0 {
		return Ref{}, false, nil
	}
	return Ref{IsPR: m["is_pr"] == "1", Merged: m["merged"] == "1", State: m["state"], Base: m["base"], At: m["at"]}, true, nil
}

// WriteRef writes the copy.
func WriteRef(ctx context.Context, rdb redis.Cmdable, repo string, n int, r Ref, now time.Time) error {
	f := map[string]any{"is_pr": flag(r.IsPR), "merged": flag(r.Merged), "state": r.State, "base": r.Base,
		"at": strconv.FormatInt(now.Unix(), 10)}
	if err := rdb.HSet(ctx, RefKey(repo, n), f).Err(); err != nil {
		return fmt.Errorf("HSET %s: %w", RefKey(repo, n), err)
	}
	return nil
}

// InvalidateRef drops the copy, so the next read asks GitHub once.
func InvalidateRef(ctx context.Context, rdb redis.Cmdable, repo string, n int) error {
	return rdb.Del(ctx, RefKey(repo, n)).Err()
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Ref reads <repo>#<n> from GitHub: GET pulls/n answers a PR; a 404 there
// reads the number as an issue. One or two calls, both counted.
func (c *Client) Ref(ctx context.Context, repo string, n int) (Ref, error) {
	var pr struct {
		Merged bool   `json:"merged"`
		State  string `json:"state"`
		Base   struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), nil, &pr)
	if err == nil {
		return Ref{IsPR: true, Merged: pr.Merged, State: pr.State, Base: pr.Base.Ref}, nil
	}
	var herr *HTTPError
	if !errors.As(err, &herr) || herr.Status != http.StatusNotFound {
		return Ref{}, err
	}
	var is struct {
		State string `json:"state"`
	}
	if _, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repo, n), nil, &is); err != nil {
		return Ref{}, err
	}
	return Ref{State: is.State}, nil
}

// CachedRef answers from the copy when it has a terminal answer, or any
// answer written after since; otherwise it asks GitHub once and writes the
// copy. since zero means any copy stands (the webhook ingest keeps it
// current: events over polling).
func (c *Client) CachedRef(ctx context.Context, repo string, n int) (Ref, bool, error) {
	if c.Redis != nil {
		if r, ok, err := ReadRef(ctx, c.Redis, repo, n); err != nil {
			return Ref{}, false, err
		} else if ok {
			return r, true, nil
		}
	}
	r, err := c.Ref(ctx, repo, n)
	if err != nil {
		return Ref{}, false, err
	}
	if c.Redis != nil {
		if err := WriteRef(ctx, c.Redis, repo, n, r, c.now()); err != nil {
			return r, false, err
		}
	}
	return r, false, nil
}
