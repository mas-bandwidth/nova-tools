package main

// `native --mode pipeline`: THE CARD IS THE PIPELINE, AND THIS TOOL IS THE HARNESS
// (issue #856).
//
// Glenn, 2026-09-16: "The idea is for it to have no memory between calls. The idea is to
// just do work." A card's deterministic steps -- clone, checkout, test, commit -- run here,
// in this process's own shell, inside the same wall the harness would have run in. Its
// model steps are ONE chat completion each, straight to the route's OpenAI-compatible
// endpoint, with no tools, no session and no transcript. The harness binary is not started
// at all: there is no agent loop to start.
//
// What this file owns is the WIRING -- the job's directories, the wall, the capture, the
// route, and the NATIVE line the batch reads. The grammar and the run are in
// internal/swarm/pipeline*.go, where they are tested without a process.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// pipelineOptions are the flags the two modes add to `native`.
type pipelineOptions struct {
	maxInput   int    // the per-step input cap
	maxSteps   int    // the step-count cap
	maxCalls   int    // 0: one call per model step plus one retry
	endpoint   string // a base URL that overrides the provider's own
	keyFile    string // a key file that overrides the provider's own
	configFile string // the opencode.json the route is resolved from
}

// runNativePipeline runs one card as a pipeline and prints the NATIVE line. It returns the
// command's exit code: 0 the card ran, 2 a refusal, 1 a step that ended non-zero.
func runNativePipeline(cfg nativeRunConfig, opts pipelineOptions, stdout, stderr io.Writer) int {
	abslot, err := swarm.AbsResolved(cfg.slotDir)
	if err != nil {
		refuseNative(stderr, fmt.Sprintf("the slot directory %s could not be made absolute: %s", oneline.Field(cfg.slotDir), oneline.Escape(err.Error())))
		return 2
	}
	cfg.slotDir = abslot
	absroot, err := swarm.AbsResolved(cfg.root)
	if err != nil {
		refuseNative(stderr, fmt.Sprintf("the configured root %s could not be made absolute: %s", oneline.Field(cfg.root), oneline.Escape(err.Error())))
		return 2
	}
	cfg.root = absroot
	if !within(cfg.root, cfg.slotDir) {
		refuseNative(stderr, fmt.Sprintf("the slot directory %s is outside the configured root %s",
			oneline.Field(cfg.slotDir), oneline.Field(cfg.root)))
		return 2
	}
	// THE HARNESS BINARY IS RESOLVED EVEN THOUGH IT IS NEVER STARTED. Its directory is a
	// `--read` of the wall (git and the toolchain resolve beside it), and its sha is on the
	// line a reader checks the run against; a bare name would put `.` in a wall rule, which
	// every wall refuses, and `-` in the field.
	if !strings.ContainsRune(cfg.binary, filepath.Separator) {
		found, lerr := exec.LookPath(cfg.binary)
		if lerr != nil {
			refuseNative(stderr, fmt.Sprintf("the harness binary %s is missing", oneline.Field(cfg.binary)))
			return 2
		}
		cfg.binary = found
	}
	if _, serr := os.Stat(cfg.binary); serr != nil {
		refuseNative(stderr, fmt.Sprintf("the harness binary %s is missing", oneline.Field(cfg.binary)))
		return 2
	}

	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	dataHome := filepath.Join(cfg.slotDir, "data")
	tmpDir := filepath.Join(cfg.slotDir, "tmp", cfg.label)
	for _, dir := range []string{jobDir, dataHome, tmpDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			refuseNative(stderr, fmt.Sprintf("the directory %s could not be made: %s", oneline.Field(dir), oneline.Escape(err.Error())))
			return 2
		}
	}

	// THE ROUTE. One resolution, before anything is spent: the endpoint, the model id and
	// the key, out of the provider config and the provider's own key file. The refusal names
	// the FILE, never the key.
	route, err := swarm.ResolveRoute(cfg.model, swarm.RouteFiles{
		ConfigPath: opts.configFile,
		KeyFile:    opts.keyFile,
		Endpoint:   opts.endpoint,
	})
	if err != nil {
		refuseNative(stderr, fmt.Sprintf("%s the route could not be resolved: %s", oneline.Field(cfg.label), oneline.Escape(err.Error())))
		return 2
	}

	// THE CAPTURE. The same file a harness run writes, for the same readers: `harness=ok`,
	// the gather's evidence, and the per-step input sizes a coordinator reads the bill from.
	outLog := filepath.Join(jobDir, "harness-output.log")
	capture, err := os.OpenFile(outLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND|swarm.ONoFollow, 0o644)
	if err != nil {
		refuseNative(stderr, fmt.Sprintf("the harness output log %s could not be opened: %s", oneline.Field(outLog), oneline.Escape(err.Error())))
		return 2
	}
	defer capture.Close()

	// THE WALL. A pipeline's shell step is the card's own work and is walled exactly as the
	// harness would have been: the same flags, the same job, data and temp directories, with
	// `sh -c` in the place of the harness binary. --no-wall is the caller's own choice and is
	// named on the line, never implied (SPEC-SANDBOX rule 1).
	wall := cfg.sandbox
	if wall == "" && !cfg.noWall {
		found, lerr := exec.LookPath(swarm.SandboxBinary)
		if lerr != nil {
			refuseNative(stderr, fmt.Sprintf("%s no wall: %s is on no PATH entry and --sandbox names no file; name the wall with --sandbox <path> or run with --no-wall and own every read and write the child makes",
				oneline.Field(cfg.label), oneline.Field(swarm.SandboxBinary)))
			return 2
		}
		wall = found
	}
	if wall != "" && len(cfg.repos) > 0 && !sandboxHostRules(wall) {
		refuseNative(stderr, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(cfg.label)))
		return 2
	}
	if wall == "" && len(cfg.repos) > 0 {
		refuseNative(stderr, fmt.Sprintf("%s wall cannot express repo rule", oneline.Field(cfg.label)))
		return 2
	}
	cacheDir := nativeCacheDir(cfg)
	if cacheDir != "" {
		for _, d := range []string{filepath.Join(cacheDir, "go-mod"), filepath.Join(cacheDir, "go-build")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				refuseNative(stderr, fmt.Sprintf("the shared go cache %s could not be made: %s", oneline.Field(d), oneline.Escape(err.Error())))
				return 2
			}
		}
	}
	secretEnv := ""
	if cfg.worker != nil {
		secretEnv = cfg.worker.Secret
	}
	env := nativeChildEnv(dataHome, jobDir, tmpDir, cacheDir, secretEnv)
	wallName := "none"
	if cfg.noWall {
		wallName = "none-by-flag"
	}
	shell := pipelineShell(cfg, wall, env, dataHome, jobDir, tmpDir, &wallName)

	start := time.Now()
	cardHash := sha256.Sum256(cfg.card)
	binaryHash, _ := fileSHA256(cfg.binary)
	res := swarm.RunPipeline(context.Background(), swarm.PipelineConfig{
		Card:          cfg.card,
		JobDir:        jobDir,
		Label:         cfg.label,
		Route:         route,
		Shell:         shell,
		Env:           env,
		MaxInputBytes: opts.maxInput,
		MaxSteps:      opts.maxSteps,
		MaxCalls:      opts.maxCalls,
		Log:           capture,
	})
	fmt.Fprintf(stdout, "NATIVE OK label=%s job=%s tmp=%s rc=%d wall=%.2fs sandbox=%s card_sha256=%s binary_sha256=%s config=%s harness=%s mode=pipeline steps=%d calls=%d in=%d out=%d cached=%d usd=%.4f\n",
		oneline.Field(cfg.label), oneline.Field(jobDir), oneline.Field(tmpDir), res.RC, time.Since(start).Seconds(),
		oneline.Field(wallName), oneline.Field(hex.EncodeToString(cardHash[:])), oneline.Field(dash(binaryHash)),
		oneline.Field(dash("")), oneline.Field(harnessState(jobDir)),
		res.Steps, res.Calls, res.TokensIn, res.TokensOut, res.Cached, res.USD)
	if res.Refusal != "" {
		fmt.Fprintln(stderr, res.Refusal)
		return 2
	}
	if res.RC != 0 {
		return 1
	}
	return 0
}

// pipelineShell is one deterministic step's shell: `sh -c <script>` inside the wall when
// there is one, in this process otherwise. The wall's own SANDBOX OK line names the backend
// on the NATIVE line, read from the FIRST step that ran inside it, exactly as a harness run
// reads it from the child's stderr.
func pipelineShell(cfg nativeRunConfig, wall string, env []string, dataHome, jobDir, tmpDir string, wallName *string) swarm.ShellFunc {
	return func(ctx context.Context, dir, script string) swarm.ShellResult {
		argv := []string{"sh", "-c", script}
		path := "sh"
		if wall != "" {
			path = wall
			flags := nativeWallFlags(cfg.binary, cfg, dataHome, jobDir, tmpDir)
			// The step's own cwd replaces the job directory the harness would have run in:
			// a card's steps after its clone work inside ./repo.
			flags = replaceFlagValue(flags, "--cwd", dir)
			argv = append(append(flags, "--", "sh", "-c"), script)
		} else {
			argv = argv[1:]
		}
		cmd := exec.CommandContext(ctx, path, argv...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		rc := 0
		if err != nil {
			rc = 1
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			}
		}
		if wall != "" && *wallName == "none" {
			if backend, _, reason := wallNamed(string(out)); reason == "" {
				*wallName = backend
			}
		}
		return swarm.ShellResult{RC: rc, Output: string(out)}
	}
}

// replaceFlagValue sets one flag's value in an argv, appending the pair when it is absent.
func replaceFlagValue(argv []string, flag, value string) []string {
	out := append([]string{}, argv...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == flag {
			out[i+1] = value
			return out
		}
	}
	return append(out, flag, value)
}

// writeBudgetResult publishes the PARTIAL RESULT of a `MODE: explore` card the turn budget
// stopped (issue #856). A card killed mid-loop publishes nothing of its own, and a job with
// no RESULT.md is scored `no-result` by the gather -- the token for a model that chose to
// publish nothing, which is not what happened. This is the card's own contract line and one
// ABSTAIN line naming the budget, so the packet says why in one line and nobody opens the
// job. A card that already published its own result keeps it: nothing is ever overwritten.
func writeBudgetResult(jobDir string, card []byte, turns, budget int) bool {
	dst := filepath.Join(jobDir, swarm.ResultFile)
	if _, err := os.Stat(dst); err == nil {
		return false
	}
	contract := string(card)
	if i := strings.IndexByte(contract, '\n'); i >= 0 {
		contract = contract[:i]
	}
	body := fmt.Sprintf("%s\nABSTAIN reason=turn-budget turns=%d max=%d -- the harness loop passed its turn budget and was stopped; the work below it is partial\n",
		strings.TrimRight(contract, "\r"), turns, budget)
	return os.WriteFile(dst, []byte(body), 0o644) == nil
}

// nativeSuffix is the WHOLE optional tail of a harness run's NATIVE OK line: what the
// harness's own fence rejected (issue #644) and what the turn budget did (issue #856). It
// is one function so the line has ONE escaping claim rather than a sum of two: fenceSuffix
// puts its path through oneline.Field inside itself, and exploreSuffix prints integers.
func nativeSuffix(res nativeRunResult, budget int) string {
	return fenceSuffix(res.fence) + exploreSuffix(res.turns, budget, res.overBudget)
}

// exploreSuffix is what the NATIVE line of a budgeted explore card carries.
func exploreSuffix(turns, budget int, stopped bool) string {
	if budget <= 0 {
		return ""
	}
	out := fmt.Sprintf(" mode=explore turns=%d max_turns=%d", turns, budget)
	if stopped {
		out += " stopped=turn-budget"
	}
	return out
}
