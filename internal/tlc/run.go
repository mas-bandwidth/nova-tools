package tlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// Run is one TLC invocation: the java flags, the TLC flags and the module.
type Run struct {
	Java string   // the java program
	Jar  string   // the TLC jar
	Dir  string   // working directory: the private copy of the models
	JVM  []string // JVM flags before -cp, for example -Xmx2g

	TmpDir       string // java.io.tmpdir, when not empty: TLC unpacks its standard modules there
	Workers      int    // TLC -workers
	LnCheckFinal bool   // -lncheck final: check liveness once at the end
	NoDeadlock   bool   // -deadlock: switch TLC's deadlock check OFF
	MetaDir      string // -metadir, when not empty: TLC's state files
	Config       string // -config
	Module       string // the .tla file
}

// Args is the argument list after the java program. The order is fixed: JVM
// flags, the classpath and TLC's main class, then TLC's own flags, and last
// the config and module.
func (r Run) Args() []string {
	args := append([]string(nil), r.JVM...)
	if r.TmpDir != "" {
		args = append(args, "-Djava.io.tmpdir="+r.TmpDir)
	}
	args = append(args, "-cp", r.Jar, "tlc2.TLC")
	if r.LnCheckFinal {
		args = append(args, "-lncheck", "final")
	}
	args = append(args, "-workers", strconv.Itoa(r.Workers))
	if r.NoDeadlock {
		args = append(args, "-deadlock")
	}
	if r.MetaDir != "" {
		args = append(args, "-metadir", r.MetaDir)
	}
	return append(args, "-config", r.Config, r.Module)
}

// Executor runs TLC and leaves its combined output in the log file. It returns
// the exit status: ExitTimeout when the context ended first, ExitNoStart when
// the program could not be started. Its note is text already written to the
// log. Suites take an Executor so their bookkeeping is testable without java.
type Executor func(ctx context.Context, r Run, log string) int

// Execute is the Executor that runs java. The output goes to the log file, one
// stream for both of TLC's, and the run ends when the context does: a run that
// outlives its budget is killed and reported as ExitTimeout, never as a result.
func Execute(ctx context.Context, r Run, log string) int {
	f, err := os.Create(log)
	if err != nil {
		return ExitNoStart
	}
	defer f.Close()
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(f, "TLC suite budget exhausted before starting this case")
		return ExitTimeout
	}
	cmd := exec.CommandContext(ctx, r.Java, r.Args()...)
	cmd.Dir = r.Dir
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	if err == nil {
		return ExitPass
	}
	if ctx.Err() != nil {
		fmt.Fprintln(f, "TLC suite budget exhausted during this case")
		return ExitTimeout
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	fmt.Fprintln(f, err.Error())
	return ExitNoStart
}
