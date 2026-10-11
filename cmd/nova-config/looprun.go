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

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// loop run: a loop's command under its single-instance lock (pkg/config/looprun.go,
// docs/SPEC-CONFIG.md "loop run"). The verb opens the store only when no command follows
// `--`; the lock, the start count and the metrics are files on this machine.
//
// loop run is a verb on pkg/tool: its flags are the skeleton's (tool.Flags), so
// the no-hand-printing ledger records no verbflag.New site for it. The name may stand
// before the flags and the command follows `--`, which the skeleton's parser does not
// read, so runLoopRun takes those two words off the arguments and hands the flags to
// the verb's own tool.Tool.

// loopRunEffect is what loop run -h states.
const loopRunEffect = "local write: takes <run-dir>/<name>.lock (a second copy exits 3), counts the start in <run-dir>/<name>.starts, writes the restart metrics to --metrics when given, then runs the loop's command (its row's argv, or the command after --) and exits with the command's exit code, 128+N when a signal ended it; it opens the store only when no command follows --; --dry-run reads the row and the start count, prints the LOOP RUN line it would print, and takes no lock, writes nothing and runs nothing"

// loopRunMore is loop run's own help below the effect.
const loopRunMore = "a unit runs it in place of the bash nova-loop: ExecStart=nova-config loop run <name> -- <the unit's command>; a lock whose holder died is taken (the kernel released it); the lock lives in this process, so a wrapper killed outright ends the command with it on linux (the kernel's parent-death signal) and a restarted unit never runs a second command beside the first; SIGINT and SIGTERM are passed to the command\nexit codes: the command's own, 128+N when a signal ended it; 1 the row is not runnable (missing, disabled, with secrets); 2 usage, or the command did not start; 3 another copy holds the lock\n"

// runLoop starts argv with this process's stdin and the given output, passes SIGINT
// and SIGTERM on to it, and is its status once it ends.
type runLoop func(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error)

// startLoop is the real runLoop: one long-lived child that outlives the caller's
// interrupt but not the caller itself. pkg/tool's RunContext cancels the
// context it hands a verb on SIGINT, and loop run passes SIGINT and SIGTERM to
// the command instead of letting that cancellation kill it, so the child runs
// under the context's values with the cancellation removed. The lock lives in
// this process (loopRunCall), so a command that outlived a killed wrapper would
// run beside the restarted wrapper's command; loopChildAttr ties the command's
// life to this process instead, so the wrapper's death ends the command. A
// signal death is env(1)'s 128+N, never a made-up status: the unit and the
// shell around it read it as the bash nova-loop wrapper's exec left it.
func startLoop(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := subproc.Long(context.WithoutCancel(ctx), argv[0], argv[1:]...)
	if attr := loopChildAttr(); attr != nil {
		cmd.SysProcAttr = attr
	}
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
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal()), nil
			}
			if code := cmd.ProcessState.ExitCode(); code >= 0 {
				return code, nil
			}
			return 0, fmt.Errorf("the command ended with no exit status")
		}
	}
}

// runLoopRun is `nova-config loop run <name> [flags] [-- <command> ...]`. The name
// stands before the flags and the command follows `--`, neither of which the skeleton
// parses, so they are read here and the rest goes to the verb's own tool.Tool (its
// flags are tool.Flags, so the no-hand-printing ledger gains no site).
func runLoopRun(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	var command []string
	dashed := false
	for i, a := range args {
		if a == "--" {
			args, command, dashed = args[:i], args[i+1:], true
			break
		}
	}
	name, rest := "", args
	if len(args) > 0 && args[0] != "" && !strings.HasPrefix(args[0], "-") {
		name, rest = args[0], args[1:]
	}
	return loopRunTool(d, name, command, dashed).RunContext(ctx, append([]string{"loop", "run"}, rest...), os.Stdin, stdout, stderr)
}

// loopRunTool is the one verb loop run, so its -h, its flags and its refusals come
// from pkg/tool (the tool that owns the fleet's configuration).
func loopRunTool(d deps, name string, command []string, dashed bool) *tool.Tool {
	return &tool.Tool{
		Name:      toolName,
		What:      "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis",
		How:       "loop run holds the loop's lock, counts the start and runs the loop's command.\nThe lock, the start count and the metrics are files on this machine.",
		ExitTable: "the command's own, 128+N when a signal ended it; 1 the row is not runnable; 2 usage, or the command did not start; 3 another copy holds the lock",
		Verbs:     []tool.Verb{loopRunVerb(d, name, command, dashed)},
	}
}

func loopRunVerb(d deps, name string, command []string, dashed bool) tool.Verb {
	var c conn
	return tool.Verb{
		Name:      "loop run",
		Usage:     "loop run <name> [--run-dir <dir>] [--metrics <dir>] [-- <command> ...]",
		Effect:    tool.Effect(loopRunEffect),
		Detail:    loopRunMore + "example: " + toolExamples["loop run"],
		ExitTable: "the command's own, 128+N when a signal ended it; 1 the row is not runnable (missing, disabled, with secrets); 2 usage, or the command did not start; 3 another copy holds the lock",
		DryRun:    true,
		Flags: func(f *tool.Flags) {
			f.Prints()
			c = storeFlags(f.FlagSet)
			f.String("run-dir", "~/nova-bench/run", "the `dir` of the loop's lock (<name>.lock) and start count (<name>.starts)")
			f.String("metrics", "", "node_exporter's textfile `dir`: nova_loop_<name>.prom is written there at each start; empty writes none")
		},
		Run: func(call *tool.Call) *tool.Out { return loopRunCall(call, d, c, name, command, dashed) },
	}
}

// loopRefused is a refusal the verb ran and named: exit 1, the same status word and
// exit the hand verb printed.
func loopRefused(why, next string) *tool.Out {
	o := tool.Refuse(why)
	o.Exit = 1
	o.Remedy = next
	return o
}

// loopRunCall is loop run once the skeleton has parsed its flags.
func loopRunCall(call *tool.Call, d deps, c conn, name string, command []string, dashed bool) *tool.Out {
	dry := call.DryRun()
	if name == "" {
		return tool.Refuse("the name is required: loop run <name> [-- <command> ...]")
	}
	if err := config.ValidateName(name); err != nil {
		return tool.Refuse(err.Error())
	}
	if dashed && len(command) == 0 {
		return tool.Refuse("-- names no command")
	}
	if d.runLoop == nil {
		return tool.Refuse("this build runs no command")
	}
	argv := command
	if len(argv) == 0 {
		dsn, err := c.dsn(d.getenv)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		st, err := d.openStore(call.Ctx, dsn)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		row, found, err := st.Get(call.Ctx, config.KindLoop, name)
		_ = st.Close() // ignored: the row is read, and the command runs with no store open
		switch {
		case err != nil:
			return tool.Refuse(err.Error())
		case !found:
			return loopRefused("loop "+name+" not found", toolName+" loop list"+c.again())
		}
		if argv, err = config.LoopRunArgv(row); err != nil {
			what, next, _ := strings.Cut(strings.TrimPrefix(err.Error(), config.ErrLoopNotRunnable.Error()+": "), "; run: ")
			return loopRefused(what, next)
		}
	}
	dir := homeTilde(call.Str("run-dir"), d.getenv)
	if dry {
		// the line the real run prints first, from the same row and count, and nothing taken, written or run
		prev, _ := os.ReadFile(config.LoopStartsPath(dir, name)) // an absent or unreadable count starts again at 1
		words, _ := json.Marshal(argv)                           // ignored: a slice of strings always encodes
		fmt.Fprintf(call.Stdout, "LOOP RUN name=%s starts=%d argv=%s dry_run=true; no lock taken, nothing written or run\n",
			name, config.NextLoopStarts(prev), config.Value(string(words)))
		return tool.Exit(0)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return tool.Refuse("the run directory: " + err.Error())
	}
	lock, err := filelock.TryLock(config.LoopLockPath(dir, name), "nova-config loop run "+name)
	if err != nil {
		if held, ok := filelock.AsHeldError(err); ok {
			o := tool.Refuse("loop " + name + " runs already (" + held.Holder.String() + ")")
			o.Exit = config.LoopRunExitHeld
			o.Remedy = toolName + " loop show " + name
			return o
		}
		return tool.Refuse(err.Error())
	}
	defer func() { _ = lock.Unlock() }()                     // ignored: the kernel releases the lock when this process ends in any case
	prev, _ := os.ReadFile(config.LoopStartsPath(dir, name)) // an absent or unreadable count starts again at 1
	starts := config.NextLoopStarts(prev)
	if err := atomicfile.WriteFile(config.LoopStartsPath(dir, name), []byte(fmt.Sprintf("%d\n", starts)), 0o644); err != nil {
		return tool.Refuse("the start count: " + err.Error())
	}
	if metrics := call.Str("metrics"); metrics != "" {
		mdir := homeTilde(metrics, d.getenv)
		if err := os.MkdirAll(mdir, 0o755); err != nil {
			return tool.Refuse("the metrics directory: " + err.Error())
		}
		if err := atomicfile.WriteFile(config.LoopMetricsPath(mdir, name), []byte(config.LoopMetrics(name, starts, d.now())), 0o644); err != nil {
			return tool.Refuse("the metrics: " + err.Error())
		}
	}
	words, _ := json.Marshal(argv) // ignored: a slice of strings always encodes
	fmt.Fprintf(call.Stdout, "LOOP RUN name=%s starts=%d argv=%s\n", name, starts, config.Value(string(words)))
	code, err := d.runLoop(call.Ctx, argv, call.Stdout, call.Stderr)
	if err != nil {
		return tool.Refuse("the command did not start: " + err.Error())
	}
	fmt.Fprintf(call.Stdout, "LOOP DONE name=%s exit=%d\n", name, code)
	return tool.Exit(code)
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
