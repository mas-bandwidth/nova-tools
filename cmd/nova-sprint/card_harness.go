package main

// `nova-sprint card run --sprint <S> --ids <L> --attempt <a>` is the
// bench-side card harness (#3681, internal/nsprint/card/run.go): what
// rowan-tools' bash nova-card-harness did, as Go with Redis state. The card
// wrapper reaches the same function in-process; this verb is the by-hand
// and test entry. It reads the bench's card.env from the environment
// (NOVA_CARD_REDIS, NOVA_CARD_BENCH, NOVA_CARD_HARNESS_BIN,
// NOVA_CARD_DEADLINE, NOVA_CARD_TOKENS, HOME, and the store's
// NOVA_SPRINT_REDIS_USER / NOVA_SPRINT_REDIS_PASSWORD_ENV), the four seat
// keys the launcher carries, and NOVA_CARD_OUT / NOVA_CARD_JOB unless --out
// and --job name them. It prints one line and exits with the harness's
// code: native's rc, 1 when rc=0 left no RESULT.md, 2 refused (nothing
// ran), and the usual usage refusal for a bad argv.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func runCardRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runCardRunEnv(ctx, args, stdout, stderr, os.Getenv)
}

func runCardRunEnv(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := verbflag.New("card run")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	attempt := fs.String("attempt", "", "the attempt number")
	addr := fs.String("redis", redisOr(getenv("NOVA_CARD_REDIS")), verbflag.HelpRedis)
	out := fs.String("out", getenv("NOVA_CARD_OUT"), "the results directory (default NOVA_CARD_OUT)")
	job := fs.String("job", getenv("NOVA_CARD_JOB"), "the job directory (default NOVA_CARD_JOB)")
	if err := fs.Parse(args); err != nil {
		return cardUsage(stderr, "card run", err.Error())
	}
	if fs.NArg() != 0 {
		return cardUsage(stderr, "card run", "takes flags, not positional arguments")
	}
	a, err := strconv.Atoi(*attempt)
	label := oneID(*ids)
	if *sprint == "" || label == "" || err != nil || a < 1 {
		return cardUsage(stderr, "card run", "wants --sprint, --ids <label> and --attempt (a positive integer)")
	}
	cfg, missing := card.RunConfigFromEnv(getenv)
	if *addr == "" {
		missing = append(missing, "NOVA_CARD_REDIS (or --redis)")
	}
	if *out == "" {
		missing = append(missing, "NOVA_CARD_OUT (or --out)")
	}
	if *job == "" {
		missing = append(missing, "NOVA_CARD_JOB (or --job)")
	}
	if len(missing) > 0 {
		return cardUsage(stderr, "card run", "missing "+strings.Join(missing, ", "))
	}
	cfg.Sprint, cfg.Label, cfg.Attempt = *sprint, label, a
	if cfg.OutDir, err = filepath.Abs(*out); err != nil {
		return cardUsage(stderr, "card run", "--out: "+err.Error())
	}
	if cfg.JobDir, err = filepath.Abs(*job); err != nil {
		return cardUsage(stderr, "card run", "--job: "+err.Error())
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		fmt.Fprintln(stdout, (card.RunReport{Card: cfg.Sprint + "/" + cfg.Label + "/" + strconv.Itoa(a), Code: card.RunExitRefused, Why: "redis: " + err.Error()}).Line())
		return card.RunExitRefused
	}
	defer st.Close()
	rep := card.Run(ctx, st, cfg)
	fmt.Fprintln(stdout, rep.Line())
	return rep.Code
}
