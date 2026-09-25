package merge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// cisource.go is where the lander reads CI from. The lander asks ONE question of a head
// commit -- has CI judged this very commit green on its own -- and the answer lives in ONE
// place: the `ci:<repo>:<sha>` redis key CI writes when it finishes judging a commit. The
// lander never reads the forge's check-runs (#2924): a green check-run is a pull request's
// own evidence, read on whatever commit the rollup happens to name, and the queue entry is
// the commit CI judged on its own, which is a different fact. A green check-run beside a
// missing ci key is not a verdict: it is MISSING, and the lander refuses.

// CIKey is the redis key CI writes for one commit. The value is the one-word verdict: OK
// is green; anything else is a refusal; an absent key is MISSING.
func CIKey(repo, sha string) string {
	return "ci:" + repo + ":" + sha
}

// CIVerdict is one head commit's CI, read from the ci:<repo>:<sha> key.
type CIVerdict struct {
	// State is the closed set the lander acts on: OK, MISSING, or FAILURE.
	State string
	// Raw is the value under the key, "" when the key is absent.
	Raw string
}

// Batches reports whether this verdict admits the batch: OK is the only green.
func (v CIVerdict) Batches() bool { return v.State == "OK" }

// Missing reports whether the ci key was absent.
func (v CIVerdict) Missing() bool { return v.State == "MISSING" }

// CISource is the edge the lander reads CI through. It is an interface for the reason
// merge.Host is: the tests drive a fake and reach no network, and the one implementation
// that reads redis is then a thing a reader can check line by line.
type CISource interface {
	// CI returns the value under one ci:<repo>:<sha> key, and "" when the key is absent.
	CI(ctx context.Context, key string) (string, error)
}

// RedisCI reads the ci:<repo>:<sha> key from one redis client. go-redis is already this
// repository's client (internal/redisq, internal/record, internal/ci), so this adds no
// dependency; the one call is GET, a single key, so no Lua and no KEYS/SCAN.
type RedisCI struct {
	Client *redis.Client
}

// NewRedisCI returns a CI source over one redis client.
func NewRedisCI(c *redis.Client) *RedisCI { return &RedisCI{Client: c} }

// CI reads one key. A missing key is "", nil -- MISSING to the lander -- not an error: a
// commit CI has not judged yet is a fact the lander refuses, never a read that failed.
func (s *RedisCI) CI(ctx context.Context, key string) (string, error) {
	if s == nil || s.Client == nil {
		return "", errors.New("no redis client to read CI from")
	}
	val, err := s.Client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

// ReadLanderCI reads a head commit's CI from the ci:<repo>:<sha> key. A missing key is
// MISSING -- a green check-run never substitutes for it, because the lander reads CI and
// never check-runs (nova-tools #2924). The value OK is the only green; any other value is
// a failure.
func ReadLanderCI(ctx context.Context, src CISource, repo, sha string) (CIVerdict, error) {
	if src == nil {
		return CIVerdict{}, errors.New("no CI source to read CI from")
	}
	raw, err := src.CI(ctx, CIKey(repo, sha))
	if err != nil {
		return CIVerdict{}, fmt.Errorf("read ci for %s:%s: %w", repo, sha, err)
	}
	raw = strings.TrimSpace(raw)
	v := CIVerdict{Raw: raw}
	switch raw {
	case "":
		v.State = "MISSING"
	case "OK":
		v.State = "OK"
	default:
		v.State = "FAILURE"
	}
	return v, nil
}
