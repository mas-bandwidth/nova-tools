package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runLandEval(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("land eval")
	redisAddr := fs.String("redis", "", "Redis address")
	sprint := fs.String("sprint", "", "Sprint ID")
	repo := fs.String("repo", "", "Target repository")
	policyPath := fs.String("policy", "", "Path to repo policy file")
	once := fs.Bool("once", true, "Run single evaluation pass")

	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "land eval", err.Error())
	}

	if *repo == "" {
		return refuse(errOut, "land eval", "needs --repo <repo>")
	}

	if *redisAddr == "" {
		*redisAddr = os.Getenv("NOVA_REDIS_ADDR")
		if *redisAddr == "" {
			*redisAddr = "127.0.0.1:6379"
		}
	}

	if *sprint == "" {
		*sprint = os.Getenv("NOVA_SPRINT")
		if *sprint == "" {
			*sprint = "current"
		}
	}

	// Load policy
	polFile := *policyPath
	if polFile == "" {
		polFile = filepath.Join("fleet", "land", *repo+".yml")
	}

	var polRepo *land.RepoPolicy
	if _, err := os.Stat(polFile); err == nil {
		p, err := land.LoadPolicy(polFile)
		if err == nil {
			polRepo = p
		}
	}

	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land eval: connect redis: %v\n", err)
		return 6
	}
	defer st.Close()

	// Sync policy to Redis if available
	if polRepo != nil {
		for _, bp := range polRepo.Bases {
			_ = land.SyncPolicyToRedis(ctx, st.Client(), *repo, bp)
		}
	}

	evalCount, landableCount, err := land.EvalPass(ctx, st.Client(), *sprint, *repo, polRepo)
	if err != nil {
		if strings.Contains(err.Error(), "fenced") || strings.Contains(err.Error(), "lease") {
			fmt.Fprintf(errOut, "nova-sprint land eval: %v\n", err)
			return 3
		}
		return refuse(errOut, "land eval", err.Error())
	}

	fmt.Fprintf(out, "EVAL repo=%s sprint=%s evaluated=%d landable=%d once=%v\n",
		*repo, *sprint, evalCount, landableCount, *once)
	return 0
}
