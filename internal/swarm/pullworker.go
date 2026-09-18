package swarm

// Pull workers under leases and heartbeats (docs/SPEC-JOBS.md section 3).
//
// A worker process takes one card at a time under a slot lease from the bench
// store (<store>/queue/), runs each card inside a container from the toolchain
// image (or a configured runner) with secrets passed via `nova-secrets exec` from
// the bench's seat, heartbeats the lease while the card executes, harvests the
// RESULT.md before releasing the lease, and exits cleanly on SIGTERM.
//
// The container is the hygiene: all temporary files and toolchain caches are
// held in disposable container tmpfs mounts (/tmp, /home/card) and the
// single /work bind volume is cleaned up when the run finishes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// Default lease duration and container image.
const (
	DefaultWorkerLeaseDur = 30 * time.Minute
	DefaultWorkerImage    = "nova-card:latest"
	DefaultWorkerModel    = "deepseek/deepseek-chat"
)

// PullWorkerOptions specifies the configuration of a pull worker.
type PullWorkerOptions struct {
	Bench     string        // bench name (e.g. "hulk", "space")
	Slots     int           // slot count (default 1)
	Seat      string        // bench seat name for nova-secrets exec
	Store     string        // bench store directory holding queue/, taken/, slots/
	Harvest   string        // directory where card RESULT.md is saved
	Image     string        // container image name
	Model     string        // default model
	Runner    string        // custom runner command (overrides container execution)
	Container string        // container engine ("podman", "docker", or "none")
	For       time.Duration // lease duration (default 30m)
	Once      bool          // if true, run at most one card and exit
	Stdout    io.Writer
	Stderr    io.Writer

	// RunCard is an optional test hook to execute a card instead of the container/runner command.
	RunCard func(ctx context.Context, label, cardPath, workDir string) (int, error)
}

// BuildContainerArgs constructs the exact podman/docker run command per infra/image/README.md.
func BuildContainerArgs(engine, image, workDir, model, label, cardContent, secretKey string) []string {
	if engine == "" {
		engine = "podman"
	}
	if image == "" {
		image = DefaultWorkerImage
	}
	if model == "" {
		model = DefaultWorkerModel
	}
	if secretKey == "" {
		secretKey = "DEEPSEEK_API_KEY"
	}

	args := []string{
		"run", "--rm",
		"--userns=keep-id:uid=10001,gid=10001",
		"--read-only",
		"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=1g",
		"--mount", "type=tmpfs,destination=/home/card,tmpfs-size=2g,tmpfs-mode=0700,U=true,notmpcopyup",
		"--security-opt", "no-new-privileges",
		"--cap-drop=ALL",
		"--memory", "8g",
		"--pids-limit", "512",
		"-v", fmt.Sprintf("%s:/work", workDir),
		"-e", secretKey,
		image,
		"opencode", "run", "--model", model, "--title", label, "--", cardContent,
	}
	return args
}

// WrapSecretsExec wraps a command line with `nova-secrets exec --as <seat> -- <cmd...>` if seat is set.
func WrapSecretsExec(seat string, cmdArgs []string) []string {
	if strings.TrimSpace(seat) == "" {
		return cmdArgs
	}
	wrapped := []string{"nova-secrets", "exec", "--as", seat, "--"}
	return append(wrapped, cmdArgs...)
}

// RunPullWorker executes the pull worker loop.
func RunPullWorker(ctx context.Context, opts PullWorkerOptions) int {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Slots <= 0 {
		opts.Slots = 1
	}
	if opts.For <= 0 {
		opts.For = DefaultWorkerLeaseDur
	}
	if strings.TrimSpace(opts.Store) == "" {
		opts.Store = opts.Bench
	}
	if strings.TrimSpace(opts.Harvest) == "" {
		opts.Harvest = filepath.Join(opts.Store, "harvest")
	}
	if strings.TrimSpace(opts.Image) == "" {
		opts.Image = DefaultWorkerImage
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = DefaultWorkerModel
	}

	for {
		select {
		case <-ctx.Done():
			return 0
		default:
		}

		// Use the seat name (or bench name) as owner for the lease so it checks against shares.tsv,
		// and pid distinguishes the instance.
		leaseOwner := opts.Seat
		if leaseOwner == "" {
			leaseOwner = opts.Bench
		}
		now := time.Now().UTC()

		res, err := PullCard(opts.Store, leaseOwner, opts.For, now, os.Getpid())
		if errors.Is(err, ErrNoCard) {
			fmt.Fprintf(opts.Stdout, "PULL IDLE bench=%s cards=0\n", oneline.Field(opts.Bench))
			if opts.Once {
				return 0
			}
			// Wait before looking again, respecting context cancellation.
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(2 * time.Second):
				continue
			}
		}
		if err != nil {
			var ref *LeaseRefusal
			if errors.As(err, &ref) {
				fmt.Fprintf(opts.Stderr, "nova-swarm pull: no lease bench=%s card=%s held=%d share=%d free=%d holders=%s\n",
					oneline.Field(opts.Bench), oneline.Field(ref.Card), ref.Held, ref.Share, ref.Free, oneline.Escape(ref.Holders))
			} else {
				fmt.Fprintf(opts.Stderr, "nova-swarm pull: take error: %s\n", oneline.Err(err))
			}
			if opts.Once {
				return 2
			}
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// A card was taken under lease!
		label := strings.TrimSuffix(res.Card, CardExt)
		fmt.Fprintf(opts.Stderr, "PULL START bench=%s worker=%s card=%s lease=%s until=%s\n",
			oneline.Field(opts.Bench), oneline.Field(leaseOwner), oneline.Field(res.Card),
			oneline.Field(res.Lease), oneline.Field(res.Until.UTC().Format(time.RFC3339)))

		takenPath := filepath.Join(opts.Store, takenDirName, leaseOwner+"-"+res.Card)
		cardBytes, readErr := os.ReadFile(takenPath)
		if readErr != nil {
			fmt.Fprintf(opts.Stderr, "nova-swarm pull: cannot read card %s: %s\n", oneline.Field(takenPath), oneline.Err(readErr))
			_, _, _ = ReleaseSlotLeases(opts.Store, leaseOwner, res.Card, false)
			if opts.Once {
				return 2
			}
			continue
		}

		// Create isolated workspace for this card.
		workDir := filepath.Join(opts.Store, "work", label)
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			fmt.Fprintf(opts.Stderr, "nova-swarm pull: mkdir workdir: %s\n", oneline.Err(err))
			_, _, _ = ReleaseSlotLeases(opts.Store, leaseOwner, res.Card, false)
			if opts.Once {
				return 2
			}
			continue
		}
		_ = os.WriteFile(filepath.Join(workDir, "CARD.md"), cardBytes, 0o644)

		// Start heartbeat goroutine to renew lease while card runs.
		cardCtx, cardCancel := context.WithCancel(ctx)
		hbDone := make(chan struct{})
		go runHeartbeat(cardCtx, opts.Store, leaseOwner, res.Card, opts.For, hbDone, opts.Stderr)

		// Run card.
		var exitCode int
		var runErr error
		if opts.RunCard != nil {
			exitCode, runErr = opts.RunCard(cardCtx, label, takenPath, workDir)
		} else if strings.TrimSpace(opts.Runner) != "" {
			exitCode, runErr = executeRunner(cardCtx, opts.Runner, takenPath, workDir, label, opts.Stderr)
		} else {
			exitCode, runErr = executeContainer(cardCtx, opts, label, string(cardBytes), workDir)
		}

		// Stop heartbeat before releasing lease.
		cardCancel()
		<-hbDone

		// Terminal outcome storage BEFORE releasing lease (per Stella's audit).
		resultPath := filepath.Join(opts.Harvest, label, "RESULT.md")
		harvestResult(workDir, resultPath)

		// Move taken card to done/
		doneDir := filepath.Join(opts.Store, "done")
		if err := os.MkdirAll(doneDir, 0o755); err == nil {
			_ = os.Rename(takenPath, filepath.Join(doneDir, res.Card))
		} else {
			_ = os.Remove(takenPath)
		}

		// Release lease.
		_, _, _ = ReleaseSlotLeases(opts.Store, leaseOwner, res.Card, false)

		// Clean up workspace. The container is the hygiene.
		_ = safepath.RemoveUnder(opts.Store, workDir)

		summary := "RESULT: ok"
		if runErr != nil {
			summary = fmt.Sprintf("FAIL: %s", oneline.Err(runErr))
		} else if exitCode != 0 {
			summary = fmt.Sprintf("FAIL: rc=%d", exitCode)
		}

		fmt.Fprintf(opts.Stdout, "PULL DONE bench=%s worker=%s card=%s rc=%d result=%s\n",
			oneline.Field(opts.Bench), oneline.Field(leaseOwner), oneline.Field(res.Card),
			exitCode, oneline.Field(summary))

		if opts.Once {
			if runErr != nil || exitCode != 0 {
				return 1
			}
			return 0
		}
	}
}

// runHeartbeat periodically renews the slot lease until ctx is cancelled.
func runHeartbeat(ctx context.Context, store, owner, label string, dur time.Duration, done chan<- struct{}, stderr io.Writer) {
	defer close(done)
	interval := dur / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	if interval > 60*time.Second {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, until, err := RenewSlotLease(store, owner, label, dur, time.Now().UTC())
			if err != nil || renewed == 0 {
				fmt.Fprintf(stderr, "HEARTBEAT WARN owner=%s card=%s renewed=%d err=%v\n",
					owner, label, renewed, err)
			} else {
				fmt.Fprintf(stderr, "HEARTBEAT OK owner=%s card=%s until=%s\n",
					owner, label, until.Format(time.RFC3339))
			}
		}
	}
}

// executeRunner runs a custom runner command.
func executeRunner(ctx context.Context, runner, cardPath, workDir, label string, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", runner)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PULL_CARD="+cardPath,
		"PULL_WORK="+workDir,
		"PULL_LABEL="+label,
	)
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// executeContainer runs the card inside the toolchain container with nova-secrets exec.
func executeContainer(ctx context.Context, opts PullWorkerOptions, label, cardContent, workDir string) (int, error) {
	engine := opts.Container
	if engine == "" {
		if _, err := exec.LookPath("podman"); err == nil {
			engine = "podman"
		} else if _, err := exec.LookPath("docker"); err == nil {
			engine = "docker"
		} else {
			return 2, fmt.Errorf("neither podman nor docker found on PATH for container run; pass --runner <cmd> or install podman")
		}
	}

	containerArgs := BuildContainerArgs(engine, opts.Image, workDir, opts.Model, label, cardContent, "DEEPSEEK_API_KEY")
	var finalCmd string
	var finalArgs []string

	if strings.TrimSpace(opts.Seat) != "" {
		if _, err := exec.LookPath("nova-secrets"); err == nil {
			finalCmd = "nova-secrets"
			finalArgs = append([]string{"exec", "--as", opts.Seat, "--", engine}, containerArgs...)
		} else {
			finalCmd = engine
			finalArgs = containerArgs
		}
	} else {
		finalCmd = engine
		finalArgs = containerArgs
	}

	cmd := exec.CommandContext(ctx, finalCmd, finalArgs...)
	cmd.Dir = workDir
	cmd.Stdout = opts.Stderr
	cmd.Stderr = opts.Stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// harvestResult finds RESULT.md in the workdir and copies it to harvestPath.
func harvestResult(workDir, harvestPath string) {
	candidates := []string{
		filepath.Join(workDir, "RESULT.md"),
		filepath.Join(workDir, "repo", "RESULT.md"),
	}
	under, _ := filepath.Glob(filepath.Join(workDir, "repo", "*", "RESULT.md"))
	candidates = append(candidates, under...)

	for _, cand := range candidates {
		data, err := os.ReadFile(cand)
		if err == nil && len(data) > 0 {
			_ = os.MkdirAll(filepath.Dir(harvestPath), 0o755)
			_ = os.WriteFile(harvestPath, data, 0o644)
			return
		}
	}
}
