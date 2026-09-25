package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fixture is one run's world: a throwaway Redis, a binaries dir, a receipts
// dir and a nova-tools-shaped git repo in which Ada's one commit adds
// cmd/nova-fix/main.go holding the literal "links".
type fixture struct {
	t        *testing.T
	mr       *miniredis.Miniredis
	client   *redis.Client
	tools    string
	repo     string
	receipts string
	now      time.Time
	seq      int
}

var defaultBins = map[string][]string{
	"nova-fix":    {"nova-fix", "nova-fix links"},
	"nova-sprint": {"nova-sprint help", "nova-sprint land eval"},
	"nova-check":  {"nova-check help"},
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	f := &fixture{
		t: t, mr: mr, client: client,
		tools:    t.TempDir(),
		repo:     t.TempDir(),
		receipts: t.TempDir(),
		now:      time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
	}
	f.git("init", "-q", "-b", "main")
	f.commitAs("Ada", "cmd/nova-fix/main.go", "package main\n\nvar verbs = []string{\"links\"}\n")
	f.bins(defaultBins)
	return f
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) commitAs(who, path, body string) string {
	f.t.Helper()
	full := filepath.Join(f.repo, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git("add", "-A")
	f.git("-c", "user.name="+who, "-c", "user.email="+strings.ToLower(who)+"@example.invalid", "commit", "-q", "-m", who+" "+path)
	return f.git("rev-parse", "HEAD")
}

// bins rewrites the binaries dir: one executable script per tool whose help
// prints the lines given.
func (f *fixture) bins(help map[string][]string) {
	f.t.Helper()
	entries, err := os.ReadDir(f.tools)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(f.tools, e.Name())); err != nil {
			f.t.Fatal(err)
		}
	}
	for tool, lines := range help {
		script := "#!/bin/sh\nexit 1\n"
		if lines != nil {
			script = "#!/bin/sh\nprintf '%s\\n' '" + strings.Join(lines, "' '") + "'\n"
		}
		if err := os.WriteFile(filepath.Join(f.tools, tool), []byte(script), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) cfg() Config {
	return Config{Tools: f.tools, Repo: f.repo, Receipts: f.receipts, Days: 14, Now: func() time.Time { return f.now }}
}

func (f *fixture) runCfg(ctx context.Context, cfg Config) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Unused(ctx, f.client, cfg, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (f *fixture) run() (int, string, string) {
	f.t.Helper()
	return f.runCfg(context.Background(), f.cfg())
}

// mustRun runs and fails the test on anything but exit 0.
func (f *fixture) mustRun() string {
	f.t.Helper()
	code, out, errOut := f.run()
	if code != 0 {
		f.t.Fatalf("verbs unused exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	return out
}

func (f *fixture) check() (int, string, string) {
	var out, errOut bytes.Buffer
	code := Check(context.Background(), f.client, f.repo, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (f *fixture) last() entry {
	f.t.Helper()
	msgs, err := f.client.XRevRangeN(context.Background(), LogKey, "+", "-", 1).Result()
	if err != nil || len(msgs) != 1 {
		f.t.Fatalf("read the newest %s entry: %v %v", LogKey, msgs, err)
	}
	e, err := parseEntry(msgs[0])
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

func (f *fixture) xlen() int64 {
	f.t.Helper()
	n, err := f.client.XLen(context.Background(), LogKey).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// receipt writes one dogfood receipt line to its own file and returns the
// file's name.
func (f *fixture) receipt(tool, verb, by string, at time.Time, notes string) string {
	f.t.Helper()
	f.seq++
	line, err := json.Marshal(map[string]any{"tool": tool, "verb": verb, "by": by, "at": at.UTC().Format(time.RFC3339), "ok": true, "notes": notes})
	if err != nil {
		f.t.Fatal(err)
	}
	name := fmt.Sprintf("r%03d.json", f.seq)
	if err := os.WriteFile(filepath.Join(f.receipts, name), append(line, '\n'), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return name
}

// logAt appends a use receipt to a stream with the id of its time.
func (f *fixture) logAt(stream string, at time.Time, fields ...string) {
	f.t.Helper()
	f.seq++
	id := strconv.FormatInt(at.UnixMilli(), 10) + "-" + strconv.Itoa(f.seq)
	if err := f.client.XAdd(context.Background(), &redis.XAddArgs{Stream: stream, ID: id, Values: fields}).Err(); err != nil {
		f.t.Fatal(err)
	}
}

func has(list []string, key string) bool {
	for _, k := range list {
		if k == key {
			return true
		}
	}
	return false
}

func wantListed(t *testing.T, e entry, in, out []string) {
	t.Helper()
	for _, k := range in {
		if !has(e.Verbs, k) {
			t.Errorf("%q not listed; verbs %q", k, e.Verbs)
		}
	}
	for _, k := range out {
		if has(e.Verbs, k) {
			t.Errorf("%q listed; verbs %q", k, e.Verbs)
		}
	}
}

func wantRefused(t *testing.T, code int, errOut string, want string) {
	t.Helper()
	if code != ExitRefused {
		t.Fatalf("exit %d, want 2; stderr %q", code, errOut)
	}
	if !strings.HasSuffix(errOut, "; run: nova-sprint help\n") || strings.Count(errOut, "\n") != 1 {
		t.Fatalf("stderr %q, want one line ending ; run: nova-sprint help", errOut)
	}
	if !strings.Contains(errOut, want) {
		t.Fatalf("stderr %q, want %q", errOut, want)
	}
}

// hand appends a hand-written entry and returns its id.
func (f *fixture) hand(sha string, verbs []string, resolved map[string]string, prev string) string {
	f.t.Helper()
	v, _ := json.Marshal(nonNil(verbs))
	if resolved == nil {
		resolved = map[string]string{}
	}
	r, _ := json.Marshal(resolved)
	id, err := f.client.XAdd(context.Background(), &redis.XAddArgs{Stream: LogKey, Values: []string{
		"at", f.now.Format(time.RFC3339), "days", "14", "since", "-", "until", "-",
		"dev_sha", sha, "count", strconv.Itoa(len(verbs)), "verbs", string(v), "resolved", string(r), "prev", prev,
	}}).Result()
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestVerbsUnused(t *testing.T) {
	day := 24 * time.Hour
	all := []string{"nova-check help", "nova-fix", "nova-fix links", "nova-sprint help", "nova-sprint land eval"}

	// Window and authorship.
	t.Run("window_edges", func(t *testing.T) {
		f := newFixture(t)
		f.logAt("cap:log", f.now.Add(-14*day-time.Second), "verb", "help", "actor", "rowan")
		f.logAt("cap:log", f.now.Add(-14*day+time.Second), "verb", "land eval", "actor", "rowan")
		name := f.receipt("nova-check", "help", "Bob", f.now.Add(-14*day-time.Second), "#1")
		out := f.mustRun()
		wantListed(t, f.last(), []string{"nova-sprint help", "nova-check help"}, []string{"nova-sprint land eval"})
		if !strings.Contains(out, "VERBS SKIP receipt="+name+":1 why=at") {
			t.Errorf("no SKIP why=at for the receipt a second outside the window:\n%s", out)
		}
	})
	t.Run("own_author_does_not_count", func(t *testing.T) {
		f := newFixture(t)
		name := f.receipt("nova-fix", "links", "Ada", f.now.Add(-time.Hour), "#1")
		out := f.mustRun()
		wantListed(t, f.last(), []string{"nova-fix links"}, nil)
		if !strings.Contains(out, "VERBS SKIP receipt="+name+":1 why=by") {
			t.Errorf("no SKIP why=by:\n%s", out)
		}
	})
	t.Run("non_author_counts", func(t *testing.T) {
		f := newFixture(t)
		f.mustRun()
		wantListed(t, f.last(), []string{"nova-fix links"}, nil)
		name := f.receipt("nova-fix", "links", "Bob", f.now.Add(-time.Hour), "#1")
		f.mustRun()
		e := f.last()
		wantListed(t, e, nil, []string{"nova-fix links"})
		if got := e.Resolved["nova-fix links"]; got != "dogfood:"+name+":1" {
			t.Errorf("resolved[nova-fix links] = %q, want dogfood:%s:1", got, name)
		}
	})
	t.Run("absent_from_binaries_not_listed", func(t *testing.T) {
		f := newFixture(t)
		f.mustRun()
		f.bins(map[string][]string{"nova-fix": defaultBins["nova-fix"], "nova-sprint": defaultBins["nova-sprint"]})
		f.mustRun()
		e := f.last()
		wantListed(t, e, nil, []string{"nova-check help"})
		if got := e.Resolved["nova-check help"]; got != "deleted" {
			t.Errorf("resolved[nova-check help] = %q, want deleted", got)
		}
	})
	t.Run("delete_merged_vs_opened", func(t *testing.T) {
		withGone := map[string][]string{
			"nova-fix":    {"nova-fix", "nova-fix links", "nova-fix gone"},
			"nova-sprint": defaultBins["nova-sprint"],
			"nova-check":  defaultBins["nova-check"],
		}
		// Merged: the delete is on dev and the binaries are rebuilt without it.
		f := newFixture(t)
		f.bins(withGone)
		f.mustRun()
		f.commitAs("Bob", "cmd/nova-fix/main.go", "package main\n\nvar verbs = []string{\"links\"} // gone deleted\n")
		f.bins(defaultBins)
		f.mustRun()
		if got := f.last().Resolved["nova-fix gone"]; got != "deleted" {
			t.Errorf("resolved[nova-fix gone] = %q, want deleted", got)
		}
		if code, out, errOut := f.check(); code != 0 || !strings.Contains(out, "VERBS UNUSED FALLING was=6 now=5\n") {
			t.Errorf("merged delete: check exit %d\n%s%s", code, out, errOut)
		}
		// Opened only: the delete sits on a branch; dev moves on and still ships it.
		g := newFixture(t)
		g.bins(withGone)
		g.mustRun()
		g.git("checkout", "-q", "-b", "delete-gone")
		g.commitAs("Bob", "cmd/nova-fix/main.go", "package main\n\nvar verbs = []string{\"links\"} // gone deleted\n")
		g.git("checkout", "-q", "main")
		g.commitAs("Bob", "README", "unrelated\n")
		g.mustRun()
		if code, out, errOut := g.check(); code != 1 || !strings.Contains(out, "VERBS UNUSED NOT FALLING was=6 now=6\n") {
			t.Errorf("unmerged delete: check exit %d\n%s%s", code, out, errOut)
		}
	})

	// Bare keys.
	t.Run("bare_key_own_author_does_not_count", func(t *testing.T) {
		f := newFixture(t)
		f.receipt("nova-fix", "-", "Ada", f.now.Add(-time.Hour), "#1")
		f.mustRun()
		wantListed(t, f.last(), []string{"nova-fix"}, nil)
	})
	t.Run("bare_key_non_author_counts", func(t *testing.T) {
		f := newFixture(t)
		f.mustRun()
		wantListed(t, f.last(), []string{"nova-fix"}, nil)
		name := f.receipt("nova-fix", "-", "Bob", f.now.Add(-time.Hour), "#1")
		f.mustRun()
		e := f.last()
		wantListed(t, e, nil, []string{"nova-fix"})
		if got := e.Resolved["nova-fix"]; got != "dogfood:"+name+":1" {
			t.Errorf("resolved[nova-fix] = %q, want dogfood:%s:1", got, name)
		}
	})

	// Keys.
	t.Run("bare_verb_maps_to_nova_sprint", func(t *testing.T) {
		f := newFixture(t)
		f.logAt("cap:log", f.now.Add(-time.Hour), "verb", "help", "actor", "rowan")
		f.mustRun()
		wantListed(t, f.last(), []string{"nova-check help"}, []string{"nova-sprint help"})
	})
	t.Run("tool_field_wins", func(t *testing.T) {
		f := newFixture(t)
		f.logAt("cap:log", f.now.Add(-time.Hour), "verb", "help", "actor", "rowan", "tool", "nova-check")
		f.mustRun()
		wantListed(t, f.last(), []string{"nova-sprint help"}, []string{"nova-check help"})
	})
	t.Run("multi_word_key", func(t *testing.T) {
		f := newFixture(t)
		f.logAt("cap:log", f.now.Add(-time.Hour), "verb", "land eval", "actor", "rowan")
		f.mustRun()
		wantListed(t, f.last(), []string{"nova-sprint help"}, []string{"nova-sprint land eval"})
	})
	t.Run("unknown_key_ignored", func(t *testing.T) {
		f := newFixture(t)
		f.logAt("cap:log", f.now.Add(-time.Hour), "verb", "nosuch", "actor", "rowan")
		out := f.mustRun()
		if got := f.last().Verbs; strings.Join(got, ",") != strings.Join(all, ",") {
			t.Errorf("verbs %q, want every key %q", got, all)
		}
		if strings.Count(out, "\n") != 2 || strings.Contains(out, "nosuch") {
			t.Errorf("an unknown key printed something:\n%s", out)
		}
	})

	// Streams.
	t.Run("sprint_log_counts", func(t *testing.T) {
		f := newFixture(t)
		f.client.ZAdd(context.Background(), "sprint:order", redis.Z{Score: float64(f.now.Add(-5 * day).UnixMilli()), Member: "old"})
		f.logAt("s:old:log", f.now.Add(-2*day), "verb", "help", "actor", "stella")
		f.mustRun()
		wantListed(t, f.last(), nil, []string{"nova-sprint help"})
	})
	t.Run("sprint_after_window_ignored", func(t *testing.T) {
		f := newFixture(t)
		f.client.ZAdd(context.Background(), "sprint:order", redis.Z{Score: float64(f.now.Add(time.Hour).UnixMilli()), Member: "late"})
		f.logAt("s:late:log", f.now.Add(-time.Hour), "verb", "help", "actor", "stella")
		out := f.mustRun()
		wantListed(t, f.last(), []string{"nova-sprint help"}, nil)
		if !strings.Contains(out, "VERBS STREAMS n=1 sprints=0\n") {
			t.Errorf("a sprint opened after the window was read:\n%s", out)
		}
	})
	t.Run("streams_line", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		f.client.SAdd(ctx, "sprints", "open1")
		f.client.ZAdd(ctx, "sprint:order",
			redis.Z{Score: float64(f.now.Add(-day).UnixMilli()), Member: "open1"},
			redis.Z{Score: float64(f.now.Add(-5 * day).UnixMilli()), Member: "old"})
		out := f.mustRun()
		if !strings.Contains(out, "VERBS STREAMS n=3 sprints=2\n") {
			t.Errorf("stdout:\n%s", out)
		}
	})

	// Chain.
	t.Run("concurrent_appends_bind_prev", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		universe := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}
		use := func(string) string { return "use" }
		var wg sync.WaitGroup
		errs := make(chan error, 40)
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 5; i++ {
					var listed []string
					for j, k := range universe {
						if (j+g+i)%3 != 0 {
							listed = append(listed, k)
						}
					}
					e := entry{At: f.now, Days: 14, Since: f.now, Until: f.now, DevSHA: "sha", Verbs: listed}
					for {
						_, err := appendEntry(ctx, f.client, e, use, nil)
						if errors.Is(err, errPrevMoved) {
							continue // this caller runs again, as a fold would
						}
						errs <- err
						break
					}
				}
			}(g)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		msgs, err := f.client.XRange(ctx, LogKey, "-", "+").Result()
		if err != nil || len(msgs) != 40 {
			t.Fatalf("%d entries (%v), want 40", len(msgs), err)
		}
		prevVerbs := []string(nil)
		prevID := "0"
		for _, m := range msgs {
			e, err := parseEntry(m)
			if err != nil {
				t.Fatal(err)
			}
			if e.Prev != prevID {
				t.Fatalf("entry %s prev %s, want %s", e.ID, e.Prev, prevID)
			}
			want := map[string]string{}
			for _, k := range prevVerbs {
				if !has(e.Verbs, k) {
					want[k] = "use"
				}
			}
			if fmt.Sprint(want) != fmt.Sprint(nonNilMap(e.Resolved)) {
				t.Fatalf("entry %s resolved %v, want %v", e.ID, e.Resolved, want)
			}
			prevID, prevVerbs = e.ID, e.Verbs
		}
	})
	t.Run("cas_exhausted_appends_nothing", func(t *testing.T) {
		f := newFixture(t)
		cfg := f.cfg()
		cfg.beforeExec = func(ctx context.Context) error {
			return f.client.XAdd(ctx, &redis.XAddArgs{Stream: LogKey, Values: []string{
				"dev_sha", "hook", "count", "0", "verbs", "[]", "resolved", "{}", "prev", "0"}}).Err()
		}
		code, _, errOut := f.runCfg(context.Background(), cfg)
		if code != ExitFenced || !strings.Contains(errOut, "prev-moved tries=3") {
			t.Fatalf("exit %d stderr %q, want 3 prev-moved tries=3", code, errOut)
		}
		msgs, _ := f.client.XRange(context.Background(), LogKey, "-", "+").Result()
		if len(msgs) != 3 {
			t.Fatalf("%d entries, want the hook's 3", len(msgs))
		}
		for _, m := range msgs {
			if m.Values["dev_sha"] != "hook" {
				t.Fatalf("the fenced run appended %v", m.Values)
			}
		}
	})
	t.Run("broken_chain_check", func(t *testing.T) {
		f := newFixture(t)
		f.mustRun()
		f.mustRun()
		sha := f.git("rev-parse", "HEAD")
		id := f.hand(sha, all, nil, "1-1")
		code, out, _ := f.check()
		if code != 1 || !strings.Contains(out, "VERBS UNUSED BROKEN-CHAIN newest="+id+" prev=1-1 want=") {
			t.Fatalf("check exit %d\n%s", code, out)
		}
	})
	t.Run("maxlen_set", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		e := entry{At: f.now, Days: 14, Since: f.now, Until: f.now, DevSHA: "sha", Verbs: []string{"a"}}
		for i := 0; i < 10300; i++ {
			if _, err := appendEntry(ctx, f.client, e, func(string) string { return "use" }, nil); err != nil {
				t.Fatal(err)
			}
		}
		if n := f.xlen(); n < 10000 || n >= 10300 {
			t.Fatalf("XLEN %d after 10,300 appends, want 10,000 <= n < 10,300", n)
		}
	})

	// Refusals: nothing appended, one stderr line naming the help door.
	t.Run("inventory_failure_refuses", func(t *testing.T) {
		f := newFixture(t)
		bins := map[string][]string{"nova-broken": nil}
		for k, v := range defaultBins {
			bins[k] = v
		}
		f.bins(bins)
		code, _, errOut := f.run()
		wantRefused(t, code, errOut, "inventory tool=nova-broken")
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})
	t.Run("inventory_empty_refuses", func(t *testing.T) {
		f := newFixture(t)
		f.bins(nil)
		code, _, errOut := f.run()
		wantRefused(t, code, errOut, "inventory")
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})
	t.Run("authors_failure_refuses", func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg := f.cfg()
		cfg.Git = func(context.Context, string, ...string) (string, error) {
			cancel()
			return "", errors.New("cancelled by the test")
		}
		code, _, errOut := f.runCfg(ctx, cfg)
		wantRefused(t, code, errOut, "authors")
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})
	t.Run("receipts_dir_missing_refuses", func(t *testing.T) {
		f := newFixture(t)
		f.receipts = filepath.Join(f.receipts, "absent")
		code, _, errOut := f.run()
		wantRefused(t, code, errOut, "receipts")
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})
	t.Run("unreadable_receipt_refuses", func(t *testing.T) {
		f := newFixture(t)
		if err := os.Symlink(filepath.Join(f.receipts, "missing"), filepath.Join(f.receipts, "x.json")); err != nil {
			t.Fatal(err)
		}
		code, _, errOut := f.run()
		wantRefused(t, code, errOut, "receipts")
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})
	t.Run("invalid_receipt_skips", func(t *testing.T) {
		f := newFixture(t)
		if err := os.WriteFile(filepath.Join(f.receipts, "bad.json"), []byte("{not json\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := f.mustRun()
		if !strings.Contains(out, "VERBS SKIP receipt=bad.json:1 why=parse\n") {
			t.Fatalf("stdout:\n%s", out)
		}
	})
	t.Run("store_down_refuses", func(t *testing.T) {
		f := newFixture(t)
		addr := f.mr.Addr()
		f.mr.Close()
		var out, errOut bytes.Buffer
		code := Main(context.Background(), []string{"unused", "--store", addr, "--tools", f.tools, "--repo", f.repo, "--receipts", f.receipts}, &out, &errOut)
		wantRefused(t, code, errOut.String(), "store")
		if err := f.mr.Restart(); err != nil {
			t.Fatal(err)
		}
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})
	t.Run("bad_flag_refuses", func(t *testing.T) {
		f := newFixture(t)
		var out, errOut bytes.Buffer
		code := Main(context.Background(), []string{"unused", "--store", f.mr.Addr(), "--tools", f.tools, "--repo", f.repo, "--receipts", f.receipts, "--days", "0"}, &out, &errOut)
		wantRefused(t, code, errOut.String(), "--days")
		if f.xlen() != 0 {
			t.Fatal("a refused run appended")
		}
	})

	// Check edges and verdicts, on hand-written entries.
	five := []string{"a", "b", "c", "d", "e"}
	checkCase := func(name string, setup func(f *fixture, c1, c2 string), wantCode int, wantOut, wantErr string) {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			c1 := f.git("rev-parse", "HEAD")
			c2 := f.commitAs("Bob", "README", "next\n")
			setup(f, c1, c2)
			code, out, errOut := f.check()
			if code != wantCode || !strings.Contains(out, wantOut) || !strings.Contains(errOut, wantErr) {
				t.Fatalf("check exit %d, want %d\nstdout %q (want %q)\nstderr %q (want %q)", code, wantCode, out, wantOut, errOut, wantErr)
			}
		})
	}
	checkCase("check_not_lower_fails", func(f *fixture, c1, c2 string) {
		f.hand(c2, five, nil, f.hand(c1, five, nil, "0"))
	}, 1, "VERBS UNUSED NOT FALLING was=5 now=5\n", "")
	checkCase("same_sha_receipt_falls", func(f *fixture, _, c2 string) {
		f.hand(c2, five[:4], map[string]string{"e": "dogfood:r001.json:1"}, f.hand(c2, five, nil, "0"))
	}, 0, "VERBS UNUSED FALLING was=5 now=4 via=receipts\n", "")
	checkCase("same_sha_unchanged_stale", func(f *fixture, _, c2 string) {
		f.hand(c2, five, nil, f.hand(c2, five, nil, "0"))
	}, 1, "VERBS UNUSED STALE dev_sha="+"", "")
	checkCase("same_sha_unexplained", func(f *fixture, _, c2 string) {
		f.hand(c2, five[:4], nil, f.hand(c2, five, nil, "0"))
	}, 1, `VERBS UNUSED UNEXPLAINED verb="e"`, "")
	checkCase("other_sha_stale", func(f *fixture, c1, c2 string) {
		f.hand(c1, five[:4], map[string]string{"e": "use"}, f.hand(c2, five, nil, "0"))
	}, 1, "VERBS UNUSED STALE dev_sha=", "")
	checkCase("check_missing", func(f *fixture, _, c2 string) {
		f.hand(c2, five, nil, "0")
	}, 1, "VERBS UNUSED MISSING entries=1\n", "")
	checkCase("check_zero_same_sha", func(f *fixture, _, c2 string) {
		f.hand(c2, nil, nil, f.hand(c2, nil, nil, "0"))
	}, 0, "VERBS UNUSED FALLING was=0 now=0 via=receipts\n", "")
	checkCase("check_repo_error", func(f *fixture, c1, _ string) {
		f.hand(c1, five[:4], nil, f.hand(strings.Repeat("d", 40), five, nil, "0"))
	}, 2, "", "repo")
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
