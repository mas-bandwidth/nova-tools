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
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
	"github.com/redis/go-redis/v9"
)

// lineup is #3108 (#2756 v6 section 7, controls 49, 64, 65): run the conform
// publish across the UP benches in one batch, cut the five probes, then print
// every DRIFT line, the 7.18-7.21, 7.24 and 7.25 checks and the x/y tally.
// Any RED refuses sprint open (exit 1).
//
//	nova-sprint lineup --redis <addr> --sprint <S> [--probes a,b,c,d,e]
//	    [--conform-cmd "bench-conform --publish" | --no-conform]
//	    [--gql-remaining n] --lane-gql-per-pass n --lane-cadence 10s
//	nova-sprint lineup publish --redis <addr> --bench <b> --all-yml <file> < probe-output
//
// `lineup publish` is the one writer of bench:<b>:conform; bin/bench-conform
// --publish pipes each bench's probe answers into it.
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
	return redis.NewClient(&redis.Options{
		Addr: addr, Password: os.Getenv("NOVA_REDIS_BENCH_PASSWORD"),
		DialTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, MaxRetries: -1,
	})
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

	if !*noConform {
		code, err := runConformPublish(ctx, strings.Fields(*conformCmd), stderr)
		switch {
		case err != nil:
			fmt.Fprintf(stdout, "CONFORM ERROR %s\n", oneline.Err(err))
		default:
			fmt.Fprintf(stdout, "CONFORM exit=%d\n", code)
		}
	}
	if *probes != "" {
		if *sprint == "" {
			return refuse(stderr, "lineup", "--probes needs --sprint <S>")
		}
		if err := preflight.CutProbes(ctx, client, *sprint, strings.Split(*probes, ",")); err != nil {
			return refuse(stderr, "lineup", "probe cut: "+err.Error())
		}
	}
	in, err := preflight.GatherLineup(ctx, client, *sprint)
	if err != nil {
		return refuse(stderr, "lineup", "redis: "+err.Error())
	}
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
	lines := preflight.LineupChecks(in)
	fmt.Fprint(stdout, preflight.RenderLineup(in, lines))
	if preflight.OpenGate(lines) != nil {
		return 1
	}
	return 0
}

// runConformPublish runs bench-conform --publish, which probes every UP bench
// in one ssh batch and pipes each answer into `lineup publish`. Its exit is
// reported, not trusted: the records it wrote are what the checks read.
func runConformPublish(ctx context.Context, argv []string, stderr io.Writer) (int, error) {
	if len(argv) == 0 {
		return 0, fmt.Errorf("--conform-cmd is empty")
	}
	testguard.RefuseHosts(argv[0], argv[1:]...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), nil
	}
	return 0, err
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
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "lineup publish", err.Error()+"; it wants --redis <addr> --bench <b> --all-yml <file>")
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
