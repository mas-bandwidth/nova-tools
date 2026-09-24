package land

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const VisibleWindow = 10 * time.Minute

// PendingError means the intent remains durable and a later observation must
// reconcile it before another issue is filed.
type PendingError struct{ Err error }

func (e *PendingError) Error() string { return e.Err.Error() }
func (e *PendingError) Unwrap() error { return e.Err }

// HTTPStatusError lets the store distinguish a definite rejected POST from
// failures that may have committed remotely.
type HTTPStatusError struct {
	Code int
	Body string
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("forge HTTP %d: %s", e.Code, e.Body) }

type RedisStore struct {
	client *redis.Client
	sprint string
}

func NewRedisStore(client *redis.Client, sprint string) *RedisStore {
	return &RedisStore{client: client, sprint: sprint}
}

func token() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func stringsReply(v any) ([]string, error) {
	a, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("land: invalid ns_flaky_observe reply %T", v)
	}
	out := make([]string, len(a))
	for i := range a {
		out[i] = fmt.Sprint(a[i])
	}
	return out, nil
}

func recordReply(a []string) FlakyRecord {
	var r FlakyRecord
	if len(a) > 0 {
		r.Status = a[0]
	}
	if len(a) > 1 {
		r.FirstSeen = a[1]
	}
	if len(a) > 2 {
		r.LanesHit, _ = strconv.Atoi(a[2])
	}
	if len(a) > 3 {
		r.Issue, _ = strconv.Atoi(a[3])
	}
	if len(a) > 4 {
		r.LastAt = a[4]
	}
	if len(a) > 5 {
		r.LastLane = a[5]
	}
	if len(a) > 6 {
		r.At = a[6]
	}
	if len(a) > 7 {
		r.Token = a[7]
	}
	if len(a) > 8 {
		r.Extra, _ = strconv.Atoi(a[8])
		r.Reconciled = a[8] == "reconciled"
	}
	return r
}

func (s *RedisStore) call(ctx context.Context, obs Observation, tok string, arg ...string) ([]string, error) {
	argv := []interface{}{s.sprint, obs.Key, obs.Lane, tok}
	if len(arg) > 0 {
		argv = append(argv, arg[0])
	}
	v, err := s.client.FCall(ctx, "ns_flaky_observe", []string{}, argv...).Result()
	if err != nil {
		return nil, err
	}
	return stringsReply(v)
}

func (s *RedisStore) Observe(context.Context, string, string, func() (int, error)) (FlakyRecord, bool, error) {
	return FlakyRecord{}, false, errors.New("land: RedisStore requires ObserveLive with a reconciliation filer")
}

func (s *RedisStore) ObserveLive(ctx context.Context, obs Observation, filer LiveFiler) (FlakyRecord, bool, error) {
	if s == nil || s.client == nil || strings.TrimSpace(s.sprint) == "" {
		return FlakyRecord{}, false, errors.New("land: redis store and sprint are required")
	}
	if obs.Repo == "" || obs.Key == "" || obs.Lane == "" || filer == nil {
		return FlakyRecord{}, false, errors.New("land: repo, key, lane and filer are required")
	}
	tok, err := token()
	if err != nil {
		return FlakyRecord{}, false, err
	}
	a, err := s.call(ctx, obs, tok)
	if err != nil {
		return FlakyRecord{}, false, err
	}
	if len(a) == 0 {
		return FlakyRecord{}, false, errors.New("land: empty ns_flaky_observe reply")
	}
	switch a[0] {
	case "SEEN", "FILING", "DUPLICATE", "FILED":
		r := recordReply(a)
		return r, a[0] == "FILED", nil
	case "NEW":
		if len(a) < 5 {
			return FlakyRecord{}, false, fmt.Errorf("land: short NEW reply %q", a)
		}
		reconcile := a[1] == "1"
		atMS, _ := strconv.ParseInt(a[3], 10, 64)
		ageMS, _ := strconv.ParseInt(a[4], 10, 64)
		if reconcile {
			n, found, findErr := filer.Find(ctx, obs.Repo, "dedup="+obs.Key, time.UnixMilli(atMS).UTC())
			if findErr != nil {
				return FlakyRecord{}, false, &PendingError{findErr}
			}
			if found {
				return s.finish(ctx, obs, tok, n, true)
			}
			if time.Duration(ageMS)*time.Millisecond < VisibleWindow {
				return FlakyRecord{}, false, &PendingError{fmt.Errorf("not visible, age=%ds window=%ds", ageMS/1000, int64(VisibleWindow/time.Second))}
			}
			if a, err = s.call(ctx, obs, tok, "post"); err != nil || len(a) == 0 || a[0] != "POST" {
				if err == nil {
					err = fmt.Errorf("post transition: %q", a)
				}
				return FlakyRecord{}, false, &PendingError{err}
			}
		}
		n, fileErr := filer.File(ctx, obs.Repo, obs.Title, obs.Body)
		if fileErr != nil {
			var hs *HTTPStatusError
			if errors.As(fileErr, &hs) && hs.Code >= 400 && hs.Code < 500 && hs.Code != 408 && hs.Code != 429 {
				_, _ = s.call(context.WithoutCancel(ctx), obs, tok, "abort")
			}
			return FlakyRecord{}, false, &PendingError{fileErr}
		}
		return s.finish(ctx, obs, tok, n, reconcile)
	default:
		return FlakyRecord{}, false, fmt.Errorf("land: ns_flaky_observe returned %q", a[0])
	}
}

func (s *RedisStore) finish(ctx context.Context, obs Observation, tok string, issue int, reconciled bool) (FlakyRecord, bool, error) {
	if issue <= 0 {
		return FlakyRecord{}, false, fmt.Errorf("land: filer returned issue %d", issue)
	}
	a, err := s.call(ctx, obs, tok, strconv.Itoa(issue))
	if err != nil {
		a, err = s.call(ctx, obs, tok, strconv.Itoa(issue))
	}
	if err != nil {
		return FlakyRecord{}, false, &PendingError{err}
	}
	r := recordReply(a)
	r.Reconciled = r.Reconciled || reconciled
	if r.Status == "DUPLICATE" {
		return r, false, nil
	}
	if r.Status != "FILED" {
		return r, false, fmt.Errorf("land: FILED transition returned %q", r.Status)
	}
	return r, true, nil
}

func (s *RedisStore) List(ctx context.Context, repo string) ([]struct {
	Key    string
	Record FlakyRecord
}, error) {
	keys, err := s.client.SMembers(ctx, "flaky:idx").Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	pipe := s.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.HGetAll(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]struct {
		Key    string
		Record FlakyRecord
	}, 0, len(keys))
	for i, k := range keys {
		if repo != "" && !strings.HasPrefix(k, "flaky:"+repo+":") {
			continue
		}
		m := cmds[i].Val()
		if len(m) == 0 {
			continue
		}
		r := FlakyRecord{FirstSeen: m["first_seen"], LastAt: m["last_at"], LastLane: m["last_lane"], At: m["at"], Token: m["token"]}
		r.LanesHit, _ = strconv.Atoi(m["lanes_hit"])
		r.Issue, _ = strconv.Atoi(m["issue"])
		out = append(out, struct {
			Key    string
			Record FlakyRecord
		}{k, r})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Record.LanesHit == out[j].Record.LanesHit {
			return out[i].Key < out[j].Key
		}
		return out[i].Record.LanesHit > out[j].Record.LanesHit
	})
	return out, nil
}
