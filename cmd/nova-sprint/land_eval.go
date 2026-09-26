package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runLandEval(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("land eval")
	redisAddr := fs.String("redis", redisDefault(), "Redis address")
	sprint := fs.String("sprint", "", "Sprint ID")
	repo := fs.String("repo", "", "Target repository")
	policyPath := fs.String("policy", "", "repo policy file (default fleet/land/<repo>.yml under the working directory when present; absent means the store's policy; a named or found file that cannot be read refuses)")
	once := fs.Bool("once", true, "Run single evaluation pass")
	mirror := fs.String("mirror", "", "bench mirror of the repo (default ~/nova-bench/mirror/<repo>.git when present; \"none\": no mirror reads)")
	consumer := fs.String("consumer", "eval", "consumer name in ev:github group land")
	shadow := fs.Bool("shadow", false, "compare SHADOW lines on stdin (lander --shadow) with the hand lander's CLOSE records, per PR (#3800)")

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
		return runLandEvalShadow(ctx, addr, landEvalInput(), out, errOut)
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

	// THE POLICY. --policy names it; else the default discovery is
	// fleet/land/<repo>.yml under the working directory (the fleet checkout's
	// layout), which is the one place a policy file is looked for. A policy
	// that was asked for, or found there, and cannot be read or parsed refuses
	// here, before anything is evaluated or synced: a review policy on disk is
	// never silently ignored (Stella's audit stella-e6353bf80360, finding 1).
	// Only an ABSENT default means no file policy, and the receipt says so
	// (policy=none): RunEval then reads the base policy the store holds.
	polFile, explicit := *policyPath, *policyPath != ""
	if !explicit {
		polFile = filepath.Join("fleet", "land", *repo+".yml")
	}
	var polRepo *land.RepoPolicy
	policyName := "none"
	if _, err := os.Stat(polFile); err != nil {
		if explicit || !os.IsNotExist(err) {
			return refuse(errOut, "land eval", policyRefusal(polFile, explicit, err))
		}
	} else {
		p, err := land.LoadPolicy(polFile)
		if err != nil {
			return refuse(errOut, "land eval", policyRefusal(polFile, explicit, err))
		}
		polRepo, policyName = p, polFile
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

	// Sync the file's policy to the store, base by base in name order. A sync
	// that fails refuses before evaluation and names what changed: the bases
	// synced before it now hold the file's policy, the rest are as they were.
	if polRepo != nil {
		bases := make([]string, 0, len(polRepo.Bases))
		for b := range polRepo.Bases {
			bases = append(bases, b)
		}
		sort.Strings(bases)
		for i, b := range bases {
			if err := land.SyncPolicyToRedis(ctx, st.Client(), *repo, polRepo.Bases[b]); err != nil {
				if storeDown(errOut, "land eval", err) {
					return 6
				}
				return refuse(errOut, "land eval", fmt.Sprintf("policy %s: sync base %s to the store (ns_policy_set): %v; nothing evaluated; store partially changed: bases %s synced, %s not; fix the store or the policy and rerun",
					polFile, b, err, dash(strings.Join(bases[:i], ",")), strings.Join(bases[i:], ",")))
			}
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
		if storeDown(errOut, "land eval", err) {
			return 6
		}
		return refuse(errOut, "land eval", err.Error())
	}

	in := rep.Inbound
	if in == nil {
		in = &land.InboundResult{}
	}
	fmt.Fprintf(out, "EVAL repo=%s sprint=%s evaluated=%d landable=%d inbound=%d heads=%d holds=%d released=%d nobody=%d missed=%d mirror=%t once=%v policy=%s\n",
		*repo, *sprint, rep.Evaluated, rep.Landable, in.Entries, in.Heads, in.Holds, in.Released, in.NoBody,
		len(rep.Missed), mirrorDir != "", *once, policyName)
	return 0
}

// policyRefusal is the line for a policy file that could not be used: the
// path, whether --policy named it or the default discovery found it, the
// cause, that nothing ran, and the way out.
func policyRefusal(path string, explicit bool, err error) string {
	how := "the default fleet/land/<repo>.yml"
	if explicit {
		how = "--policy"
	}
	return fmt.Sprintf("policy %s (%s) could not be used: %v; nothing evaluated, store unchanged; fix the file, name another with --policy, or move it aside to evaluate on the store's policy alone", path, how, err)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
