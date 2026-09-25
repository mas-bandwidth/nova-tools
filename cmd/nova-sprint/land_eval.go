package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	mirror := fs.String("mirror", "", "bench mirror of the repo (default ~/nova-bench/mirror/<repo>.git when present; \"none\": no mirror reads)")
	consumer := fs.String("consumer", "eval", "consumer name in ev:github group land")
	shadow := fs.Bool("shadow", false, "compare SHADOW lines on stdin (lander --shadow) with the hand lander's CLOSE records, per PR (#3800)")
	since := fs.String("since", "", "duration window of shadow verdicts to evaluate from Redis stream (e.g. 1h)")

	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "land eval", err.Error())
	}
	if *shadow {
		addr := *redisAddr
		if addr == "" {
			addr = os.Getenv("NOVA_REDIS_ADDR")
		}
		if addr == "" {
			addr = "127.0.0.1:6379"
		}
		var sinceDur time.Duration
		if *since != "" {
			d, err := time.ParseDuration(*since)
			if err != nil {
				return refuse(errOut, "land eval", "--since: "+err.Error())
			}
			if d <= 0 {
				return refuse(errOut, "land eval", "--since: duration must be positive")
			}
			sinceDur = d
		}
		return runLandEvalShadow(ctx, addr, *repo, sinceDur, landEvalInput(), out, errOut)
	}

	if *since != "" {
		return refuse(errOut, "land eval", "--since is a --shadow flag")
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

	mirrorDir := *mirror
	if mirrorDir == "none" {
		mirrorDir = ""
	} else if mirrorDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if d := filepath.Join(home, "nova-bench", "mirror", *repo+".git"); isDir(d) {
				mirrorDir = d
			}
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

	rep, err := land.RunEval(ctx, st.Client(), land.EvalConfig{
		Sprint: *sprint, Repo: *repo, Policy: polRepo, MirrorDir: mirrorDir, Consumer: *consumer,
	})
	if rep != nil {
		for _, m := range rep.Missed {
			fmt.Fprintln(out, m)
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "fenced") || strings.Contains(err.Error(), "lease") {
			fmt.Fprintf(errOut, "nova-sprint land eval: %v\n", err)
			return 3
		}
		return refuse(errOut, "land eval", err.Error())
	}

	in := rep.Inbound
	if in == nil {
		in = &land.InboundResult{}
	}
	fmt.Fprintf(out, "EVAL repo=%s sprint=%s evaluated=%d landable=%d inbound=%d heads=%d holds=%d released=%d nobody=%d missed=%d mirror=%t once=%v\n",
		*repo, *sprint, rep.Evaluated, rep.Landable, in.Entries, in.Heads, in.Holds, in.Released, in.NoBody,
		len(rep.Missed), mirrorDir != "", *once)
	return 0
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
