package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
	"github.com/redis/go-redis/v9"
)

// lineup is #3108 (#2756 v6 section 7, controls 49, 64, 65): run the conform
// publish across the UP benches in one batch, run the preflight checks, and
// only when they are all GREEN cut the five probes; then print every DRIFT
// line, the 7.18-7.21, 7.24 and 7.25 checks and the x/y tally. A failed
// publish or a record not republished in this run is RED. Any RED refuses
// sprint open (exit 1) and leaves the probe set untouched.
//
//	nova-sprint lineup --redis <addr> --sprint <S> [--probes a,b,c,d,e]
//	    [--conform-cmd "bench-conform --publish" | --no-conform]
//	    [--gql-remaining n] --lane-gql-per-pass n --lane-cadence 10s
//	nova-sprint lineup publish --redis <addr> --bench <b> --all-yml <file> [--run <marker>] < probe-output
//
// `lineup publish` is the one writer of bench:<b>:conform; bin/bench-conform
// --publish pipes each bench's probe answers into it. Each lineup run mints a
// unique marker and passes it to bench-conform as NOVA_LINEUP_RUN; publish
// stamps it (--run, default $NOVA_LINEUP_RUN) as the record's run field, and
// only records carrying this run's marker count.
//
// Exit 0 lined up, 1 any RED (or a DRIFT record for publish), 2 could not run.
func init() {
	register(Verb{
		Name:    "lineup",
		Summary: "--redis <addr> --sprint <S> [--probes a,b,c,d,e] [--no-conform] --lane-gql-per-pass n --lane-cadence d: conform, probes, 7.18-7.21/7.24/7.25, exit 1 refuses sprint open; publish --bench <b> --all-yml f < probe writes bench:<b>:conform",
		Run:     cmdLineup,
	})
}

func lineupClient(addr string) *redis.Client {
	opts := &redis.Options{
		Addr: addr, Password: os.Getenv("NOVA_REDIS_BENCH_PASSWORD"),
		DialTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, MaxRetries: -1,
	}
	// --seat / NOVA_SEAT (#4052): the seat's login, read in this process. A seat
	// that cannot be read leaves the client unauthenticated, and its first
	// command is refused NOAUTH naming the store.
	if c, ok, err := seatcred.Active(); ok && err == nil {
		opts.Username, opts.Password = c.User, ""
		_ = c.Password.Use(func(pw string) error { opts.Password = pw; return nil })
	}
	return redis.NewClient(opts)
}

func cmdLineup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "publish" {
		return cmdLineupPublish(ctx, args[1:], os.Stdin, stdout, stderr)
	}
	fs := flag.NewFlagSet("lineup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	probes := fs.String("probes", "", "")
	conformCmd := fs.String("conform-cmd", "bench-conform --publish", "")
	noConform := fs.Bool("no-conform", false, "")
	remaining := fs.Int("gql-remaining", -1, "")
	perPass := fs.Int("lane-gql-per-pass", -1, "")
	cadence := fs.Duration("lane-cadence", 0, "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "lineup", err.Error()+"; it wants --redis <addr> --sprint <S>")
	}
	var problems []string
	if *addr == "" {
		problems = append(problems, "--redis <host:port> is required; the password reaches this process as NOVA_REDIS_BENCH_PASSWORD")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments (or: lineup publish)")
	}
	if len(problems) > 0 {
		return refuse(stderr, "lineup", strings.Join(problems, "; "))
	}
	client := lineupClient(*addr)
	defer client.Close()

	if *probes != "" && *sprint == "" {
		return refuse(stderr, "lineup", "--probes needs --sprint <S>")
	}
	run := preflight.LineupRun{Sprint: *sprint}
	if *probes != "" {
		run.Probes = strings.Split(*probes, ",")
	}
	if !*noConform {
		argv := strings.Fields(*conformCmd)
		run.Conform = func(ctx context.Context, marker string) error {
			err := runConformPublish(ctx, argv, marker, stderr)
			if err != nil {
				fmt.Fprintf(stdout, "CONFORM REFUSED %s\n", oneline.Err(err))
			} else {
				fmt.Fprintln(stdout, "CONFORM exit=0")
			}
			return err
		}
	}
	run.Enrich = func(in *preflight.LineupInput) {
		in.GraphQL = preflight.GraphQLBudget{Remaining: *remaining, CallsPerPass: *perPass, Cadence: *cadence, Known: *remaining >= 0}
		if !in.GraphQL.Known {
			if n, err := ghGraphQLRemaining(ctx); err == nil {
				in.GraphQL.Remaining, in.GraphQL.Known = n, true
			} else {
				fmt.Fprintf(stderr, "nova-sprint lineup: GraphQL budget: %s\n", oneline.Err(err))
			}
		}
		if exe, err := os.Executable(); err == nil {
			if hits, err := preflight.ScanBinaryForGraphQL(exe); err == nil {
				in.BinaryScanned, in.BinaryHits = true, hits
			}
		}
	}
	res, err := preflight.RunLineup(ctx, client, run)
	if err != nil {
		return refuse(stderr, "lineup", err.Error())
	}
	if res.ProbesHeld != "" {
		fmt.Fprintln(stdout, res.ProbesHeld)
	}
	fmt.Fprint(stdout, preflight.RenderLineup(res.In, res.Lines))
	if preflight.OpenGate(res.Lines) != nil {
		return 1
	}
	return 0
}

// runConformPublish runs bench-conform --publish, which probes every UP bench
// in one ssh batch and pipes each answer into `lineup publish`. The run marker
// reaches those publishes as NOVA_LINEUP_RUN in the environment. An error or a
// nonzero exit is a failed publish (RED on 7.18); a clean exit is still not
// trusted alone: every UP bench's record must carry this run's marker.
func runConformPublish(ctx context.Context, argv []string, marker string, stderr io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("--conform-cmd is empty")
	}
	testguard.RefuseHosts(argv[0], argv[1:]...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), preflight.RunMarkerEnv+"="+marker)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("%s exit=%d", argv[0], ee.ExitCode())
	}
	return err
}

// ghGraphQLRemaining reads the login's GraphQL budget by REST (GET
// /rate_limit), never by a GraphQL call.
func ghGraphQLRemaining(ctx context.Context) (int, error) {
	args := []string{"api", "rate_limit", "--jq", ".resources.graphql.remaining"}
	testguard.RefuseHosts("gh", args...)
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func cmdLineupPublish(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lineup publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	allYML := fs.String("all-yml", "", "")
	runMarker := fs.String("run", os.Getenv(preflight.RunMarkerEnv), "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "lineup publish", err.Error()+"; it wants --redis <addr> --bench <b> --all-yml <file> [--run <marker>]")
	}
	var problems []string
	if *addr == "" {
		problems = append(problems, "--redis <host:port> is required")
	}
	if *bench == "" {
		problems = append(problems, "--bench <b> is required")
	}
	if *allYML == "" {
		problems = append(problems, "--all-yml <fleet/group_vars/all.yml> is required: the declared nova_build and secrets_rev")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, and the probe output on stdin")
	}
	if len(problems) > 0 {
		return refuse(stderr, "lineup publish", strings.Join(problems, "; "))
	}
	d, err := preflight.ReadDeclared(*allYML)
	if err != nil {
		return refuse(stderr, "lineup publish", err.Error())
	}
	body, err := io.ReadAll(stdin)
	if err != nil {
		return refuse(stderr, "lineup publish", "stdin: "+err.Error())
	}
	answers := preflight.ParseProbe(bytes.NewReader(body))
	if len(answers) == 0 {
		return refuse(stderr, "lineup publish", "no probe answers on stdin; an empty probe is not a record")
	}
	cf := preflight.Evaluate(*bench, answers, d)
	cf.Run = *runMarker
	client := lineupClient(*addr)
	defer client.Close()
	if err := preflight.PublishConform(ctx, client, cf); err != nil {
		return refuse(stderr, "lineup publish", "redis: "+err.Error())
	}
	for _, k := range cf.Keys {
		fmt.Fprintln(stdout, cf.Findings[k])
	}
	fmt.Fprintf(stdout, "CONFORM %s %s keys=%s\n", oneline.Escape(*bench), cf.Verdict, strings.Join(cf.Keys, ","))
	if cf.Verdict != "PASS" {
		return 1
	}
	return 0
}
