// Package civerdict is the one reader of a commit's CI verdict record,
// ci:<owner/repo>:<head>:<gid>, shared by nova-sprint's land and nova-merge.
//
// The record is a HASH whose "verdict" field is OK, or FAIL plus the package
// and the test (the sprint CI card's end writes it; internal/sprintci). Two
// readers that disagreed on the shape -- land read HGETALL, nova-merge read GET
// -- turned every written key into WRONGTYPE and every refusal into
// "ci: MISSING" (nova-tools #2924 follow-up). Both now read through here.
package civerdict

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	// Field is the hash field that carries the verdict word.
	Field = "verdict"
	// OK is the only green verdict word.
	OK = "OK"
	// Missing is what a reader prints when no record says anything.
	Missing = "MISSING"
)

var (
	ErrNoPolicy = errors.New("civerdict: no policy record")
)

// GID computes the 16-hex gate receipt identity (spec §2.2 / §3.7):
// sha256("kind=single", base, base_sha, required_set_id, policy_id, runner_id)[:16].
func GID(kind, base, baseSHA, requiredSetID, policyID, runnerID string) string {
	raw := fmt.Sprintf("kind=%s,%s,%s,%s,%s,%s", kind, base, baseSHA, requiredSetID, policyID, runnerID)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:16]
}

// Key is where one commit's verdict lives: ci:<owner/repo>:<head>:<gid>.
func Key(repo, head, gid string) string {
	return "ci:" + strings.TrimSpace(repo) + ":" + strings.TrimSpace(head) + ":" + strings.TrimSpace(gid)
}

// GIDsKey is the set of all GIDs gated for a head: ci:<owner/repo>:<head>:gids.
func GIDsKey(repo, head string) string {
	return "ci:" + strings.TrimSpace(repo) + ":" + strings.TrimSpace(head) + ":gids"
}

// PolicyKey is where the base policy record lives: land:<repo>:<base>:policy.
func PolicyKey(repo, base string) string {
	return "land:" + strings.TrimSpace(repo) + ":" + strings.TrimSpace(base) + ":policy"
}

// TipKey is where the base tip record lives: land:<repo>:<base>:tip.
func TipKey(repo, base string) string {
	return "land:" + strings.TrimSpace(repo) + ":" + strings.TrimSpace(base) + ":tip"
}

// Expected reads the land:<repo>:<base>:policy record from Redis and returns the expected GID for base and baseSHA.
func Expected(ctx context.Context, c redis.Cmdable, repo, base, baseSHA string) (string, error) {
	if c == nil {
		return "", ErrNoPolicy
	}
	polKey := PolicyKey(repo, base)
	fields, err := c.HMGet(ctx, polKey, "policy_id", "required_set_id", "runner_id").Result()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", polKey, err)
	}
	policyID, _ := fields[0].(string)
	requiredSetID, _ := fields[1].(string)
	runnerID, _ := fields[2].(string)
	if policyID == "" || requiredSetID == "" || runnerID == "" {
		return "", ErrNoPolicy
	}
	return GID("single", base, baseSHA, requiredSetID, policyID, runnerID), nil
}

// Of is the verdict word a record carries, trimmed, or "" when the record is
// absent or has no verdict. "" is NOT a verdict: a missing record is no
// evidence either way.
func Of(fields map[string]string) string {
	if fields == nil {
		return ""
	}
	return strings.TrimSpace(fields[Field])
}

// Green reports whether the verdict word is exactly OK.
func Green(verdict string) bool { return strings.TrimSpace(verdict) == OK }

// Read fetches one commit's record with HGETALL. An absent key is an empty
// map and a nil error. Any error (WRONGTYPE, NOPERM, a dead store) is returned
// as is: it is an unreadable record, never a verdict.
func Read(ctx context.Context, c redis.Cmdable, repo, head, gid string) (map[string]string, error) {
	fields, err := c.HGetAll(ctx, Key(repo, head, gid)).Result()
	if errors.Is(err, redis.Nil) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return fields, nil
}

// ReadHead reads all receipts for a head using ci:<repo>:<head>:gids.
// It returns a receipt only if its GID matches Expected(base, base_sha, policy),
// where base_sha is the current base tip from land:<repo>:<base>:tip (or the receipt's base_sha).
// If base is provided via the optional base argument, only receipts for that base are considered.
// Green receipts on a moved base tip or obsolete policy are rejected as stale.
func ReadHead(ctx context.Context, c redis.Cmdable, repo, head string, base ...string) (map[string]string, error) {
	if c == nil {
		return map[string]string{}, nil
	}
	gids, err := c.SMembers(ctx, GIDsKey(repo, head)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if len(gids) == 0 {
		return map[string]string{}, nil
	}
	reqBase := ""
	if len(base) > 0 {
		reqBase = strings.TrimSpace(base[0])
	}
	var best map[string]string
	var bestAt int64 = -1
	for _, gid := range gids {
		rec, err := Read(ctx, c, repo, head, gid)
		if err != nil {
			return nil, err
		}
		if len(rec) == 0 {
			continue
		}
		recBase := rec["base"]
		if recBase == "" {
			recBase = "dev"
		}
		if reqBase != "" && recBase != reqBase {
			continue
		}
		tipSHA, err := c.HGet(ctx, TipKey(repo, recBase), "sha").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if tipSHA == "" {
			tipSHA = rec["base_sha"]
		}
		if tipSHA == "" {
			continue
		}
		expGID, err := Expected(ctx, c, repo, recBase, tipSHA)
		if err != nil {
			if errors.Is(err, ErrNoPolicy) {
				continue
			}
			return nil, err
		}
		if gid != expGID {
			continue
		}
		at, _ := strconv.ParseInt(rec["at"], 10, 64)
		if at == 0 {
			at, _ = strconv.ParseInt(rec["end_at"], 10, 64)
		}
		if best == nil || at >= bestAt {
			best = rec
			bestAt = at
		}
	}
	if best != nil {
		return best, nil
	}
	return map[string]string{}, nil
}
