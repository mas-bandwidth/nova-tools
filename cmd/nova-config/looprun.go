package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// loop run: a loop's command under its single-instance lock (internal/config/looprun.go,
// docs/SPEC-CONFIG.md "loop run"). The verb opens the store only when no command follows
// `--`; the lock, the start count and the metrics are files on this machine.

// loopRunEffect is what loop run -h states.
const loopRunEffect = "process: takes <run-dir>/<name>.lock (a second copy exits 3), counts the start in <run-dir>/<name>.starts, writes the restart metrics to --metrics when given, then runs the loop's command (its row's argv, or the command after --) and exits with its exit code; it opens the store only when no command follows --"

// loopRunMore is loop run's own help below the effect.
const loopRunMore = "a unit runs it in place of the bash nova-loop: ExecStart=nova-config loop run <name> -- <the unit's command>; a lock whose holder died is taken (the kernel released it); SIGINT and SIGTERM are passed to the command\nexit codes: the command's own; 1 the row is not runnable (missing, disabled, with secrets); 2 usage, or the command did not start; 3 another copy holds the lock\n"

// runLoop starts argv with this process's stdin and the given output, passes SIGINT
// and SIGTERM on to it, and is its exit code once it ends.
type runLoop func(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error)

// startLoop is the real runLoop: one long-lived child under the caller's context.
func startLoop(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := subproc.Long(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case s := <-sigs:
			_ = cmd.Process.Signal(s) // ignored: a command already gone has nothing to stop, and its wait says how it ended
		case err := <-done:
			if cmd.ProcessState == nil {
				return 0, err
			}
			if code := cmd.ProcessState.ExitCode(); code >= 0 {
				return code, nil
			}
			return 1, nil // ended by a signal
		}
	}
}

func runLoopRun(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "loop run"
	var command []string
	dashed := false
	for i, a := range args {
		if a == "--" {
			args, command, dashed = args[:i], args[i+1:], true
			break
		}
	}
	fs := verbflag.New(verb)
	c := storeFlags(fs)
	runDir := fs.String("run-dir", "~/nova-bench/run", "the `dir` of the loop's lock (<name>.lock) and start count (<name>.starts)")
	metrics := fs.String("metrics", "", "node_exporter's textfile `dir`: nova_loop_<name>.prom is written there at each start; empty writes none")
	name, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, rest = args[0], args[1:]
	}
	if code, ok := parse(fs, rest, stderr, verb); !ok {
		return code
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return refuse(stderr, verb, "want loop run <name> [-- <command> ...]; flags follow the name")
	}
	if err := config.ValidateName(name); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if dashed && len(command) == 0 {
		return refuse(stderr, verb, "-- names no command")
	}
	if d.runLoop == nil {
		return refuse(stderr, verb, "this build runs no command")
	}
	argv := command
	if len(argv) == 0 {
		dsn, err := c.dsn(d.getenv)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		st, err := d.openStore(ctx, dsn)
		if err != nil {
			return refuse(stderr, verb, err.Error())
		}
		row, found, err := st.Get(ctx, config.KindLoop, name)
		_ = st.Close() // ignored: the row is read, and the command runs with no store open
		switch {
		case err != nil:
			return refuse(stderr, verb, err.Error())
		case !found:
			return refused(stderr, verb, "loop "+name+" not found", toolName+" loop list"+c.again())
		}
		if argv, err = config.LoopRunArgv(row); err != nil {
			what, next, _ := strings.Cut(strings.TrimPrefix(err.Error(), config.ErrLoopNotRunnable.Error()+": "), "; run: ")
			return refused(stderr, verb, what, next)
		}
	}
	dir := homeTilde(*runDir, d.getenv)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return refuse(stderr, verb, "the run directory: "+err.Error())
	}
	lock, err := filelock.TryLock(config.LoopLockPath(dir, name), "nova-config loop run "+name)
	if err != nil {
		if held, ok := filelock.AsHeldError(err); ok {
			fmt.Fprintf(stderr, "%s %s REFUSED: loop %s runs already (%s); run: %s loop show %s\n", toolName, verb, name, held.Holder, toolName, name)
			return config.LoopRunExitHeld
		}
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = lock.Unlock() }()                     // ignored: the kernel releases the lock when this process ends in any case
	prev, _ := os.ReadFile(config.LoopStartsPath(dir, name)) // an absent or unreadable count starts again at 1
	starts := config.NextLoopStarts(prev)
	if err := atomicfile.WriteFile(config.LoopStartsPath(dir, name), []byte(fmt.Sprintf("%d\n", starts)), 0o644); err != nil {
		return refuse(stderr, verb, "the start count: "+err.Error())
	}
	if *metrics != "" {
		mdir := homeTilde(*metrics, d.getenv)
		if err := os.MkdirAll(mdir, 0o755); err != nil {
			return refuse(stderr, verb, "the metrics directory: "+err.Error())
		}
		if err := atomicfile.WriteFile(config.LoopMetricsPath(mdir, name), []byte(config.LoopMetrics(name, starts, d.now())), 0o644); err != nil {
			return refuse(stderr, verb, "the metrics: "+err.Error())
		}
	}
	words, _ := json.Marshal(argv) // ignored: a slice of strings always encodes
	fmt.Fprintf(stdout, "LOOP RUN name=%s starts=%d argv=%s\n", name, starts, config.Value(string(words)))
	code, err := d.runLoop(ctx, argv, stdout, stderr)
	if err != nil {
		return refuse(stderr, verb, "the command did not start: "+err.Error())
	}
	fmt.Fprintf(stdout, "LOOP DONE name=%s exit=%d\n", name, code)
	return code
}

// homeTilde expands a leading ~/ with HOME.
func homeTilde(p string, getenv func(string) string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home := getenv("HOME"); home != "" {
			return filepath.Join(home, rest)
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
