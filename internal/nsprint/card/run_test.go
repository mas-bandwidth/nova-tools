package card_test

// run_test.go: the Go card harness (#3681, run.go), against a throwaway
// redis-server with a card hash and its body, and a fake nova-swarm (this
// test binary re-executed, fakeRunner) that records the argv it got and the
// NAMES of the provider keys in its environment.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const (
	fakeRunnerEnv    = "CARDRUN_FAKE_RUNNER" // set: this binary is nova-swarm
	fakeRunnerSeen   = "CARDRUN_SEEN"        // the dir the runner records into
	fakeRunnerResult = "CARDRUN_RESULT"      // 1: leave a RESULT.md under --results-root
	fakeRunnerRC     = "CARDRUN_RC"          // the exit code
)

// fakeRunner is nova-swarm native: it records argv, the provider key names
// it can see, the card env it was given, and the card bytes; leaves a
// RESULT.md when asked; prints native's state line; exits as told.
func fakeRunner() int {
	seen := os.Getenv(fakeRunnerSeen)
	if err := os.MkdirAll(seen, 0o755); err != nil {
		return 9
	}
	args := os.Args[1:]
	if err := os.WriteFile(filepath.Join(seen, "argv"), []byte(strings.Join(args, "\n")+"\n"), 0o644); err != nil {
		return 9
	}
	var keys []string
	for _, k := range card.ProviderKeys {
		if os.Getenv(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	_ = os.WriteFile(filepath.Join(seen, "keys"), []byte(strings.Join(keys, "\n")), 0o644)
	env := fmt.Sprintf("card=%s out=%s job=%s pw=%t auth=%t\n", os.Getenv("NOVA_CARD"), os.Getenv("NOVA_CARD_OUT"), os.Getenv("NOVA_CARD_JOB"),
		os.Getenv("NOVA_REDIS_BENCH_PASSWORD") != "", os.Getenv("REDISCLI_AUTH") != "")
	_ = os.WriteFile(filepath.Join(seen, "env"), []byte(env), 0o644)
	cardPath, results := "", ""
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--card":
			cardPath = args[i+1]
		case "--results-root":
			results = args[i+1]
		}
	}
	if data, err := os.ReadFile(cardPath); err == nil {
		_ = os.WriteFile(filepath.Join(seen, "card.md"), data, 0o644)
	}
	if os.Getenv(fakeRunnerResult) == "1" {
		job := filepath.Join(results, "job")
		if err := os.MkdirAll(job, 0o755); err != nil {
			return 9
		}
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte("RESULT: "+os.Getenv("NOVA_CARD")+" sha=000000000000\n"), 0o644); err != nil {
			return 9
		}
		fmt.Println("NATIVE OK rc=0 harness=ok")
	} else {
		fmt.Println("NATIVE INCOMPLETE")
	}
	rc := 0
	fmt.Sscanf(os.Getenv(fakeRunnerRC), "%d", &rc)
	return rc
}

// runFixture is one card run's world: a store, a seeded card and body, a
// home, a job and out dir, and the fake runner's environment.
type runFixture struct {
	t      *testing.T
	ctx    context.Context
	st     *store.Store
	client *redis.Client
	sprint string
	label  string
	body   []byte
	sha    string
	home   string
	seen   string
	cfg    card.RunConfig
}

func newRunFixture(t *testing.T, label string) *runFixture {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	root := t.TempDir()
	f := &runFixture{t: t, ctx: context.Background(), st: store.New(client), client: client,
		sprint: "run-s", label: label, home: filepath.Join(root, "home"), seen: filepath.Join(root, "seen")}
	// exact bytes: a CRLF line and no trailing newline, so a stripped or added byte changes the sha
	f.body = []byte("RESULT: " + label + " sha=0\r\nBASE: main\nPATHS: x\n\nbody line")
	sum := sha256.Sum256(f.body)
	f.sha = hex.EncodeToString(sum[:])
	f.seed(f.sha, f.body)
	f.cfg = card.RunConfig{
		Sprint: f.sprint, Label: label, Attempt: 1, Bench: "testbench",
		OutDir: filepath.Join(root, "job", "out"), JobDir: filepath.Join(root, "job"), Home: f.home,
		Runner: self, HarnessBin: "/opt/harness/opencode", Seat: "testseat", Deadline: "60", Tokens: "1000",
		Env: []string{
			fakeRunnerEnv + "=1", fakeRunnerSeen + "=" + f.seen, fakeRunnerResult + "=1",
			"DEEPSEEK_API_KEY=ds-val", "INCEPTION_API_KEY=in-val", "OPENCODE_API_KEY=oc-val", "OPENROUTER_API_KEY=or-val",
			"NOVA_REDIS_BENCH_PASSWORD=bpw", "REDISCLI_AUTH=bpw", "NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD",
			"PATH=" + os.Getenv("PATH"),
		},
	}
	return f
}

func (f *runFixture) seed(sha string, body []byte) {
	f.t.Helper()
	if err := f.client.HSet(f.ctx, card.CardKey(f.sprint, f.label), "label", f.label, "payload_sha", sha, "state", "dealt",
		"repo", "acme/public", "base", "main", "base_sha", strings.Repeat("0", 40), "est", "5").Err(); err != nil {
		f.t.Fatal(err)
	}
	if body != nil {
		if err := f.client.Set(f.ctx, card.BodyKey(f.sprint, sha), body, 0).Err(); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *runFixture) run() card.RunReport { return card.Run(f.ctx, f.st, f.cfg) }

func (f *runFixture) harnessLog() string {
	data, _ := os.ReadFile(filepath.Join(f.cfg.OutDir, "harness.log"))
	return string(data)
}

func (f *runFixture) seenFile(name string) string {
	data, err := os.ReadFile(filepath.Join(f.seen, name))
	if err != nil {
		return ""
	}
	return string(data)
}

func (f *runFixture) ran() bool {
	_, err := os.Stat(filepath.Join(f.seen, "argv"))
	return err == nil
}

// labelFor finds a label the flash spread sends to the given provider.
func labelFor(t *testing.T, tab *route.Table, provider string) string {
	t.Helper()
	for i := 0; i < 512; i++ {
		label := fmt.Sprintf("probe-%d", i)
		row, _, err := tab.Pick("flash", label)
		if err != nil {
			t.Fatal(err)
		}
		if p, _, _ := strings.Cut(row.Launch(), "/"); p == provider {
			return label
		}
	}
	t.Fatalf("no label in 512 picks the %s provider on the flash tier", provider)
	return ""
}

// TestCardRunEachProviderGetsItsOwnKey is the DONE-WHEN control of #3681:
// for every provider of the spread, the runner sees exactly that provider's
// key and no other, the bench Redis password is gone, the card bytes are the
// pushed bytes, START and END are in harness.log, RESULT.md is copied up,
// and the slot is moved aside.
func TestCardRunEachProviderGetsItsOwnKey(t *testing.T) {
	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	providers := map[string]bool{}
	for _, s := range tab.Spread("flash") {
		providers[launchProvider(t, tab, s)] = true
	}
	if len(providers) < 2 {
		t.Fatalf("the flash spread names %d providers; the control needs the spread", len(providers))
	}
	for provider := range providers {
		key, err := card.ProviderKey(provider + "/x")
		if err != nil {
			t.Fatal(err)
		}
		t.Run(provider, func(t *testing.T) {
			f := newRunFixture(t, labelFor(t, tab, provider))
			rep := f.run()
			if rep.Code != 0 || rep.Key != key || rep.Tier != "flash" || rep.RC != 0 {
				t.Fatalf("run: %s (log %q)", rep.Line(), f.harnessLog())
			}
			if got := f.seenFile("keys"); got != key {
				t.Fatalf("runner saw keys %q, want exactly %s", got, key)
			}
			if got := f.seenFile("card.md"); got != string(f.body) {
				t.Fatalf("runner got card bytes %q, want the pushed %q", got, f.body)
			}
			wantEnv := fmt.Sprintf("card=%s/%s/1 out=%s job=%s pw=false auth=false\n", f.sprint, f.label, f.cfg.OutDir, f.cfg.JobDir)
			if got := f.seenFile("env"); got != wantEnv {
				t.Fatalf("runner env %q, want %q", got, wantEnv)
			}
			argv := f.seenFile("argv")
			for _, want := range []string{"native\n", "--harness\n/opt/harness/opencode\n", "--model\n" + rep.Model + "\n",
				"--label\n" + f.label + "\n", "--owner\ntestseat\n", "--deadline\n60\n", "--tokens\n1000\n",
				"--slots-store\n" + filepath.Join(f.home, "nova-bench", "slots") + "\n",
				"--root\n" + filepath.Join(f.home, "rowan-working", "tmp") + "\n",
				"--results-root\n" + filepath.Join(f.cfg.OutDir, "native") + "\n"} {
				if !strings.Contains(argv, want) {
					t.Fatalf("runner argv lacks %q:\n%s", want, argv)
				}
			}
			log := f.harnessLog()
			start := fmt.Sprintf("START %s/%s/1 bench=testbench tier=flash route=%s model=%s key=%s sha=%s", f.sprint, f.label, rep.Route, rep.Model, key, f.sha[:12])
			if !strings.Contains(log, start) || !strings.Contains(log, "END native rc=0 wall_s=") {
				t.Fatalf("harness.log lacks START %q or END:\n%s", start, log)
			}
			for _, v := range []string{"ds-val", "in-val", "oc-val", "or-val", "bpw"} {
				if strings.Contains(log, v) || strings.Contains(rep.Line(), v) {
					t.Fatalf("a secret value reached the log or the line: %s", v)
				}
			}
			if data, err := os.ReadFile(filepath.Join(f.cfg.OutDir, "RESULT.md")); err != nil || !strings.HasPrefix(string(data), "RESULT: ") {
				t.Fatalf("RESULT.md not copied up: %v %q", err, data)
			}
			slot := filepath.Join(f.home, "rowan-working", "tmp", fmt.Sprintf("nc-%s-%s-1", f.sprint, f.label))
			if _, err := os.Stat(slot); err == nil {
				t.Fatalf("slot %s still in place; it is moved aside at the end", slot)
			}
			asides, _ := filepath.Glob(slot + ".aside-*")
			if len(asides) != 1 {
				t.Fatalf("aside dirs %v, want one", asides)
			}
			for _, name := range []string{"native.out", "native.err"} {
				if _, err := os.Stat(filepath.Join(f.cfg.OutDir, name)); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
		})
	}
}

func launchProvider(t *testing.T, tab *route.Table, s route.SpreadRow) string {
	t.Helper()
	for _, r := range tab.Rows() {
		if r.Route == s.Routes[0] {
			p, _, _ := strings.Cut(r.Launch(), "/")
			return p
		}
	}
	t.Fatalf("spread row %s names route %s, not in the table", s.Provider, s.Routes[0])
	return ""
}

func TestCardRunRefusesBeforeTheRunner(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *runFixture)
		want  string
	}{
		{"wrong sha", func(f *runFixture) {
			if err := f.client.Set(f.ctx, card.BodyKey(f.sprint, f.sha), "tampered bytes", 0).Err(); err != nil {
				f.t.Fatal(err)
			}
		}, "REFUSED body sha "},
		{"no body", func(f *runFixture) {
			if err := f.client.Del(f.ctx, card.BodyKey(f.sprint, f.sha)).Err(); err != nil {
				f.t.Fatal(err)
			}
		}, "REFUSED no body at s:run-s:body:sha256:"},
		{"no payload sha", func(f *runFixture) {
			if err := f.client.HDel(f.ctx, card.CardKey(f.sprint, f.label), "payload_sha").Err(); err != nil {
				f.t.Fatal(err)
			}
		}, "REFUSED no payload_sha on s:run-s:card:"},
		{"route not pro or flash", func(f *runFixture) {
			if err := f.client.HSet(f.ctx, card.CardKey(f.sprint, f.label), "route", "turbo").Err(); err != nil {
				f.t.Fatal(err)
			}
		}, "REFUSED route 'turbo' on s:run-s:card:"},
		{"empty key", func(f *runFixture) {
			var env []string
			for _, kv := range f.cfg.Env {
				if k, _, _ := strings.Cut(kv, "="); k == "OPENROUTER_API_KEY" || k == "OPENCODE_API_KEY" || k == "DEEPSEEK_API_KEY" || k == "INCEPTION_API_KEY" {
					continue
				}
				env = append(env, kv)
			}
			f.cfg.Env = env
		}, "_API_KEY is not set for route "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t, "refuse-"+strings.ReplaceAll(tc.name, " ", "-"))
			tc.setup(f)
			rep := f.run()
			if rep.Code != card.RunExitRefused {
				t.Fatalf("code %d, want 2: %s", rep.Code, rep.Line())
			}
			if f.ran() {
				t.Fatal("the runner ran on a refused card")
			}
			if log := f.harnessLog(); !strings.Contains(log, tc.want) {
				t.Fatalf("harness.log lacks %q:\n%s", tc.want, log)
			}
			if !strings.HasPrefix(rep.Line(), "REFUSED card run "+f.sprint+"/"+f.label+"/1 code=2 why=") {
				t.Fatalf("line %q", rep.Line())
			}
		})
	}
}

func TestCardRunNoRouteRunsFlashAndProRunsPro(t *testing.T) {
	f := newRunFixture(t, "tiers")
	rep := f.run()
	if rep.Code != 0 || rep.Tier != "flash" || !strings.Contains(f.harnessLog(), "ROUTE none on s:run-s:card:tiers, running the flash tier") {
		t.Fatalf("no route: %s\n%s", rep.Line(), f.harnessLog())
	}
	g := newRunFixture(t, "tiers-pro")
	if err := g.client.HSet(g.ctx, card.CardKey(g.sprint, g.label), "route", "pro").Err(); err != nil {
		t.Fatal(err)
	}
	rep = g.run()
	if rep.Code != 0 || rep.Tier != "pro" || strings.Contains(g.harnessLog(), "ROUTE none") {
		t.Fatalf("pro: %s\n%s", rep.Line(), g.harnessLog())
	}
	if !strings.Contains(g.seenFile("argv"), "--model\n"+rep.Model+"\n") {
		t.Fatalf("pro runner argv lacks the pro model %s", rep.Model)
	}
}

func TestCardRunExitCodesAreTheHarnessCodes(t *testing.T) {
	// native rc=0 with no RESULT.md is FAILED (exit 1), not DONE.
	f := newRunFixture(t, "noresult")
	f.cfg.Env = append(f.cfg.Env[:0:0], f.cfg.Env...)
	for i, kv := range f.cfg.Env {
		if strings.HasPrefix(kv, fakeRunnerResult+"=") {
			f.cfg.Env[i] = fakeRunnerResult + "=0"
		}
	}
	rep := f.run()
	if rep.Code != card.RunExitNoResult || rep.RC != 0 || !strings.Contains(f.harnessLog(), "FAILED native rc=0 but no RESULT.md (NATIVE INCOMPLETE)") {
		t.Fatalf("no RESULT.md: %s\n%s", rep.Line(), f.harnessLog())
	}
	// native's own rc passes through.
	g := newRunFixture(t, "rc3")
	g.cfg.Env = append(g.cfg.Env, fakeRunnerRC+"=3")
	rep = g.run()
	if rep.Code != 3 || rep.RC != 3 || !strings.Contains(g.harnessLog(), "END native rc=3 wall_s=") {
		t.Fatalf("rc 3: %s\n%s", rep.Line(), g.harnessLog())
	}
}

func TestCardRunRefusesAnIncompleteConfig(t *testing.T) {
	rep := card.Run(context.Background(), nil, card.RunConfig{Sprint: "s", Label: "l", Attempt: 1})
	if rep.Code != card.RunExitRefused || !strings.HasPrefix(rep.Why, "missing ") {
		t.Fatalf("%s", rep.Line())
	}
	for _, want := range []string{"bench", "out dir", "job dir", "home", "harness bin", "seat", "deadline", "tokens"} {
		if !strings.Contains(rep.Why, want) {
			t.Fatalf("why %q lacks %s", rep.Why, want)
		}
	}
	cfg, missing := card.RunConfigFromEnv(func(string) string { return "" })
	if cfg.Home != "" || strings.Join(missing, " ") != "HOME NOVA_BENCH_SEAT NOVA_CARD_BENCH NOVA_CARD_DEADLINE NOVA_CARD_HARNESS_BIN NOVA_CARD_TOKENS" {
		t.Fatalf("missing = %v", missing)
	}
}

func TestProviderKey(t *testing.T) {
	for launch, want := range map[string]string{
		"openrouter/deepseek/deepseek-v4-flash": "OPENROUTER_API_KEY",
		"opencode/glm-5.3-flash":                "OPENCODE_API_KEY",
		"deepseek/deepseek-v4-flash":            "DEEPSEEK_API_KEY",
		"inception/mercury-2.5":                 "INCEPTION_API_KEY",
	} {
		if got, err := card.ProviderKey(launch); err != nil || got != want {
			t.Fatalf("%s: %s %v, want %s", launch, got, err, want)
		}
	}
	if _, err := card.ProviderKey("datacenter/inception/mercury-2.5"); err == nil || !strings.Contains(err.Error(), "provider datacenter") {
		t.Fatalf("undeclared provider: %v", err)
	}
}

// TestWrapperRunsTheHarnessInProcess: the wrapper with no harness program
// runs card.Run in-process against the real ns_card_* functions: one
// launched, the start beat, an end DONE with exit 0, and the results dir
// holds the run's harness.log (START and END) and RESULT.md.
func TestWrapperRunsTheHarnessInProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, client := newSprint(t)
	id := card.Identity{Sprint: "control-wrap", Label: "card-inproc", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
	token := attemptToken(1, fmt.Sprintf("%032x", 77))
	seedCard(t, ctx, client, id, "dealt", token)
	body := []byte("RESULT: card-inproc sha=0\nBASE: main\n\nbody")
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), "payload_sha", sha).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, card.BodyKey(id.Sprint, sha), body, 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_CARD_TOKEN", token)

	h := newHarnessRun(t, id, "")
	seen := filepath.Join(t.TempDir(), "seen")
	h.cfg.Store = st
	h.cfg.InProcess = &card.RunConfig{
		Home: t.TempDir(), Runner: self, HarnessBin: "/opt/harness/opencode", Seat: "testseat", Deadline: "60", Tokens: "1000",
		Env: []string{fakeRunnerEnv + "=1", fakeRunnerSeen + "=" + seen, fakeRunnerResult + "=1",
			"OPENROUTER_API_KEY=or-val", "OPENCODE_API_KEY=oc-val", "DEEPSEEK_API_KEY=ds-val", "INCEPTION_API_KEY=in-val",
			"NOVA_CARD_TOKEN=" + token, "PATH=" + os.Getenv("PATH")},
	}
	// RedisLedger itself (a ResultRecorder): the record is written at end.
	got := make(chan card.WrapperReport, 1)
	go func() {
		got <- card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})
	}()
	rep := h.report(got)
	if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" || rep.Reason != "done" || rep.Exit != 0 {
		t.Fatalf("report %s why=%q", rep.Line(), rep.Why)
	}
	hash := hashOf(t, ctx, client, id.Sprint, id.Label)
	if hash["state"] != "ended" || hash["outcome"] != "DONE" || hash["exit"] != "0" {
		t.Fatalf("card hash %v", hash)
	}
	// #3693: the harness feeds the wrapper's record through its START line
	// in out/harness.log; the provider facts are in Redis, names only.
	rec := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)+":result:a1").Val()
	if rec["w_tier"] != "flash" || rec["w_route"] == "" || rec["w_model"] == "" || !strings.HasSuffix(rec["w_key"], "_API_KEY") || rec["w_card_sha"] != sha[:12] {
		t.Fatalf("result record provider facts %v", rec)
	}
	for _, v := range rec {
		if strings.HasSuffix(v, "-val") {
			t.Fatalf("a key value reached the result record: %v", rec)
		}
	}
	log, err := os.ReadFile(filepath.Join(h.results, "harness.log"))
	if err != nil || !strings.Contains(string(log), "START control-wrap/card-inproc/1 bench=wrap-bench tier=flash ") || !strings.Contains(string(log), "END native rc=0") {
		t.Fatalf("results harness.log %v:\n%s", err, log)
	}
	if _, err := os.Stat(filepath.Join(h.results, "RESULT.md")); err != nil {
		t.Fatal(err)
	}
	keys, _ := os.ReadFile(filepath.Join(seen, "keys"))
	if n := strings.Count(string(keys), "_API_KEY"); n != 1 {
		t.Fatalf("runner saw %d keys %q, want one", n, keys)
	}
	if env, _ := os.ReadFile(filepath.Join(seen, "env")); !strings.Contains(string(env), "card=control-wrap/card-inproc/1 ") {
		t.Fatalf("runner env %q", env)
	}
	if strings.Contains(string(log), token) {
		t.Fatal("the token reached the harness log")
	}
}

// TestWrapperConfigWantsOneHarness: a program or in-process, never both,
// and in-process needs the store.
func TestWrapperConfigWantsOneHarness(t *testing.T) {
	id := card.Identity{Sprint: "s", Label: "l", BaseSHA: "0123abcd", Bench: "b", Attempt: 1}
	both := newHarnessRun(t, id, "/bin/true").cfg
	both.InProcess = &card.RunConfig{}
	if rep := card.RunWrapper(context.Background(), both, nil); rep.Code != card.WrapperExitUsage || !strings.Contains(rep.Why, "not both") {
		t.Fatalf("%s", rep.Line())
	}
	noStore := newHarnessRun(t, id, "").cfg
	noStore.InProcess = &card.RunConfig{}
	if rep := card.RunWrapper(context.Background(), noStore, nil); rep.Code != card.WrapperExitUsage || !strings.Contains(rep.Why, "store") {
		t.Fatalf("%s", rep.Line())
	}
	neither := newHarnessRun(t, id, "").cfg
	if rep := card.RunWrapper(context.Background(), neither, nil); rep.Code != card.WrapperExitUsage || !strings.Contains(rep.Why, "harness") {
		t.Fatalf("%s", rep.Line())
	}
}
