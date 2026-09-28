package merge

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// The injectable CI source. GH.Checks reads a commit's CI verdict from Redis
// and never from GitHub (#2924; THE BOUNDARY, Glenn 2026-09-25: CI verdicts
// arrive by webhook into Redis or from our own runners, never by polling).
// RedisCISource reads three records, first answer wins:
//
//  1. the ci card verdict receipt ci:<owner/repo>:<head>:<gid>, through
//     internal/civerdict, the same HASH and "verdict" field nova-sprint's land reads;
//  2. our own CI's request record ci:<owner/repo>:<head> (ci_run.lua): its
//     "ci" word, green, red or pending, the word nova-sprint land waits on;
//  3. the GitHub leg ci:<owner/repo>:<head>:gh: its "gh" fold, green, red or
//     pending, written only from GitHub's webhook deliveries (#3888).
//
// A MISSING RECORD IS NOT A VERDICT (no-evidence-is-not-negative-evidence):
// when none of the three says anything the answer is ErrCIMissing, which
// stops the entry (UNKNOWN) and never marks it red. An unreadable record
// (WRONGTYPE, NOPERM, a store that is down) is an error, the same stop. The
// forge's check-runs are never read: the afternoon #2924's first cut landed
// nothing was a missing writer for integration heads, and the writer is the
// fix (the ci request and the GitHub leg), not a poll.

// ErrCIMissing is the answer when no CI record in Redis says anything about a
// commit. A green check-run on GitHub does not change it.
var ErrCIMissing = errors.New("ci: MISSING")

// CIFromRedis is the only Source GH.Checks reports.
const CIFromRedis = "redis"

// CISource answers one commit's CI verdict. Read returns the verdict word
// for a commit (OK, FAIL..., or anything else for pending) and whether any
// record said anything at all (ok false: every record absent or without a
// word). An error is an unreadable record. Injection is by WithCISource, and
// the production implementation is RedisCISource.
type CISource interface {
	Read(repo, sha string) (value string, ok bool, err error)
}

// Where the two nova-sprint CI records live and the word each carries.
const (
	ciRequestField = "ci" // on ci:<repo>:<sha>: green, red or pending
	ciGHSuffix     = "gh" // ci:<repo>:<sha>:gh
	ciGHField      = "gh" // on ci:<repo>:<sha>:gh: green, red or pending
)

// CIRequestKey is our own CI's request record for a commit: ci:<owner/repo>:<sha>.
func CIRequestKey(repo, sha string) string {
	return "ci:" + strings.TrimSpace(repo) + ":" + strings.TrimSpace(sha)
}

// CIGitHubKey is the GitHub leg for a commit, folded from webhook deliveries:
// ci:<owner/repo>:<sha>:gh.
func CIGitHubKey(repo, sha string) string {
	return CIRequestKey(repo, sha) + ":" + ciGHSuffix
}

// legWord maps a green|red|pending word from the request record or the GitHub
// leg onto the verdict vocabulary ciState reads. why names the red.
func legWord(word, why string) string {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "green":
		return civerdict.OK
	case "red":
		return strings.TrimSpace("FAIL " + why)
	case "":
		return ""
	default:
		return "pending"
	}
}

// CIKey is where a commit's verdict record lives: ci:<owner/repo>:<head>:<gid>.
func CIKey(repo, sha, gid string) string {
	return civerdict.Key(repo, sha, gid)
}

// ciState maps a record's verdict word onto a check-run state Checks buckets:
// exactly OK is success, FAIL... is failure, and any other word is pending.
func ciState(value string) string {
	v := strings.TrimSpace(value)
	switch {
	case civerdict.Green(v):
		return "success"
	case strings.HasPrefix(strings.ToUpper(v), "FAIL"):
		return "failure"
	default:
		return "pending"
	}
}

// ciReadTimeout bounds one Redis read so a hung server cannot hang a pass.
const ciReadTimeout = 5 * time.Second

// RedisCISource is the production CISource: the receipt through
// civerdict.ReadHead, then the request record and the GitHub leg in one pipeline.
type RedisCISource struct {
	Client redis.UniversalClient
}

// Read answers from the first record that carries a word. A nil client or no
// word anywhere reads as ok false; a transport, type or ACL error is returned.
func (r *RedisCISource) Read(repo, sha string) (string, bool, error) {
	if r == nil || r.Client == nil {
		return "", false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ciReadTimeout)
	defer cancel()
	fields, err := civerdict.ReadHead(ctx, r.Client, repo, sha)
	if err != nil {
		return "", false, err
	}
	if v := civerdict.Of(fields); v != "" {
		return v, true, nil
	}
	pipe := r.Client.Pipeline()
	req := pipe.HMGet(ctx, CIRequestKey(repo, sha), ciRequestField, "why")
	gh := pipe.HMGet(ctx, CIGitHubKey(repo, sha), ciGHField, "gh_fail")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", false, err
	}
	for _, cmd := range []*redis.SliceCmd{req, gh} {
		vals := cmd.Val()
		if len(vals) < 2 {
			continue
		}
		word, _ := vals[0].(string)
		why, _ := vals[1].(string)
		if v := legWord(word, why); v != "" {
			return v, true, nil
		}
	}
	return "", false, nil
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
	host := envOr("NOVA_REDIS_HOST", "localhost")
	port := envOr("NOVA_REDIS_PORT", "6379")
	return host + ":" + port
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
