package main

import (
	"context"
	stdflag "flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// loopHold is the single-instance lock. release drops it. clear keeps the
// descriptor open across the exec that replaces this process.
type loopHold struct {
	release func()
	clear   func() error
}

// runLoop is `nova-config loop run`: one lock, the restart count, then the
// loop row's command in this process. --dry-run prints the command and
// writes nothing.
func runLoop(ctx context.Context, fs *stdflag.FlagSet, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "loop run"
	c := storeFlags(fs)
	runDirFlag := fs.String("run-dir", "", "the `dir` of the lock, the restart count and the default metrics file; empty is ~/nova-bench/run; the directory must already exist")
	stopFile := fs.String("stop-file", "", "a marker `file`; stop=1 exits 3 and does not exec; a missing file is not a stop")
	metrics := fs.String("metrics", "", "the restart textfile, a `file`; empty is <run-dir>/<name>.prom")
	dry := fs.Bool("dry-run", false, "print the command and write nothing")
	// The name comes before the flags. Flag parsing stops at the first word
	// that is not a flag, so the name is taken off before the flags are read.
	name, rest := "", args
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		name, rest = args[0], args[1:]
	}
	if code, ok := parse(fs, rest, stderr, verb); !ok {
		return code
	}
	if name == "" {
		if fs.NArg() != 1 {
			return refuse(stderr, verb, "want loop run <name>")
		}
		name = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return refuse(stderr, verb, "want loop run <name>")
	}
	if err := config.ValidateName(name); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: the row is already read; a close error changes nothing the exec needs
	k, _ := config.Lookup(config.KindLoop)
	if laterKind(k) {
		if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
			return code
		}
	}
	row, found, err := st.Get(ctx, config.KindLoop, name)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if !found {
		return refuse(stderr, verb, "no loop "+oneline.Field(name))
	}
	if row.Fields["enabled"] == "false" {
		return refuse(stderr, verb, "loop "+oneline.Field(name)+" is disabled")
	}
	argv := config.Argv(row.Fields["argv"])
	if len(argv) == 0 || argv[0] == "" {
		return refuse(stderr, verb, "loop "+oneline.Field(name)+" has no command")
	}
	if *stopFile != "" {
		raw, err := os.ReadFile(*stopFile)
		if err != nil && !os.IsNotExist(err) {
			return refuse(stderr, verb, "--stop-file "+oneline.Field(*stopFile)+" could not be read")
		}
		if config.LoopMarkerStops(string(raw)) {
			fmt.Fprintf(stderr, "nova-config loop run REFUSED: stop=1 in %s; run: nova-config loop run -h\n", oneline.Field(*stopFile))
			return 3
		}
	}
	if *dry {
		fmt.Fprintf(stdout, "LOOP DRY-RUN name=%s words=%d\n", oneline.Field(name), len(argv))
		for i, w := range argv {
			fmt.Fprintf(stdout, "LOOP ARG %d %s\n", i, oneline.Field(w))
		}
		return 0
	}
	if runtime.GOOS == "windows" {
		return refuse(stderr, verb, "a Windows machine is a client and never a bench: loop run is refused")
	}
	if d.exec == nil {
		return refuse(stderr, verb, "loop run has no process to exec into")
	}
	runDir := *runDirFlag
	if runDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return refuse(stderr, verb, "the user's home could not be read")
		}
		runDir = filepath.Join(home, "nova-bench", "run")
	}
	stDir, err := os.Lstat(runDir)
	if err != nil || !stDir.IsDir() {
		return refuse(stderr, verb, "--run-dir "+oneline.Field(runDir)+" is not a directory")
	}
	lockFn := d.lock
	if lockFn == nil {
		lockFn = platformLoopLock
	}
	hold, held, err := lockFn(filepath.Join(runDir, name+".lock"))
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if !held {
		if hold.release != nil {
			hold.release()
		}
		fmt.Fprintf(stderr, "nova-config loop run REFUSED: %s holds a live lock; run: nova-config loop run -h\n", oneline.Field(name))
		return 3
	}
	if err := writeLoopMetrics(runDir, name, *metrics); err != nil {
		if hold.release != nil {
			hold.release()
		}
		return refuse(stderr, verb, err.Error())
	}
	if hold.clear != nil {
		if err := hold.clear(); err != nil {
			if hold.release != nil {
				hold.release()
			}
			return refuse(stderr, verb, err.Error())
		}
	}
	if err := d.exec(argv); err != nil {
		if hold.release != nil {
			hold.release()
		}
		return refuse(stderr, verb, err.Error())
	}
	if hold.release != nil {
		hold.release() // a fake exec returned; the real one does not
	}
	return 0
}

// writeLoopMetrics records the next restart count and the textfile. The
// parent directory is the run directory, which the caller already checked.
func writeLoopMetrics(runDir, name, metrics string) error {
	counter := filepath.Join(runDir, name+".restarts")
	prev, err := os.ReadFile(counter)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("the restart count %s could not be read", oneline.Field(counter))
	}
	n, err := config.NextRestart(string(prev))
	if err != nil {
		return err
	}
	if err := writeLoopFile(counter, fmt.Sprintf("%d\n", n)); err != nil {
		return err
	}
	prom := metrics
	if prom == "" {
		prom = filepath.Join(runDir, name+".prom")
	}
	return writeLoopFile(prom, config.RestartProm(name, n))
}

// writeLoopFile refuses a symlink and writes text. It does not create a
// parent directory.
func writeLoopFile(path, text string) error {
	parent := filepath.Dir(path)
	st, err := os.Lstat(parent)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s has no directory to write in", oneline.Field(path))
	}
	if cur, err := os.Lstat(path); err == nil && cur.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", oneline.Field(path))
	}
	return os.WriteFile(path, []byte(text), 0o644)
}
