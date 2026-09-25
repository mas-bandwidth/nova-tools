package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/redis/go-redis/v9"
)

type c42Find struct {
	issue int
	found bool
	err   error
}
type c42Filer struct {
	fileResults []c42Find
	finds       []c42Find
	files, gets int
}

func (f *c42Filer) File(context.Context, string, string, string) (int, error) {
	f.files++
	r := f.fileResults[0]
	f.fileResults = f.fileResults[1:]
	return r.issue, r.err
}
func (f *c42Filer) Find(context.Context, string, string, time.Time) (int, bool, error) {
	f.gets++
	r := f.finds[0]
	f.finds = f.finds[1:]
	return r.issue, r.found, r.err
}

func expireC42Lock(t *testing.T, c *redis.Client, key string) {
	t.Helper()
	lock := "flaky:lock:" + strings.TrimPrefix(key, "flaky:")
	if err := c.PExpire(context.Background(), lock, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
}
func ageC42Intent(t *testing.T, c *redis.Client, key string) {
	t.Helper()
	ctx := context.Background()
	tm, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	intent := "flaky:intent:" + strings.TrimPrefix(key, "flaky:")
	if err := c.HSet(ctx, intent, "at_ms", tm.UnixMilli()-land.VisibleWindow.Milliseconds()-1).Err(); err != nil {
		t.Fatal(err)
	}
}

type c42Forge struct {
	states []string
	calls  int
}

func (f *c42Forge) Mergeable(context.Context, string, int) (string, error) {
	f.calls++
	s := f.states[0]
	f.states = f.states[1:]
	return s, nil
}

type c42Gate struct{ calls int }

func (g *c42Gate) Run(context.Context, land.Batch, int) (land.Verdict, error) {
	g.calls++
	return land.Verdict{OK: true}, nil
}

type c42Bisect struct{}

func (c42Bisect) Alone(context.Context, land.Batch, land.Verdict) (bool, []bool, error) {
	return true, nil, nil
}

type c42Lander struct{ calls int }

func (l *c42Lander) Land(context.Context, land.Batch, land.Verdict) error { l.calls++; return nil }

type c42Clock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *c42Clock) Now() time.Time        { return c.now }
func (c *c42Clock) Sleep(d time.Duration) { c.sleeps = append(c.sleeps, d); c.now = c.now.Add(d) }

func TestControl42Redis(t *testing.T) {
	t.Run("flaky-once", func(t *testing.T) {
		addr, _ := sprintRedis(t)
		posts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Fatalf("method=%s", r.Method)
			}
			posts++
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 42})
		}))
		defer srv.Close()
		t.Setenv("GH_TOKEN", "fixture")
		base := []string{"land", "flaky", "observe", "--redis", addr, "--sprint", "c42r", "--repo", "nova-tools", "--pkg", "internal/deal", "--test", "TestProbe3098", "--forge-api", srv.URL}
		code, out, stderr := runSprint(append(base, "--lane", "lane-a")...)
		if code != 0 || !strings.Contains(out, "FLAKY FILED") {
			t.Fatalf("first code=%d out=%q err=%q", code, out, stderr)
		}
		code, out, stderr = runSprint(append(base, "--lane", "lane-b")...)
		if code != 0 || !strings.Contains(out, "FLAKY SEEN") || !strings.Contains(out, "lanes_hit=2") {
			t.Fatalf("second code=%d out=%q err=%q", code, out, stderr)
		}
		if posts != 1 {
			t.Fatalf("POSTs=%d want 1", posts)
		}
		code, out, stderr = runSprint("land", "flaky", "list", "--redis", addr, "--repo", "nova-tools")
		if code != 0 || !strings.Contains(out, "last_lane=lane-b") {
			t.Fatalf("list code=%d out=%q err=%q", code, out, stderr)
		}
	})

	t.Run("commit-then-error", func(t *testing.T) {
		t.Run("response-lost", func(t *testing.T) {
			_, c := sprintRedis(t)
			st := land.NewRedisStore(c, "c42r")
			key := "flaky:nova-tools:p.ResponseLost"
			obs := land.Observation{Repo: "nova-tools", Key: key, Lane: "a", Title: "x", Body: "dedup=" + key + "\n"}
			f := &c42Filer{fileResults: []c42Find{{issue: 41, err: errors.New("response lost")}}, finds: []c42Find{{issue: 41, found: true}}}
			if _, _, err := st.ObserveLive(context.Background(), obs, f); err == nil {
				t.Fatal("lost response succeeded")
			}
			expireC42Lock(t, c, key)
			r, filed, err := st.ObserveLive(context.Background(), obs, f)
			if err != nil || !filed || !r.Reconciled || r.Issue != 41 || f.files != 1 || f.gets != 1 {
				t.Fatalf("r=%+v filed=%t err=%v files=%d gets=%d", r, filed, err, f.files, f.gets)
			}
		})
		t.Run("visible-late", func(t *testing.T) {
			_, c := sprintRedis(t)
			st := land.NewRedisStore(c, "c42r")
			key := "flaky:nova-tools:p.VisibleLate"
			obs := land.Observation{Repo: "nova-tools", Key: key, Lane: "a", Title: "x", Body: "dedup=" + key + "\n"}
			f := &c42Filer{fileResults: []c42Find{{issue: 42, err: errors.New("response lost")}}, finds: []c42Find{{}, {issue: 42, found: true}}}
			_, _, _ = st.ObserveLive(context.Background(), obs, f)
			expireC42Lock(t, c, key)
			if _, _, err := st.ObserveLive(context.Background(), obs, f); err == nil || !strings.Contains(err.Error(), "not visible") {
				t.Fatalf("first reconcile err=%v", err)
			}
			expireC42Lock(t, c, key)
			r, _, err := st.ObserveLive(context.Background(), obs, f)
			if err != nil || !r.Reconciled || f.files != 1 || f.gets != 2 {
				t.Fatalf("r=%+v err=%v files=%d gets=%d", r, err, f.files, f.gets)
			}
		})
		t.Run("late-duplicate", func(t *testing.T) {
			_, c := sprintRedis(t)
			st := land.NewRedisStore(c, "c42r")
			key := "flaky:nova-tools:p.LateDuplicate"
			obs := land.Observation{Repo: "nova-tools", Key: key, Lane: "a", Title: "x", Body: "dedup=" + key + "\n"}
			f := &c42Filer{fileResults: []c42Find{{issue: 41, err: errors.New("response lost")}, {issue: 42}}, finds: []c42Find{{}, {}}}
			_, _, _ = st.ObserveLive(context.Background(), obs, f)
			expireC42Lock(t, c, key)
			_, _, _ = st.ObserveLive(context.Background(), obs, f)
			ageC42Intent(t, c, key)
			expireC42Lock(t, c, key)
			r, _, err := st.ObserveLive(context.Background(), obs, f)
			if err != nil || r.Issue != 42 || f.files != 2 || f.gets != 2 {
				t.Fatalf("r=%+v err=%v files=%d gets=%d", r, err, f.files, f.gets)
			}
			r, _, err = st.ObserveLive(context.Background(), obs, f)
			if err != nil || r.Status != "SEEN" || r.Issue != 42 {
				t.Fatalf("seen r=%+v err=%v", r, err)
			}
		})
		for _, name := range []string{"redis-lost", "reply-lost"} {
			t.Run(name, func(t *testing.T) {
				_, c := sprintRedis(t)
				ctx := context.Background()
				key := "flaky:nova-tools:p." + name
				args := []interface{}{"c42r", key, "a", "tok"}
				a, err := c.FCall(ctx, "ns_flaky_observe", nil, args...).Result()
				if err != nil {
					t.Fatal(err)
				}
				_ = a
				if _, err = c.FCall(ctx, "ns_flaky_observe", nil, "c42r", key, "a", "tok", "43").Result(); err != nil {
					t.Fatal(err)
				}
				again, err := c.FCall(ctx, "ns_flaky_observe", nil, "c42r", key, "a", "tok", "43").Result()
				if err != nil || !strings.Contains(fmt.Sprint(again), "FILED") {
					t.Fatalf("again=%v err=%v", again, err)
				}
			})
		}
		t.Run("never-committed", func(t *testing.T) {
			_, c := sprintRedis(t)
			st := land.NewRedisStore(c, "c42r")
			key := "flaky:nova-tools:p.NeverCommitted"
			obs := land.Observation{Repo: "nova-tools", Key: key, Lane: "a", Title: "x", Body: "dedup=" + key + "\n"}
			f := &c42Filer{fileResults: []c42Find{{err: errors.New("closed")}, {issue: 44}}, finds: []c42Find{{}, {}}}
			_, _, _ = st.ObserveLive(context.Background(), obs, f)
			expireC42Lock(t, c, key)
			_, _, _ = st.ObserveLive(context.Background(), obs, f)
			ageC42Intent(t, c, key)
			expireC42Lock(t, c, key)
			r, _, err := st.ObserveLive(context.Background(), obs, f)
			if err != nil || r.Issue != 44 || f.files != 2 {
				t.Fatalf("r=%+v err=%v files=%d", r, err, f.files)
			}
		})
	})

	for _, tc := range []struct {
		name        string
		states      []string
		wantDropped bool
	}{{"terminal-unknown", []string{"UNKNOWN", "UNKNOWN", "UNKNOWN"}, true}, {"unknown-then-mergeable", []string{"UNKNOWN", "UNKNOWN", "MERGEABLE"}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			forge := &c42Forge{states: append([]string(nil), tc.states...)}
			gate := &c42Gate{}
			lander := &c42Lander{}
			clock := &c42Clock{now: time.Unix(0, 0)}
			lane := land.Lane{Gate: gate, Bisect: c42Bisect{}, Forge: forge, Land: lander, Store: land.NewMemory(), Filer: &c42Filer{}, Clock: clock}
			res, err := lane.Run(context.Background(), land.Batch{Repo: "nova-tools", Name: "c42", Members: []land.Member{{Number: 13, Head: strconv.Itoa(13)}}})
			if err != nil {
				t.Fatal(err)
			}
			if forge.calls != 3 || len(clock.sleeps) != 2 {
				t.Fatalf("calls=%d sleeps=%v", forge.calls, clock.sleeps)
			}
			if tc.wantDropped != (len(res.Dropped) == 1) {
				t.Fatalf("dropped=%+v", res.Dropped)
			}
			if !tc.wantDropped && !res.Landed {
				t.Fatalf("not landed: %+v", res)
			}
		})
	}

	t.Run("duplicate-detected", func(t *testing.T) {
		_, c := sprintRedis(t)
		ctx := context.Background()
		key := "flaky:nova-tools:p.Duplicate"
		_, _ = c.FCall(ctx, "ns_flaky_observe", nil, "c42r", key, "a", "tok").Result()
		_, _ = c.FCall(ctx, "ns_flaky_observe", nil, "c42r", key, "a", "tok", "51").Result()
		got, err := c.FCall(ctx, "ns_flaky_observe", nil, "c42r", key, "b", "other", "52").Result()
		if err != nil || !strings.Contains(fmt.Sprint(got), "DUPLICATE") {
			t.Fatalf("got=%v err=%v", got, err)
		}
	})
}
