// The lander verb runs one pass of the lane's gate-retry rule (spec 4.9,
// internal/nsprint/land) over a batch of PRs the sprint's records name. It is
// the production entry point that builds land.Lane (nx-g61, #2720): with
// --metrics-addr it serves /metrics for the pass (the batch depth, the members
// admitted to the gate, one latency per mergeable read and gate run).
//
//	nova-sprint lander --redis <addr> --sprint <S> --repo <repo> --batch <name>
//	    --gate <prog> --bisect <prog> --land <prog> --file <prog>
//	    [--metrics-addr <host:port>] <n> [<n> ...]
//
// Each member's head and mergeable word are read from its record
// s:<S>:pr:<repo>:<n> (head, mergeable; an empty word is UNKNOWN), never from
// GitHub. The flaky hash is flaky:<repo>:<pkg>.<test> in the same Redis. The
// four programs are the host-touching seams, each handed its arguments on the
// command line (members as <n>@<head>):
//
//	gate   <repo> <batch> <attempt> <members...>   exit 0 green; exit 1 red, its
//	                                               last line carries step= package= test=
//	bisect <repo> <batch> <package> <test> <members...>
//	                                               prints base=green|red members=green,red,...
//	land   <repo> <batch> <members...>             exit 0 landed
//	file   <repo> <title>                          the body on stdin; prints the issue number
//
// Exit 0 the pass ran (LANDER line), 1 the pass errored, 2 could not run, 6 no Redis.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "lander",
		Summary: "lander --redis <addr> --sprint <S> --repo <r> --batch <name> --gate/--bisect/--land/--file <prog> [--metrics-addr <host:port>] <n>...: one gate-retry lane pass (exit 1 pass error)",
		Run:     runLander,
	})
}

// landerPrograms are the four programs the flags name.
type landerPrograms struct {
	Gate, Bisect, Land, File string
}

// landerSeams builds the lane's host-touching collaborators from the
// programs. A test swaps in in-process fakes (CI-NET: no host in a test);
// the Redis reads, the flaky hash and the metrics are the production ones.
var landerSeams = func(p landerPrograms) (land.Gate, land.Bisect, land.Lander, land.Filer) {
	return execGate{p.Gate}, execBisect{p.Bisect}, execLand{p.Land}, execFile{p.File}
}

// landerClock is the lane's clock: the wall clock, whose Sleep waits the
// PollGap between UNKNOWN re-polls. A test swaps in one that does not block.
var landerClock land.Clock = land.WallClock{}

func runLander(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("lander")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "", "")
	batchName := fs.String("batch", "", "")
	var progs landerPrograms
	fs.StringVar(&progs.Gate, "gate", "", "")
	fs.StringVar(&progs.Bisect, "bisect", "", "")
	fs.StringVar(&progs.Land, "land", "", "")
	fs.StringVar(&progs.File, "file", "", "")
	metricsAddr := fs.String("metrics-addr", "", "")
	var nums []string
	for len(args) > 0 {
		if !strings.HasPrefix(args[0], "-") {
			nums = append(nums, args[0])
			args = args[1:]
			continue
		}
		if err := fs.Parse(args); err != nil {
			return refuse(errOut, "lander", err.Error())
		}
		args = fs.Args()
	}
	if *redisAddr == "" || *sprint == "" || *repo == "" || *batchName == "" {
		return refuse(errOut, "lander", "needs --redis <addr> --sprint <S> --repo <repo> --batch <name>")
	}
	if progs.Gate == "" || progs.Bisect == "" || progs.Land == "" || progs.File == "" {
		return refuse(errOut, "lander", "needs --gate, --bisect, --land and --file programs")
	}
	if len(nums) == 0 {
		return refuse(errOut, "lander", "names no member: give the PR numbers of the batch")
	}
	numbers := make([]int, 0, len(nums))
	for _, s := range nums {
		n, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
		if err != nil || n <= 0 {
			return refuse(errOut, "lander", "member "+strconv.Quote(s)+" is not a PR number")
		}
		numbers = append(numbers, n)
	}

	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint lander: %v\n", err)
		return 6
	}
	defer st.Close()
	c := st.Client()
	records, err := loadMembers(ctx, c, *sprint, *repo, numbers)
	if err != nil {
		return refuse(errOut, "lander", err.Error())
	}

	if strings.TrimSpace(*metricsAddr) != "" {
		srv, err := metrics.Default.Listen(*metricsAddr)
		if err != nil {
			return refuse(errOut, "lander", "--metrics-addr "+strconv.Quote(*metricsAddr)+": "+err.Error()+" (name a free host:port, or leave it out)")
		}
		defer srv.Close()
		fmt.Fprintf(out, "METRICS lander url=%s\n", srv.URL())
	}

	gate, bisect, lander, filer := landerSeams(progs)
	lane := land.Lane{
		Gate: gate, Bisect: bisect, Land: lander, Filer: filer,
		Forge:   recordForge{c: c, sprint: *sprint},
		Store:   redisFlaky{c: c},
		Clock:   landerClock,
		Metrics: metrics.Default,
	}
	batch := land.Batch{Repo: *repo, Name: *batchName}
	for _, r := range records {
		batch.Members = append(batch.Members, land.Member{Number: r.n, Head: r.head})
	}
	res, err := lane.Run(ctx, batch)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint lander: pass: %v\n", err)
		return 1
	}
	dropped := make([]string, 0, len(res.Dropped))
	for _, d := range res.Dropped {
		dropped = append(dropped, fmt.Sprintf("#%d:%s", d.Number, d.Reason))
	}
	fmt.Fprintf(out, "LANDER batch=%s repo=%s landed=%t runs=%d retries=%d kept=%d dropped=%s class=%s flaky=%s filed=%t\n",
		*batchName, *repo, res.Landed, res.Runs, res.Retries, len(res.Kept),
		orDash(strings.Join(dropped, ",")), orDash(res.Class), orDash(res.FlakyKey), res.Filed)
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

type memberRecord struct {
	n    int
	head string
}

// loadMembers reads every member's head from its record in one pipelined
// round trip. A member with no record or no head is a refusal: the lane gates
// only what the sprint knows.
func loadMembers(ctx context.Context, c *redis.Client, sprint, repo string, numbers []int) ([]memberRecord, error) {
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(numbers))
	for i, n := range numbers {
		cmds[i] = pipe.HGet(ctx, land.ID{Repo: repo, N: n}.Key(sprint), "head")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]memberRecord, len(numbers))
	for i, n := range numbers {
		head, err := cmds[i].Result()
		if errors.Is(err, redis.Nil) || (err == nil && strings.TrimSpace(head) == "") {
			return nil, fmt.Errorf("%s#%d has no head in %s", repo, n, land.ID{Repo: repo, N: n}.Key(sprint))
		}
		if err != nil {
			return nil, err
		}
		out[i] = memberRecord{n: n, head: strings.TrimSpace(head)}
	}
	return out, nil
}

// recordForge answers a member's mergeable word from its record, the field
// ns_pr_eval writes (the record contract in internal/nsprint/land). An absent
// or empty word is UNKNOWN, which the lane re-polls and then drops.
type recordForge struct {
	c      *redis.Client
	sprint string
}

func (f recordForge) Mergeable(ctx context.Context, repo string, number int) (string, error) {
	v, err := f.c.HGet(ctx, land.ID{Repo: repo, N: number}.Key(f.sprint), "mergeable").Result()
	if errors.Is(err, redis.Nil) {
		return "UNKNOWN", nil
	}
	if err != nil {
		return "", err
	}
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return "UNKNOWN", nil
	}
	return v, nil
}

// redisFlaky is the flaky hash flaky:<repo>:<pkg>.<test> (first_seen,
// lanes_hit, issue, last_at) with Memory's contract: the first hit claims the
// key with HSETNX first_seen, files, and publishes the issue; a failed file
// deletes the claim so a later hit can file; a later hit increments lanes_hit
// and files nothing.
type redisFlaky struct{ c *redis.Client }

func (s redisFlaky) Observe(ctx context.Context, key, at string, file func() (int, error)) (land.FlakyRecord, bool, error) {
	if key == "" || at == "" || file == nil {
		return land.FlakyRecord{}, false, fmt.Errorf("lander: flaky key, time and filer are required")
	}
	claimed, err := s.c.HSetNX(ctx, key, "first_seen", at).Result()
	if err != nil {
		return land.FlakyRecord{}, false, err
	}
	if claimed {
		n, err := file()
		if err == nil && n <= 0 {
			err = fmt.Errorf("filer returned issue %d", n)
		}
		if err != nil {
			_ = s.c.Del(context.WithoutCancel(ctx), key).Err()
			return land.FlakyRecord{}, false, err
		}
		if err := s.c.HSet(ctx, key, "lanes_hit", 1, "issue", n, "last_at", at).Err(); err != nil {
			return land.FlakyRecord{}, false, err
		}
		return land.FlakyRecord{FirstSeen: at, LanesHit: 1, Issue: n, LastAt: at}, true, nil
	}
	pipe := s.c.TxPipeline()
	pipe.HIncrBy(ctx, key, "lanes_hit", 1)
	pipe.HSet(ctx, key, "last_at", at)
	all := pipe.HGetAll(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return land.FlakyRecord{}, false, err
	}
	m := all.Val()
	hit, _ := strconv.Atoi(m["lanes_hit"])
	issue, _ := strconv.Atoi(m["issue"])
	return land.FlakyRecord{FirstSeen: m["first_seen"], LanesHit: hit, Issue: issue, LastAt: m["last_at"]}, false, nil
}

// memberArgs renders members as <n>@<head>.
func memberArgs(b land.Batch) []string {
	out := make([]string, len(b.Members))
	for i, m := range b.Members {
		out[i] = strconv.Itoa(m.Number) + "@" + m.Head
	}
	return out
}

// runProgram runs prog with args (stdin optional) and returns its exit code
// and stdout. A program that cannot start is an error, not an exit code.
func runProgram(ctx context.Context, prog string, stdin []byte, args ...string) (int, string, error) {
	cmd := exec.CommandContext(ctx, prog, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), stdout.String(), nil
	}
	if err != nil {
		return -1, "", fmt.Errorf("%s: %w", prog, err)
	}
	return 0, stdout.String(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// fields parses key=value words.
func fields(line string) map[string]string {
	m := map[string]string{}
	for _, w := range strings.Fields(line) {
		if k, v, ok := strings.Cut(w, "="); ok {
			m[k] = v
		}
	}
	return m
}

type execGate struct{ prog string }

func (g execGate) Run(ctx context.Context, b land.Batch, attempt int) (land.Verdict, error) {
	code, out, err := runProgram(ctx, g.prog, nil, append([]string{b.Repo, b.Name, strconv.Itoa(attempt)}, memberArgs(b)...)...)
	if err != nil {
		return land.Verdict{}, err
	}
	switch code {
	case 0:
		return land.Verdict{OK: true}, nil
	case 1:
		f := fields(lastLine(out))
		return land.Verdict{Step: f["step"], Package: f["package"], Test: f["test"]}, nil
	}
	return land.Verdict{}, fmt.Errorf("gate %s exited %d", g.prog, code)
}

type execBisect struct{ prog string }

func (x execBisect) Alone(ctx context.Context, b land.Batch, v land.Verdict) (bool, []bool, error) {
	code, out, err := runProgram(ctx, x.prog, nil, append([]string{b.Repo, b.Name, v.Package, v.Test}, memberArgs(b)...)...)
	if err != nil {
		return false, nil, err
	}
	if code != 0 {
		return false, nil, fmt.Errorf("bisect %s exited %d", x.prog, code)
	}
	f := fields(lastLine(out))
	base, ok := f["base"]
	if !ok || (base != "green" && base != "red") {
		return false, nil, fmt.Errorf("bisect %s: want base=green|red members=..., got %q", x.prog, lastLine(out))
	}
	var members []bool
	for _, w := range strings.Split(f["members"], ",") {
		switch w {
		case "green":
			members = append(members, true)
		case "red":
			members = append(members, false)
		default:
			return false, nil, fmt.Errorf("bisect %s: member word %q is not green or red", x.prog, w)
		}
	}
	return base == "green", members, nil
}

type execLand struct{ prog string }

func (l execLand) Land(ctx context.Context, b land.Batch, _ land.Verdict) error {
	code, _, err := runProgram(ctx, l.prog, nil, append([]string{b.Repo, b.Name}, memberArgs(b)...)...)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("land %s exited %d", l.prog, code)
	}
	return nil
}

type execFile struct{ prog string }

func (f execFile) File(ctx context.Context, repo, title, body string) (int, error) {
	code, out, err := runProgram(ctx, f.prog, []byte(body), repo, title)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("file %s exited %d", f.prog, code)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(lastLine(out), "#"))
	if err != nil {
		return 0, fmt.Errorf("file %s: want an issue number, got %q", f.prog, lastLine(out))
	}
	return n, nil
}
