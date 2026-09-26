package card

// run.go is the bench-side card harness (#3681): what rowan-tools' bash
// nova-card-harness did on every bench, as Go with Redis state. It is one
// function, Run, reached two ways: `nova-sprint card run --sprint <S>
// --label <L> --attempt <a>` by hand, and in-process from the card wrapper
// (WrapperConfig.InProcess) when the bench declares no NOVA_CARD_HARNESS
// program. Glenn 2026-09-24: "don't fix it in the garbage scripts. convert
// to golang + redis, then fix."
//
// What one run does, in order, and nowhere else:
//
//  1. reads the card hash s:<S>:card:<L> as the bench seat in ONE pipeline
//     (payload_sha, route, repo, base, base_sha, est) and refuses (exit 2)
//     a card with no 64-hex payload_sha;
//  2. fetches the body at s:<S>:body:sha256:<payload_sha> (the pusher stores
//     the exact bytes before it publishes the card) and refuses unless
//     sha256(body) == payload_sha; the body becomes <job>/card.md;
//  3. picks the route in-process from the swarm spread (route.Table.Pick)
//     for the card's ROUTE tier and label: no route runs the flash tier,
//     any tier but pro or flash is refused, a tier the spread cannot answer
//     is refused (never another tier's model);
//  4. hands the runner exactly the picked provider's key, by name, from the
//     environment the launcher carries (the four seat keys arrive through
//     nova-secrets exec); the other three and the bench Redis password are
//     dropped; an empty key is refused. Names only: no value is ever logged;
//  5. writes the START line to <out>/harness.log, runs nova-swarm native
//     with the declared harness and budgets in a slot under
//     <home>/rowan-working/tmp, stdout and stderr to <out>/native.out and
//     native.err, then the END line with rc and wall;
//  6. copies the card's RESULT.md up to <out>/RESULT.md, moves the slot
//     aside (never deletes it), and exits with native's rc, or 1 when rc=0
//     left no RESULT.md (NATIVE INCOMPLETE is not DONE).
//
// Exit codes are the bash harness's: native's rc; 1 rc=0 with no RESULT.md;
// 2 every refusal (nothing runs).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Run exit codes, the bash harness's.
const (
	RunExitNoResult = 1 // native rc=0 but no RESULT.md: FAILED, not DONE
	RunExitRefused  = 2 // refused before the runner started
)

// ProviderKeys is the one key each launch-string provider gets. The launch
// string is <via>/<model>, or the model itself when it already names its
// provider (inception/mercury-2.5); the provider is the part before the
// first slash.
var ProviderKeys = map[string]string{
	"openrouter": "OPENROUTER_API_KEY",
	"opencode":   "OPENCODE_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"inception":  "INCEPTION_API_KEY",
}

// RunnerEnv names the nova-swarm binary Run executes; unset, it is
// <home>/.local/bin/nova-swarm, where the bench standard installs it.
const RunnerEnv = "NOVA_CARD_RUNNER"

// BodyKey is where the pusher stores the card's exact bytes, content-addressed.
func BodyKey(sprint, sha string) string { return "s:" + sprint + ":body:sha256:" + sha }

// BodyTTL is how long a card body lives at BodyKey: the retired bash
// nova-card-push stored it with a 7-day PX, and card run reads it within that
// window. A body that never expires leaks every pushed card's bytes forever.
const BodyTTL = 7 * 24 * time.Hour

// ProviderKey is the key name for one launch string, or an error naming the
// provider that has none.
func ProviderKey(launch string) (string, error) {
	provider, _, _ := strings.Cut(launch, "/")
	key, ok := ProviderKeys[provider]
	if !ok {
		return "", fmt.Errorf("provider %s of %s has no declared key", provider, launch)
	}
	return key, nil
}

// RunConfig is one harness run. OutDir and JobDir are the wrapper's
// NOVA_CARD_OUT and NOVA_CARD_JOB; the rest is the bench's card.env.
type RunConfig struct {
	Sprint  string
	Label   string
	Attempt int
	Bench   string // NOVA_CARD_BENCH, the START line's bench=

	OutDir string // NOVA_CARD_OUT: harness.log, native.out, native.err, native/, RESULT.md
	JobDir string // NOVA_CARD_JOB: card.md is written here
	Home   string // the bench user's home: slots under <Home>/rowan-working/tmp

	Runner string // the nova-swarm binary; "" is <Home>/.local/bin/nova-swarm
	// Yield steps this process down to yield.Nice before the runner starts
	// (nova-tools#4293); nil is the real setpriority (yieldToCI). A test
	// gives its own; production never sets it.
	Yield      func() error
	HarnessBin string // NOVA_CARD_HARNESS_BIN, native --harness
	Deadline   string // NOVA_CARD_DEADLINE, native --deadline, passed as declared
	Tokens     string // NOVA_CARD_TOKENS, native --tokens, passed as declared

	// Env is the environment the runner is built from: the launcher's, with
	// the four seat keys in it. nil is os.Environ(). Run drops every key but
	// the picked provider's, and the bench Redis password, before exec.
	Env []string
	// Routes is the spread table; nil loads the embedded one.
	Routes *route.Table
	// Proc is called with the runner once it has started, so an in-process
	// caller (the wrapper) can stop its group. nil is fine.
	Proc func(*exec.Cmd)
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// CopyID is a consumer copy (#3998): steps 1 and 2 read its record
	// task:<copy> in one HGETALL and the card body is RenderCopy of it (its
	// payload_sha is the body's sha256), the route its tier.
	CopyID string
}

// RunReport is what one run did; the command prints Line.
type RunReport struct {
	Code  int
	Card  string
	Tier  string
	Route string
	Model string
	Key   string // the key NAME handed to the runner
	SHA   string // payload_sha
	RC    int    // native's exit code; -1 when it was killed
	Wall  time.Duration
	Why   string
}

// Line is the run's one output line. It never carries a key value.
func (r RunReport) Line() string {
	if r.Code == RunExitRefused {
		return fmt.Sprintf("REFUSED card run %s code=%d why=%s", r.Card, r.Code, strconv.Quote(r.Why))
	}
	line := fmt.Sprintf("CARD RUN %s code=%d tier=%s route=%s model=%s key=%s sha=%s rc=%d wall_s=%d",
		r.Card, r.Code, r.Tier, r.Route, r.Model, r.Key, short(r.SHA), r.RC, int64(r.Wall/time.Second))
	if r.Why != "" {
		line += " why=" + strconv.Quote(r.Why)
	}
	return line
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// RunConfigFromEnv reads the bench's card.env part of a RunConfig: the
// fields every run needs whichever way it is reached. It returns the names
// that are missing, sorted, so the caller can refuse with all of them at
// once. OutDir, JobDir, Sprint, Label and Attempt are the caller's.
func RunConfigFromEnv(getenv func(string) string) (RunConfig, []string) {
	cfg := RunConfig{
		Bench:      getenv("NOVA_CARD_BENCH"),
		Home:       getenv("HOME"),
		Runner:     getenv(RunnerEnv),
		HarnessBin: getenv("NOVA_CARD_HARNESS_BIN"),
		Deadline:   getenv("NOVA_CARD_DEADLINE"),
		Tokens:     getenv("NOVA_CARD_TOKENS"),
	}
	var missing []string
	for _, kv := range []struct{ name, val string }{
		{"HOME", cfg.Home},
		{"NOVA_CARD_BENCH", cfg.Bench},
		{"NOVA_CARD_DEADLINE", cfg.Deadline},
		{"NOVA_CARD_HARNESS_BIN", cfg.HarnessBin},
		{"NOVA_CARD_TOKENS", cfg.Tokens},
	} {
		if kv.val == "" {
			missing = append(missing, kv.name)
		}
	}
	return cfg, missing
}

func (c RunConfig) card() string { return fmt.Sprintf("%s/%s/%d", c.Sprint, c.Label, c.Attempt) }

func (c RunConfig) check() error {
	var missing []string
	if !validSprintLabel(c.Sprint, c.Label) || c.Attempt < 1 {
		missing = append(missing, "card <S>/<label>/<attempt>")
	}
	if c.Bench == "" {
		missing = append(missing, "bench")
	}
	for _, p := range []struct{ name, val string }{{"out dir", c.OutDir}, {"job dir", c.JobDir}, {"home", c.Home}} {
		if !filepath.IsAbs(p.val) {
			missing = append(missing, p.name+" (absolute path)")
		}
	}
	for _, p := range []struct{ name, val string }{{"harness bin", c.HarnessBin}, {"deadline", c.Deadline}, {"tokens", c.Tokens}} {
		if p.val == "" {
			missing = append(missing, p.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Run is the harness. See the file comment for the order of its effects.
func Run(ctx context.Context, st *store.Store, cfg RunConfig) RunReport {
	rep := RunReport{Card: cfg.card(), Code: RunExitRefused, RC: -1}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if err := cfg.check(); err != nil {
		rep.Why = err.Error()
		return rep
	}
	// CI over work (nova-tools#4293): `card run` by hand reaches here without
	// the wrapper, so the harness yields for itself before the runner starts.
	// Under the wrapper this is the second call and a no-op.
	yield := cfg.Yield
	if yield == nil {
		yield = yieldToCI
	}
	if err := yield(); err != nil {
		rep.Why = "yield to CI: " + err.Error()
		return rep
	}
	if st == nil || st.Client() == nil {
		rep.Why = "no store"
		return rep
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		rep.Why = "out dir: " + err.Error()
		return rep
	}
	logf, err := os.OpenFile(filepath.Join(cfg.OutDir, "harness.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		rep.Why = "harness log: " + err.Error()
		return rep
	}
	defer logf.Close()
	log := func(format string, args ...any) {
		fmt.Fprintf(logf, "%s %s\n", now().UTC().Format("2006-01-02T15:04:05Z"), fmt.Sprintf(format, args...))
	}
	refuse := func(why string) RunReport {
		log("REFUSED %s for %s", why, rep.Card)
		rep.Why = why
		return rep
	}
	card := rep.Card
	cardKey := CardKey(cfg.Sprint, cfg.Label)

	var body []byte
	var field func(int) string
	if cfg.CopyID != "" {
		// 1 and 2 for a copy: its record, its rendered card.
		cardKey = "task:" + cfg.CopyID
		rec, err := st.Client().HGetAll(ctx, cardKey).Result()
		if err != nil {
			return refuse("copy read failed: " + err.Error())
		}
		if len(rec) == 0 {
			return refuse("no copy " + cardKey)
		}
		if body, err = RenderCopy(CopyCardFrom(cfg.CopyID, rec)); err != nil {
			return refuse(err.Error())
		}
		tier := rec["tier"]
		if !cardhdr.IsRoute(tier) {
			tier = rec["route"]
		}
		if rec["leg"] == "read" {
			tier = RoutePro
		}
		if !cardhdr.IsRoute(tier) {
			tier = ""
		}
		vals := []string{"", tier, rec["repo"], rec["base"], rec["base_sha"], rec["est"]}
		field = func(i int) string { return strings.TrimSpace(vals[i]) }
		sum := sha256.Sum256(body)
		rep.SHA = hex.EncodeToString(sum[:])
	} else {
		// 1. The card hash, one pipeline. It is the first command on the store
		// (Open sends none, #3277), so an unreachable Redis is refused here.
		rows, err := st.PipelineHMGet(ctx, []store.HashRead{{Key: cardKey, Fields: []string{"payload_sha", "route", "repo", "base", "base_sha", "est"}}})
		if err != nil {
			return refuse("redis: card read failed: " + err.Error())
		}
		field = func(i int) string { s, _ := rows[0][i].(string); return strings.TrimSpace(s) }
		want := field(0)
		if !hex64.MatchString(want) {
			return refuse("no payload_sha on " + cardKey)
		}
		rep.SHA = want

		// 2. The body, refused unless its sha256 is the payload_sha.
		body, err = st.Client().Get(ctx, BodyKey(cfg.Sprint, want)).Bytes()
		if errors.Is(err, redis.Nil) || (err == nil && len(body) == 0) {
			return refuse("no body at " + BodyKey(cfg.Sprint, want) + " (push with nova-card-push)")
		}
		if err != nil {
			return refuse("body read failed: " + err.Error())
		}
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != want {
			return refuse(fmt.Sprintf("body sha %s != payload_sha '%s'", got, want))
		}
	}
	if err := os.MkdirAll(cfg.JobDir, 0o700); err != nil {
		return refuse("job dir: " + err.Error())
	}
	cardPath := filepath.Join(cfg.JobDir, "card.md")
	if err := os.WriteFile(cardPath, body, 0o644); err != nil {
		return refuse("card.md: " + err.Error())
	}

	// 3. The route: the card's ROUTE tier and label pick from the spread.
	tier := field(1)
	switch {
	case cardhdr.IsRoute(tier):
	case tier == "":
		log("ROUTE none on %s, running the flash tier", cardKey)
		tier = RouteFlash
	default:
		return refuse(fmt.Sprintf("route '%s' on %s is not %s", tier, cardKey, cardhdr.RouteList))
	}
	tab := cfg.Routes
	if tab == nil {
		if tab, err = route.Load(); err != nil {
			return refuse("routes table: " + err.Error())
		}
	}
	row, _, err := tab.Pick(tier, cfg.Label)
	if err != nil {
		return refuse(fmt.Sprintf("no spread route for tier %s label %s (nova-sprint routes --tier %s --label %s): %s", tier, cfg.Label, tier, cfg.Label, err))
	}
	model := row.Launch()
	rep.Tier, rep.Route, rep.Model = tier, row.Route, model

	// 4. The one key, by name.
	key, err := ProviderKey(model)
	if err != nil {
		return refuse(err.Error() + " (route " + row.Route + ")")
	}
	base := cfg.Env
	if base == nil {
		base = os.Environ()
	}
	env, have := runnerEnv(base, key, cfg, card)
	if !have {
		return refuse(key + " is not set for route " + row.Route + " (" + model + "); the seat or nova-sprint-launch --only lacks it")
	}
	rep.Key = key

	// 5. START, the runner, END.
	log("START %s bench=%s tier=%s route=%s model=%s key=%s sha=%s", card, cfg.Bench, tier, row.Route, model, key, short(rep.SHA))
	root := filepath.Join(cfg.Home, "rowan-working", "tmp")
	slot := filepath.Join(root, fmt.Sprintf("nc-%s-%s-%d", cfg.Sprint, cfg.Label, cfg.Attempt))
	if err := os.MkdirAll(slot, 0o755); err != nil {
		return refuse("slot: " + err.Error())
	}
	runner := cfg.Runner
	if runner == "" {
		runner = filepath.Join(cfg.Home, ".local", "bin", "nova-swarm")
	}
	nativeOut, err := os.Create(filepath.Join(cfg.OutDir, "native.out"))
	if err != nil {
		return refuse("native.out: " + err.Error())
	}
	defer nativeOut.Close()
	nativeErr, err := os.Create(filepath.Join(cfg.OutDir, "native.err"))
	if err != nil {
		return refuse("native.err: " + err.Error())
	}
	defer nativeErr.Close()
	cmd := exec.Command(runner, "native",
		"--harness", cfg.HarnessBin, "--model", model,
		"--card", cardPath, "--label", cfg.Label, "--slot", slot, "--root", root,
		"--deadline", cfg.Deadline, "--tokens", cfg.Tokens,
		// No --slots-store, no --owner (nova-tools#3877): the dealer admitted this card
		// against bench:<b>:desired in Redis, and that is the one slot ledger.
		"--results-root", filepath.Join(cfg.OutDir, "native"))
	cmd.Dir = cfg.JobDir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = nativeOut, nativeErr
	harnessGroup(cmd)
	t0 := now()
	if err := cmd.Start(); err != nil {
		return refuse("runner " + runner + ": " + err.Error())
	}
	if cfg.Proc != nil {
		cfg.Proc(cmd)
	}
	rc := exitCode(cmd.Wait())
	rep.RC, rep.Wall = rc, now().Sub(t0)
	log("END native rc=%d wall_s=%d", rc, int64(rep.Wall/time.Second))

	// 6. RESULT.md up, the slot aside, the exit code.
	if r := findResult(filepath.Join(cfg.OutDir, "native"), slot); r != "" {
		if err := copyFile(r, filepath.Join(cfg.OutDir, "RESULT.md")); err != nil {
			log("RESULT COPY FAILED %s: %s", r, err)
		}
	}
	aside := slot + ".aside-" + now().UTC().Format("20060102T150405Z")
	if err := os.Rename(slot, aside); err != nil {
		log("ASIDE FAILED %s: %s", slot, err)
	}
	switch {
	case rc == 0 && !hasContent(filepath.Join(cfg.OutDir, "RESULT.md")):
		log("FAILED native rc=0 but no RESULT.md (%s)", nativeLine(filepath.Join(cfg.OutDir, "native.out")))
		rep.Code, rep.Why = RunExitNoResult, "native rc=0 but no RESULT.md"
	case rc < 0:
		rep.Code, rep.Why = RunExitNoResult, "native was killed"
	default:
		rep.Code = rc
	}
	return rep
}

// runnerEnv is base with the picked key kept and the other three dropped,
// the bench Redis password and redis-cli auth dropped, the bench's push
// credential (harvestcopy.TokenEnv, #4227: the wrapper's, never the
// sandbox's) dropped, and this card's NOVA_CARD, NOVA_CARD_JOB and
// NOVA_CARD_OUT set. have reports whether the picked key had a non-empty
// value.
func runnerEnv(base []string, key string, cfg RunConfig, card string) (env []string, have bool) {
	drop := map[string]bool{"NOVA_REDIS_BENCH_PASSWORD": true, "REDISCLI_AUTH": true,
		harvestcopy.TokenEnv: true, harvestcopy.AskpassEnv: true,
		"NOVA_CARD": true, "NOVA_CARD_JOB": true, "NOVA_CARD_OUT": true}
	for _, k := range ProviderKeys {
		if k != key {
			drop[k] = true
		}
	}
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		if k == store.PasswordEnvEnv && v != "" {
			drop[v] = true
		}
	}
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		if k == key {
			if v == "" {
				continue
			}
			have = true
		}
		env = append(env, kv)
	}
	return append(env, "NOVA_CARD="+card, "NOVA_CARD_JOB="+cfg.JobDir, "NOVA_CARD_OUT="+cfg.OutDir), have
}

// exitCode is the runner's exit code; -1 when it was killed or never ran.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// findResult is the first RESULT.md under the native results root or the
// slot, the way the bash harness found it.
func findResult(dirs ...string) string {
	for _, dir := range dirs {
		found := ""
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return nil
			}
			if !d.IsDir() && d.Name() == "RESULT.md" {
				found = p
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

func hasContent(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

// nativeLine is native's `NATIVE <STATE>` token from its stdout, for the log.
func nativeLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	if m := regexp.MustCompile(`NATIVE [A-Z]+`).Find(data); m != nil {
		return string(m)
	}
	return "-"
}
