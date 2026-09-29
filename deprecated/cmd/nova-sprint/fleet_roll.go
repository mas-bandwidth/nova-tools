package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// RollDeps holds the seams for fleet roll so tests can mock them in parallel.
type RollDeps struct {
	Play   fleet.PlayRunner
	DevTip func(ctx context.Context) (string, error)
}

func defaultDevTip(ctx context.Context) (string, error) {
	// Try git rev-parse origin/dev
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "origin/dev")
	if out, err := cmd.Output(); err == nil {
		sha := strings.TrimSpace(string(out))
		if len(sha) == 40 {
			return sha, nil
		}
	}
	// Fallback to git rev-parse HEAD
	cmd = exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	if out, err := cmd.Output(); err == nil {
		sha := strings.TrimSpace(string(out))
		if len(sha) == 40 {
			return sha, nil
		}
	}
	return "", errors.New("cannot determine dev tip (try --to <sha>)")
}

func runFleetRoll(ctx context.Context, args []string, out, errOut io.Writer) (code int) {
	defer verbflag.Recover(out, "nova-sprint", usage, &code)
	return runFleetRollWith(ctx, args, out, errOut, RollDeps{
		Play:   fleet.Play,
		DevTip: defaultDevTip,
	})
}

func runFleetRollWith(ctx context.Context, args []string, out, errOut io.Writer, deps RollDeps) (code int) {
	defer verbflag.Recover(out, "nova-sprint", usage, &code)

	fs := verbflag.New("fleet roll")
	to := fs.String("to", "", "")
	redisAddr := fs.String("redis", redisDefault(), "")

	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet roll", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet roll", "takes flags, not positional arguments")
	}

	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet roll", err.Error())
	}
	defer st.Close()
	client := st.Client()

	sha := strings.TrimSpace(*to)
	if sha == "" {
		devTipFn := deps.DevTip
		if devTipFn == nil {
			devTipFn = defaultDevTip
		}
		tip, err := devTipFn(ctx)
		if err != nil {
			return refuse(errOut, "fleet roll", err.Error())
		}
		sha = tip
	}

	var version string
	if strings.HasPrefix(sha, "v") {
		version = sha
	} else {
		sha8 := sha
		if len(sha8) > 8 {
			sha8 = sha8[:8]
		}
		version = fmt.Sprintf("v0.16.0-dev.%s", sha8)
	}

	// 1. Release: sets target release version/commit in Redis (fleet:release, fleet:release:sha).
	pipe := client.Pipeline()
	pipe.HSet(ctx, "fleet:release", "version", version, "commit", sha, "sha", sha)
	pipe.Set(ctx, "fleet:release:sha", sha, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		return fleetRefuse(errOut, "fleet roll", err)
	}

	// 2. Play: triggers fleet convergence via the play runner seam (fleet.Play).
	playFn := deps.Play
	if playFn == nil {
		playFn = fleet.Play
	}
	if err := playFn(ctx, client); err != nil {
		fmt.Fprintf(errOut, "FLEET ROLL REFUSED: play: %s\n", oneline.Escape(err.Error()))
		return 1
	}

	// 3. Verify pass: reads each registered bench's beat (bench:<b>:beat field build or sha) directly from Redis (strictly NO ssh!).
	benches, err := client.SMembers(ctx, "benches").Result()
	if err != nil {
		return fleetRefuse(errOut, "fleet roll", err)
	}
	sort.Strings(benches)

	if len(benches) == 0 {
		fmt.Fprintf(out, "FLEET ROLL OK version=%s benches=0\n", version)
		return 0
	}

	beatPipe := client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(benches))
	for i, b := range benches {
		cmds[i] = beatPipe.HMGet(ctx, "bench:"+b+":beat", "build", "sha")
	}
	if _, err := beatPipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fleetRefuse(errOut, "fleet roll", err)
	}

	type checkResult struct {
		bench  string
		want   string
		have   string
		status string // "ok" or "behind"
	}

	var results []checkResult
	var behindList []string

	sha8 := sha
	if len(sha8) > 8 {
		sha8 = sha8[:8]
	}

	for i, b := range benches {
		vals := cmds[i].Val()
		var haveBuild, haveSha string
		if len(vals) > 0 && vals[0] != nil {
			haveBuild = fmt.Sprint(vals[0])
		}
		if len(vals) > 1 && vals[1] != nil {
			haveSha = fmt.Sprint(vals[1])
		}

		have := "none"
		if haveBuild != "" {
			if bf, ok := buildinfo.Parse(haveBuild); ok && bf.Version != "" {
				have = bf.Version
			} else {
				have = strings.Join(strings.Fields(haveBuild), "_")
			}
		} else if haveSha != "" {
			have = haveSha
		}

		status := "behind"
		isOK := false
		if have != "none" {
			if have == version {
				isOK = true
			} else if haveSha != "" && (haveSha == sha || strings.HasPrefix(sha, haveSha) || strings.HasPrefix(haveSha, sha)) {
				isOK = true
			} else if strings.Contains(haveBuild, version) || (len(sha8) >= 8 && strings.Contains(haveBuild, sha8)) {
				isOK = true
			} else if len(sha8) >= 8 && strings.Contains(have, sha8) {
				isOK = true
			}
		}

		if isOK {
			status = "ok"
		} else {
			behindList = append(behindList, b)
		}

		results = append(results, checkResult{
			bench:  b,
			want:   version,
			have:   have,
			status: status,
		})
	}

	// 4. Prints tabular summary: bench, want, have, ok|behind.
	for _, r := range results {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", r.bench, r.want, r.have, r.status)
	}

	// 5. Exit status:
	//    - Exit 0: FLEET ROLL OK version=<version> benches=<n>
	//    - Exit 1: FLEET ROLL BEHIND version=<version> behind=<list> (names the benches that did not converge to target SHA).
	if len(behindList) > 0 {
		fmt.Fprintf(out, "FLEET ROLL BEHIND version=%s behind=%s\n", version, strings.Join(behindList, ","))
		return 1
	}

	fmt.Fprintf(out, "FLEET ROLL OK version=%s benches=%d\n", version, len(benches))
	return 0
}
