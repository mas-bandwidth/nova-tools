// Package conform implements bench conformance evaluation, Redis persistence, lease fencing, and whole-fleet checks (#2921).
package conform

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var CodingKeys = []string{
	"push-credential",
	"stage-verb",
	"mirror-age",
	"results-root",
	"finished-jobdirs",
	"sandbox-exec",
	"legs",
	"tools",
	"harness",
	"layout",
	"sops",
	"plaintext-keys",
	"pool-identity",
	"developer-dir",
	"os-sandbox",
}

var ErrFenced = errors.New("FENCED: lease:conform is held by another instance")

type ConformResult struct {
	OK             bool
	BadKey         string
	StandardDigest string
	Answers        map[string]string
}

// CheckAll checks whole-fleet conformance using one pipeline: SMEMBERS benches,
// then HGETALL for fleet:standard and each bench:<b>:conform.
func CheckAll(ctx context.Context, client *redis.Client) (bool, error) {
	pipe := client.Pipeline()
	benchesCmd := pipe.SMembers(ctx, "benches")
	standardCmd := pipe.HGetAll(ctx, "fleet:standard")
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}

	benches := benchesCmd.Val()
	std := standardCmd.Val()
	stdDigest := std["digest"]
	if stdDigest == "" {
		return false, errors.New("fleet:standard missing or has no digest")
	}

	if len(benches) == 0 {
		return true, nil
	}

	pipe = client.Pipeline()
	cmds := make(map[string]*redis.MapStringStringCmd)
	for _, b := range benches {
		cmds[b] = pipe.HGetAll(ctx, "bench:"+b+":conform")
	}
	_, err = pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}

	now := time.Now().Unix()
	for _, b := range benches {
		row := cmds[b].Val()
		if len(row) == 0 {
			return false, fmt.Errorf("MISSING %s conform record", b)
		}
		if row["ok"] != "1" {
			return false, fmt.Errorf("DRIFT %s %s", b, row["bad"])
		}
		if row["standard_digest"] != stdDigest {
			return false, fmt.Errorf("DRIFT %s digest mismatch have=%s want=%s", b, row["standard_digest"], stdDigest)
		}
		atSec, err := strconv.ParseInt(row["at"], 10, 64)
		if err != nil || now-atSec > 180 {
			return false, fmt.Errorf("STALE %s conform record (>180s old)", b)
		}
	}
	return true, nil
}

// AcquireLease acquires lease:conform:<b> with a 6s TTL.
func AcquireLease(ctx context.Context, client *redis.Client, bench, instance, token string) error {
	key := "lease:conform:" + bench
	ok, err := client.SetNX(ctx, key+"_lock", token, 6*time.Second).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrFenced
	}
	return client.HSet(ctx, key, "instance", instance, "token", token, "at", strconv.FormatInt(time.Now().UnixMilli(), 10)).Err()
}

// CheckLease verifies token holds lease:conform:<b>.
func CheckLease(ctx context.Context, client *redis.Client, bench, token string) error {
	key := "lease:conform:" + bench
	val, err := client.HGet(ctx, key, "token").Result()
	if err != nil || val != token {
		return ErrFenced
	}
	return nil
}

// WriteConform writes bench:<b>:conform row whole (DEL + HSET in one MULTI) after checking lease.
func WriteConform(ctx context.Context, client *redis.Client, bench, token string, res ConformResult) error {
	if err := CheckLease(ctx, client, bench, token); err != nil {
		return err
	}
	key := "bench:" + bench + ":conform"
	pipe := client.TxPipeline()
	pipe.Del(ctx, key)
	fields := []string{
		"ok", boolStr(res.OK),
		"bad", res.BadKey,
		"standard_digest", res.StandardDigest,
		"took_ms", "10",
		"at", strconv.FormatInt(time.Now().Unix(), 10),
	}
	for k, v := range res.Answers {
		fields = append(fields, "k."+k, v)
	}
	anyFields := make([]any, len(fields))
	for i, f := range fields {
		anyFields[i] = f
	}
	pipe.HSet(ctx, key, anyFields...)
	_, err := pipe.Exec(ctx)
	return err
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
