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

// The injectable CI source. GH.Checks reads one commit's verdict record,
// ci:<owner/repo>:<sha>, through internal/civerdict -- the same HASH, key and
// "verdict" field nova-sprint's land reads. A record that carries a verdict is
// the answer: OK is green, FAIL is red, anything else is pending.
//
// A MISSING RECORD IS NOT A VERDICT (no-evidence-is-not-negative-evidence).
// When the key is absent, carries no verdict, or cannot be read (WRONGTYPE,
// NOPERM, a store that is down), GH.Checks falls back to GitHub's check-runs
// at the head and marks the evidence Source "from-github". ErrCIMissing is
// only the answer when BOTH say nothing: no verdict record and no check-run.
// (#2924 made the record the only source and the lander landed nothing for
// an afternoon because nothing wrote records for its integration heads.)

// ErrCIMissing is the answer when neither the verdict record nor the forge's
// check-runs say anything about a commit. It is never the answer to an absent
// record alone.
var ErrCIMissing = errors.New("ci: MISSING")

// Where a Checks value's evidence came from.
const (
	CIFromRedis  = "redis"
	CIFromGitHub = "from-github"
)

// CISource answers one commit's CI verdict. Read returns the verdict word
// stored for a commit and whether the record said anything at all (ok false:
// absent, or present without a verdict). An error is an unreadable record;
// GH.Checks treats it exactly like an absent one. Injection is by
// WithCISource, and the production implementation is RedisCISource.
type CISource interface {
	Read(repo, sha string) (value string, ok bool, err error)
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

// RedisCISource is the production CISource: reads through civerdict.ReadHead.
type RedisCISource struct {
	Client redis.UniversalClient
}

// Read fetches the commit's verdict record through civerdict.ReadHead. A nil client, a
// missing key or a record without a verdict reads as ok false; a transport or
// ACL error is returned, and GH.Checks falls back to the forge on it.
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
	v := civerdict.Of(fields)
	return v, v != "", nil
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
