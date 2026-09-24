package sprint

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The key shapes, and nothing beyond them (Johnny's division of labour, adopted: Redis holds
// ids, counts and event ids and NOTHING else -- never a diff, a prompt or a transcript, so
// Valkey stays a drop-in and the hot store never becomes the database):
//
//	sprint:<name>         hash  {goal, opened_at, closed_at, planned_close_at,
//	                             done, units, percent, eta_minutes, evaluated}
//	sprint:<name>:tasks   set   of task ids
//	task:<id>             hash  {kind, ref, owner, route, route_reason, state, est_minutes,
//	                             leased_at, done_at, actual_minutes, evidence, depends_on,
//	                             paths, leg, locality, isolation, routes, cost_ceiling_usd,
//	                             repo, base, reader, priority, created_at}
//	q:<consumer>          stream the consumer reads; q:<consumer>:front is its priority one
//	friend:<name>         string with a TTL: the heartbeat, written by the friend's harness
//	bench:<name>          hash with a TTL: the bench's own row, written by its own seat
//
// depends_on and paths are comma lists: a set per task would be two more round trips for a
// field that is three ids long, and the whole point of the line is that it answers in under
// a second.
const (
	sprintPrefix = "sprint:"
	taskPrefix   = "task:"
	friendPrefix = "friend:"
	benchPrefix  = "bench:"
	// QueuePrefix is where a consumer's stream lives. The consumer's NAME is the only
	// variable: there is no fixed set of queues (see internal/deal).
	QueuePrefix = "q:"
	// MaxLen is the approximate cap XADD trims a consumer stream to.
	MaxLen = 100_000
)

// RedisStore is the fleet Redis (one instance on the tailnet, ACL user per role, password
// through nova-secrets, never public).
type RedisStore struct {
	rdb *redis.Client
}

// Dial opens the store. addr is host:port; user and password come from the caller, which
// got them from nova-secrets -- this package never reads a secret from a file or an
// environment of its own, and never prints one.
func Dial(addr, user, password string) (*RedisStore, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("the store address is required; it wants host:port of the fleet Redis; refusing to guess")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis at %s: %w", addr, err)
	}
	return &RedisStore{rdb: rdb}, nil
}

// NewRedisStore wraps a client a caller already holds (the tests' miniredis, another verb's
// pool).
func NewRedisStore(rdb *redis.Client) *RedisStore { return &RedisStore{rdb: rdb} }

// Close releases the pool.
func (s *RedisStore) Close() error {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Close()
}

// PutSprint writes the sprint hash.
func (s *RedisStore) PutSprint(ctx context.Context, sp Sprint) error {
	if err := ValidateName("sprint", sp.Name); err != nil {
		return err
	}
	fields := map[string]interface{}{
		"goal":             sp.Goal,
		"opened_at":        stamp(sp.OpenedAt),
		"closed_at":        stamp(sp.ClosedAt),
		"planned_close_at": stamp(sp.PlannedCloseAt),
	}
	return s.rdb.HSet(ctx, sprintPrefix+sp.Name, fields).Err()
}

// GetSprint reads one back.
func (s *RedisStore) GetSprint(ctx context.Context, name string) (Sprint, error) {
	m, err := s.rdb.HGetAll(ctx, sprintPrefix+name).Result()
	if err != nil {
		return Sprint{}, err
	}
	if len(m) == 0 {
		return Sprint{}, fmt.Errorf("no sprint named %q is open; run: nova-pulse sprint open %s --goal <one sentence>", name, name)
	}
	return Sprint{
		Name:           name,
		Goal:           m["goal"],
		OpenedAt:       unstamp(m["opened_at"]),
		ClosedAt:       unstamp(m["closed_at"]),
		PlannedCloseAt: unstamp(m["planned_close_at"]),
		Done:           atoi(m["done"]),
		Units:          atoi(m["units"]),
		Percent:        atoi(m["percent"]),
		ETAMinutes:     atoi(m["eta_minutes"]),
	}, nil
}

// PutProgress writes the four fields the table reads. It does not rewrite the
// goal or the times: a state change must not look like the sprint was reopened.
func (s *RedisStore) PutProgress(ctx context.Context, name string, p Progress) error {
	if err := ValidateName("sprint", name); err != nil {
		return err
	}
	n, err := s.rdb.Exists(ctx, sprintPrefix+name).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no sprint named %q is open; run: nova-pulse sprint open %s --goal <one sentence>", name, name)
	}
	return s.rdb.HSet(ctx, sprintPrefix+name, progressFields(p)).Err()
}

// PublishProgress is the optimistic write. The task set and each member task
// are watched with the sprint hash, so a close or a newer evaluation that
// lands after this read fails EXEC instead of overwriting that newer snapshot.
// measure runs inside the watch; a slow measure does not get to publish the
// view it started with once the keys have moved.
func (s *RedisStore) PublishProgress(ctx context.Context, name string, measure func(ProgressView) (Progress, error)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ValidateName("sprint", name); err != nil {
		return false, err
	}
	sprintKey := sprintPrefix + name
	setKey := sprintKey + ":tasks"
	ids, err := s.rdb.SMembers(ctx, setKey).Result()
	if err != nil {
		return false, err
	}
	sort.Strings(ids)
	keys := make([]string, 0, len(ids)+2)
	keys = append(keys, sprintKey, setKey)
	for _, id := range ids {
		keys = append(keys, taskPrefix+id)
	}
	var moved bool
	err = s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		n, err := tx.Exists(ctx, sprintKey).Result()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no sprint named %q is open; run: nova-pulse sprint open %s --goal <one sentence>", name, name)
		}
		got, err := tx.SMembers(ctx, setKey).Result()
		if err != nil {
			return err
		}
		sort.Strings(got)
		if !sameIDs(ids, got) {
			moved = true
			return nil
		}
		view, err := readProgressView(ctx, tx, sprintKey, ids)
		if err != nil {
			return err
		}
		p, err := measure(view)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, sprintKey, progressFields(p))
			return nil
		})
		return err
	}, keys...)
	if moved && err == nil {
		return false, nil
	}
	if err == redis.TxFailedErr {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func readProgressView(ctx context.Context, tx *redis.Tx, sprintKey string, ids []string) (ProgressView, error) {
	pipe := tx.Pipeline()
	sm := pipe.HGetAll(ctx, sprintKey)
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, taskPrefix+id)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return ProgressView{}, err
	}
	m, err := sm.Result()
	if err != nil && err != redis.Nil {
		return ProgressView{}, err
	}
	var tasks []Task
	for i, cmd := range cmds {
		hm, err := cmd.Result()
		if err != nil && err != redis.Nil {
			return ProgressView{}, err
		}
		if len(hm) == 0 {
			continue
		}
		tasks = append(tasks, taskFrom(ids[i], hm))
	}
	return ProgressView{Tasks: tasks, Acceptance: acceptanceFrom(m)}, nil
}

// Sprints is every sprint in the store. It SCANs rather than KEYS: a blocking KEYS on the
// fleet store would stall every bench's row push for as long as it ran.
func (s *RedisStore) Sprints(ctx context.Context) ([]Sprint, error) {
	names, err := s.scan(ctx, sprintPrefix+"*")
	if err != nil {
		return nil, err
	}
	var out []Sprint
	for _, key := range names {
		name := strings.TrimPrefix(key, sprintPrefix)
		if strings.HasSuffix(name, ":tasks") {
			continue
		}
		sp, err := s.GetSprint(ctx, name)
		if err != nil {
			continue
		}
		out = append(out, sp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.Before(out[j].OpenedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// PutTask writes one task hash.
func (s *RedisStore) PutTask(ctx context.Context, t Task) error {
	if err := ValidateName("task", t.ID); err != nil {
		return err
	}
	if err := ValidateKind(t.Kind); err != nil {
		return err
	}
	if t.State == "" {
		t.State = StateOpen
	}
	if err := ValidateState(t.State); err != nil {
		return err
	}
	return s.rdb.HSet(ctx, taskPrefix+t.ID, taskFields(t)).Err()
}

func taskFields(t Task) map[string]interface{} {
	return map[string]interface{}{
		"kind":             t.Kind,
		"ref":              t.Ref,
		"owner":            t.Owner,
		"route":            t.Route,
		"route_reason":     t.RouteReason,
		"state":            t.State,
		"est_minutes":      t.EstMinutes,
		"leased_at":        stamp(t.LeasedAt),
		"done_at":          stamp(t.DoneAt),
		"actual_minutes":   t.Actual,
		"evidence":         t.Evidence,
		"depends_on":       strings.Join(t.DependsOn, ","),
		"paths":            strings.Join(t.Paths, ","),
		"leg":              t.Leg,
		"locality":         t.Locality,
		"isolation":        t.Isolation,
		"routes":           strings.Join(t.Routes, ","),
		"cost_ceiling_usd": strconv.FormatFloat(t.CostCeilingUSD, 'f', -1, 64),
		"repo":             t.Repo,
		"base":             t.Base,
		"reader":           t.Reader,
		"priority":         boolField(t.Priority),
		"created_at":       stamp(t.CreatedAt),
	}
}

// GetTask reads one back.
func (s *RedisStore) GetTask(ctx context.Context, id string) (Task, error) {
	m, err := s.rdb.HGetAll(ctx, taskPrefix+id).Result()
	if err != nil {
		return Task{}, err
	}
	if len(m) == 0 {
		return Task{}, fmt.Errorf("no task %q in the store", id)
	}
	return taskFrom(id, m), nil
}

func taskFrom(id string, m map[string]string) Task {
	return Task{
		ID:             id,
		Kind:           m["kind"],
		Ref:            m["ref"],
		Owner:          m["owner"],
		Route:          m["route"],
		RouteReason:    m["route_reason"],
		State:          m["state"],
		EstMinutes:     atoi(m["est_minutes"]),
		LeasedAt:       unstamp(m["leased_at"]),
		DoneAt:         unstamp(m["done_at"]),
		Actual:         atoi(m["actual_minutes"]),
		Evidence:       m["evidence"],
		DependsOn:      list(m["depends_on"]),
		Paths:          list(m["paths"]),
		Leg:            m["leg"],
		Locality:       m["locality"],
		Isolation:      m["isolation"],
		Routes:         list(m["routes"]),
		CostCeilingUSD: atof(m["cost_ceiling_usd"]),
		Repo:           m["repo"],
		Base:           m["base"],
		Reader:         m["reader"],
		Priority:       m["priority"] == "1",
		CreatedAt:      unstamp(m["created_at"]),
	}
}

// AddTask puts the id in the sprint's set.
func (s *RedisStore) AddTask(ctx context.Context, sprintName, taskID string) error {
	n, err := s.rdb.Exists(ctx, sprintPrefix+sprintName).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no sprint named %q is open; open it before adding to it", sprintName)
	}
	return s.rdb.SAdd(ctx, sprintPrefix+sprintName+":tasks", taskID).Err()
}

// Tasks is the sprint's whole set, read in ONE pipeline: the line answers in under a second
// or it is not the line Glenn asked for.
func (s *RedisStore) Tasks(ctx context.Context, sprintName string) ([]Task, error) {
	ids, err := s.rdb.SMembers(ctx, sprintPrefix+sprintName+":tasks").Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, pipe.HGetAll(ctx, taskPrefix+id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	var out []Task
	for i, cmd := range cmds {
		m, err := cmd.Result()
		if err != nil || len(m) == 0 {
			continue
		}
		out = append(out, taskFrom(ids[i], m))
	}
	return out, nil
}

// Presence reads the heartbeats: `friend:<name>` written by a friend's harness (#2612) and
// `bench:<name>` written by the bench's own seat. A key that is not there is AWAY -- that is
// what the TTL is for, and it is the only thing presence is ever read from.
func (s *RedisStore) Presence(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	for _, prefix := range []string{friendPrefix, benchPrefix} {
		keys, err := s.scan(ctx, prefix+"*")
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			name := strings.ToLower(strings.TrimPrefix(k, prefix))
			if name == "" || strings.Contains(name, ":") {
				continue
			}
			out[name] = true
		}
	}
	return out, nil
}

// QueueDepths is XLEN over each consumer's streams, in one pipeline.
func (s *RedisStore) QueueDepths(ctx context.Context, consumers []string) (map[string]int, error) {
	pipe := s.rdb.Pipeline()
	type pair struct{ bulk, front *redis.IntCmd }
	cmds := make(map[string]pair, len(consumers))
	for _, c := range consumers {
		cmds[c] = pair{pipe.XLen(ctx, QueuePrefix+c), pipe.XLen(ctx, QueuePrefix+c+":front")}
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := map[string]int{}
	for name, p := range cmds {
		out[name] = int(p.bulk.Val() + p.front.Val())
	}
	return out, nil
}

// Place is the XADD a consumer reads. A friend's task and a bench's card are the same entry
// on the same kind of stream: that is the whole of "there is no reason for these things to
// be apart".
func (s *RedisStore) Place(ctx context.Context, queue string, t Task) (string, error) {
	if strings.TrimSpace(queue) == "" {
		return "", fmt.Errorf("the queue name is required; it is q:<consumer>")
	}
	values := map[string]interface{}{"task": t.ID, "kind": t.Kind, "ref": t.Ref, "est_minutes": t.EstMinutes}
	if t.Reader != "" {
		values["reader"] = t.Reader
	}
	if len(t.Routes) > 0 {
		values["routes"] = strings.Join(t.Routes, ",")
	}
	return s.rdb.XAdd(ctx, &redis.XAddArgs{Stream: queue, MaxLen: MaxLen, Approx: true, Values: values}).Result()
}

func (s *RedisStore) scan(ctx context.Context, match string) ([]string, error) {
	var (
		cursor uint64
		out    []string
	)
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, match, 500).Result()
		if err != nil {
			return nil, err
		}
		out = append(out, keys...)
		if next == 0 {
			break
		}
		cursor = next
	}
	sort.Strings(out)
	return out, nil
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func unstamp(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func atof(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolField(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
