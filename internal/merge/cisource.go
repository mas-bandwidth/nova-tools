package merge

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The injectable CI source. GH.Checks reads one commit's verdict from
// ci:<owner/repo>:<sha> and NEVER from GitHub's check-runs. A check-run that
// the forge reports -- a green one included -- is not evidence this tool may
// merge on (nova-tools #2924). Missing, empty and non-OK values are not green:
// a caller sees ErrCIMissing and reports "ci: MISSING".

// ErrCIMissing is the not-green answer: the injectable source held no key, an
// empty value, or a value that is not OK. It is the only thing a missing,
// empty or unreadable CI verdict may become -- never a fallback to the forge's
// check-runs.
var ErrCIMissing = errors.New("ci: MISSING")

// CISource answers one commit's CI verdict. Read returns the raw value stored
// for a commit and whether the key existed at all; ok true with an empty value
// is still not-OK. Injection is by WithCISource, and the production
// implementation is RedisCISource.
type CISource interface {
	Read(repo, sha string) (value string, ok bool, err error)
}

// CIKey is where a commit's injectable verdict lives: ci:<owner/repo>:<sha>.
func CIKey(repo, sha string) string {
	return "ci:" + repo + ":" + sha
}

// ciGreen reports whether the value is exactly the protocol's OK token.
// Anything else -- empty, "fail",
// "pending", a sentence -- is non-OK and becomes ErrCIMissing.
func ciGreen(value string) bool {
	return strings.TrimSpace(value) == "OK"
}

// ciReadTimeout bounds one Redis read so a hung server cannot hang a pass.
const ciReadTimeout = 5 * time.Second

// RedisCISource is the production CISource: one GET per commit against Redis.
type RedisCISource struct {
	Client redis.UniversalClient
}

// Read fetches ci:<owner/repo>:<sha>. A nil client or a missing key reads as
// not-OK; a transport error is returned so the caller can still treat it as
// not-green without ever reaching the forge.
func (r *RedisCISource) Read(repo, sha string) (string, bool, error) {
	if r == nil || r.Client == nil {
		return "", false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ciReadTimeout)
	defer cancel()
	val, err := r.Client.Get(ctx, CIKey(repo, sha)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// RedisFromEnv builds the production source from the same NOVA_REDIS_* settings
// used by the lander. REDIS_ADDR remains a compatibility override for local
// callers. It needs no main.go wiring because NewGH calls it.
func RedisFromEnv() CISource {
	addr := redisAddrFromEnv()
	db := 0
	if v := strings.TrimSpace(os.Getenv("REDIS_DB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			db = n
		}
	}
	return &RedisCISource{Client: redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: envOr("NOVA_REDIS_USER", "bench"),
		Password: envOr("NOVA_REDIS_PASSWORD", os.Getenv("REDIS_PASSWORD")),
		DB:       db,
	})}
}

func redisAddrFromEnv() string {
	if addr := strings.TrimSpace(os.Getenv("REDIS_ADDR")); addr != "" {
		return addr
	}
	host := envOr("NOVA_REDIS_HOST", "100.115.99.19")
	port := envOr("NOVA_REDIS_PORT", "6380")
	return host + ":" + port
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
