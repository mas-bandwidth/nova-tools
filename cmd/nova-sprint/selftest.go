package main

// selftest land and server switch (docs/SPEC-SPRINT.md section 14, switching the
// server's binary): the canned card landed for real by this binary on a scratch clone,
// and the server's binary replaced by a new build only after that build's selftest is
// green, the previous one kept and put back when a land fails in the window after.
// internal/sprint holds the steps (selftest.go, switch.go); this file gives them this
// binary's verbs, git, the restart command and the server's log.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// selftestBudget bounds one selftest land of a new build, run by server switch.
const selftestBudget = 10 * time.Minute

// restartBudget bounds the server's restart command.
const restartBudget = 2 * time.Minute

func init() {
	verbExit["selftest land"] = "exit codes: 0 SELFTEST OK (the canned card landed), 1 SELFTEST FAILED (a line of the flow failed, or the base does not hold the landing: this build's lander is broken; do not install it), 2 usage or a directory it cannot run in"
	verbExit["server switch"] = "exit codes: 0 SWITCH OK (the new binary serves; --rollback: the kept one does), 1 refused with nothing changed (the new build's selftest is red, a path is wrong) or rolled back (a land failed in the window, the kept binary is back), 2 usage, 3 the rollback failed: the installed binary is in doubt, put the kept one back by hand"
	verbEffect["selftest land"] = "local write: a twin file, a bare repository and a clone in a scratch directory, removed when green; never the sprint's store, never a remote"
	verbEffect["server switch"] = "local write: the installed binary and the kept one, and the restart command run; --dry-run reads nothing and runs nothing"
}

func (a *app) cmdSelftestLand(args []string, stdout, stderr io.Writer) int {
	const name = "selftest land"
	fs, c := selftestFlags(name)
	dir := fs.String("dir", "", "an empty directory to run in, kept after (default: a new directory under nova-sprint/selftest in the user cache directory, removed when green and kept, named on its line, when red)")
	pos, err := parse(fs, args)
	switch {
	case err != nil:
		return refuse(stderr, name, err.Error())
	case len(pos) > 0:
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	// the scratch directory: --dir, kept; else a new one under the selftest root, removed
	// when green through safepath and kept to be read when red
	run, root := *dir, ""
	if run == "" {
		var err error
		if root, err = a.selftestRoot(); err == nil {
			err = os.MkdirAll(root, 0o700)
		}
		if err == nil {
			run, err = os.MkdirTemp(root, "run-")
		}
		if err != nil {
			return refuse(stderr, name, "no scratch directory: "+err.Error()+"; give one with --dir <empty dir>")
		}
	} else if entries, err := os.ReadDir(run); err != nil || len(entries) > 0 {
		return refuse(stderr, name, "--dir wants an empty directory to run in, and "+oneline.Escape(run)+" is not one")
	}
	if abs, err := filepath.Abs(run); err == nil {
		run = abs
	}
	start := a.now()
	res := sprint.Selftest{Dir: run, Git: sprint.SelftestGit(run), Verb: a.selftestVerb(run)}.Land(context.Background())
	took := a.now().Sub(start).Round(time.Millisecond)
	facts := map[string]any{"card": sprint.SelftestCard, "base": sprint.SelftestBase, "dir": res.Dir, "head": res.Head, "tip": res.Tip, "took": took.String()}
	if res.OK {
		if root != "" {
			facts["dir"] = "-"
			if err := safepath.RemoveUnder(root, res.Dir); err != nil {
				fmt.Fprintf(stderr, "NOTE the scratch directory %s was not removed: %s\n", oneline.Field(res.Dir), oneline.Escape(err.Error()))
			}
		}
		sayOK(stdout, c.json, name, fmt.Sprintf("SELFTEST OK land card=%s base=%s head=%s tip=%s took=%s", sprint.SelftestCard, sprint.SelftestBase, res.Head, res.Tip, took), facts)
		return 0
	}
	step := "check"
	if res.Step > 0 {
		step = fmt.Sprint(res.Step)
	}
	why := res.Why
	if res.Line != "" {
		why = oneline.Quote(res.Line) + ": " + why
	}
	line := fmt.Sprintf("SELFTEST FAILED land step=%s dir=%s: %s; this build's lander is broken, do not install it; run: ls %s", step, oneline.Field(res.Dir), oneline.Escape(why), oneline.Field(res.Dir))
	if c.json {
		facts["status"], facts["exit"], facts["step"], facts["why"], facts["kept"] = "failed", 1, step, why, true
		// ignored: a map of strings and numbers always encodes
		b, _ := json.Marshal(mergeFacts(map[string]any{"verb": name}, facts))
		fmt.Fprintln(stdout, string(b))
		return 1
	}
	fmt.Fprintln(stderr, line)
	return 1
}

// mergeFacts is to with every fact of from.
func mergeFacts(to, from map[string]any) map[string]any {
	for k, v := range from {
		to[k] = v
	}
	return to
}

// selftestVerb runs one verb of this binary for the flow in dir, on the twin there, as
// a fresh process would: its own app, the actor selftest, git in the flow's
// environment and land's clones under dir (docs/SPEC-SPRINT.md section 14).
func (a *app) selftestVerb(dir string) func(ctx context.Context, args []string) (string, int) {
	env := map[string]string{"NOVA_SPRINT_REDIS": sprint.SelftestTwin(dir), "NOVA_SPRINT_ACTOR": "selftest"}
	return func(_ context.Context, args []string) (string, int) {
		v := newApp(func(k string) string { return env[k] })
		v.gitEnv = sprint.SelftestEnv(dir)
		v.landRoot = func() (string, error) { return filepath.Join(dir, "land"), nil }
		var out bytes.Buffer
		code := v.run(args, &out, &out)
		v.close()
		return out.String(), code
	}
}

func (a *app) cmdServerSwitch(args []string, stdout, stderr io.Writer) int {
	const name = "server switch"
	fs, c := selftestFlags(name)
	install := fs.String("install", "", "the installed binary the server's supervisor runs (a link is followed to the file it names)")
	keep := fs.String("keep", "", "where the previous binary is kept (default: the installed path and .prev)")
	logf := fs.String("log", "", "the server's log, the file its supervisor writes run --land's output to: its LAND lines are watched for the window")
	restart := fs.String("restart", "", "a command, run by sh -c, that restarts the server (bounded to 2m); none: the run loop stops on its replaced binary and its supervisor starts the new one")
	window := fs.Duration("window", 10*time.Minute, "how long after the switch a failed land rolls it back")
	every := fs.Duration("every", 5*time.Second, "how often the log is read in the window")
	rollback := fs.Bool("rollback", false, "put the kept binary back in the installed one's place and restart; takes no binary")
	dry := fs.Bool("dry-run", false, "print the switch it would make and change nothing: reads nothing, runs no selftest and no restart")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	var bad []string
	if *install == "" {
		bad = append(bad, "--install wants the installed binary's path")
	}
	switch {
	case *rollback && len(pos) > 0:
		bad = append(bad, "--rollback takes no binary, found "+oneline.Escape(pos[0]))
	case !*rollback && len(pos) != 1:
		bad = append(bad, "wants one word, the new build's path")
	}
	if !*rollback && *logf == "" {
		bad = append(bad, "--log wants the server's log, whose LAND lines the window watches")
	}
	if *window <= 0 || *every <= 0 {
		bad = append(bad, "--window and --every want durations above zero")
	}
	if len(bad) > 0 {
		return refuse(stderr, name, strings.Join(bad, "; "))
	}
	if *keep == "" {
		*keep = *install + ".prev"
	}
	sw := sprint.Switch{Install: *install, Keep: *keep, Window: *window, Every: *every, Now: a.now,
		Sleep: func(ctx context.Context, d time.Duration) error { a.sleep(d); return ctx.Err() }}
	if *restart != "" {
		sw.Restart = func(ctx context.Context) error { return a.restartServer(ctx, *restart) }
	}
	if !*rollback {
		sw.Binary = pos[0]
		sw.Selftest = a.selftestBinary
	}
	facts := map[string]any{"install": *install, "keep": *keep, "binary": sw.Binary, "rollback": *rollback, "restart": *restart, "window": window.String(), "log": *logf}
	if *dry {
		facts["dry_run"] = true
		what := "switch to " + oneline.Field(sw.Binary) + " after its selftest land, keeping the installed binary"
		if *rollback {
			what = "put the kept binary back"
		}
		sayOK(stdout, c.json, name, fmt.Sprintf("SWITCH OK dry_run=yes install=%s keep=%s restart=%s window=%s: would %s; nothing was run, kept, installed or restarted",
			oneline.Field(*install), oneline.Field(*keep), oneline.Field(dashed(*restart)), window, what), facts)
		return 0
	}
	ctx := context.Background()
	var res sprint.SwitchResult
	if *rollback {
		res = sw.Rollback(ctx)
	} else {
		tail, err := logTail(*logf)
		if err != nil {
			return refuse(stderr, name, "--log wants the server's log file, readable: "+err.Error())
		}
		sw.Lines = tail
		res = sw.Run(ctx)
	}
	facts["status"], facts["why"], facts["land"], facts["landed"], facts["restarts"] = res.Status, res.Why, res.Land, res.Landed, res.Restarts
	code, line := switchLine(res, *rollback, *install, *keep)
	if c.json {
		facts["exit"] = code
		// ignored: a map of strings, numbers and booleans always encodes
		b, _ := json.Marshal(mergeFacts(map[string]any{"verb": name}, facts))
		fmt.Fprintln(stdout, string(b))
		return code
	}
	w := stdout
	if code != 0 {
		w = stderr
	}
	fmt.Fprintln(w, line)
	if res.Land != "" {
		fmt.Fprintf(w, "NOTE the land line: %s\n", oneline.Escape(res.Land))
	}
	return code
}

// switchLine is a switch's (or a rollback's) exit code and its line.
func switchLine(res sprint.SwitchResult, rollback bool, install, keep string) (int, string) {
	at := fmt.Sprintf("install=%s keep=%s restarts=%d", oneline.Field(install), oneline.Field(keep), res.Restarts)
	switch {
	case res.Status == sprint.SwitchOK:
		confirmed := "quiet"
		if res.Landed {
			confirmed = "landed"
		}
		return 0, "SWITCH OK " + at + " confirmed=" + confirmed
	case rollback && res.Status == sprint.SwitchRolledBack:
		return 0, "SWITCH OK rolled_back=yes " + at
	case res.Status == sprint.SwitchRefused:
		return 1, "SWITCH REFUSED: " + oneline.Escape(res.Why) + "; nothing was changed; run: nova-sprint server switch -h"
	case res.Status == sprint.SwitchRolledBack:
		return 1, "SWITCH FAILED rolled_back=yes " + at + ": " + oneline.Escape(res.Why) + "; the kept binary serves; run: nova-sprint selftest land"
	}
	return 3, "SWITCH FAILED rolled_back=no " + at + ": " + oneline.Escape(res.Why) + "; the installed binary is in doubt; run: cp " + oneline.Field(keep) + " " + oneline.Field(install)
}

// selftestBinary runs `<binary> selftest land`, bounded: nil when it is green, else
// what it said last.
func (a *app) selftestBinary(ctx context.Context, binary string) error {
	if a.selftestRun != nil {
		return a.selftestRun(ctx, binary)
	}
	cmd, cancel := subproc.CommandFor(ctx, selftestBudget, binary, "selftest", "land")
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s selftest land: %w: %s", binary, err, tailLine(string(out)))
	}
	return nil
}

// restartServer runs the restart command by sh -c, bounded.
func (a *app) restartServer(ctx context.Context, command string) error {
	if a.restart != nil {
		return a.restart(ctx, command)
	}
	cmd, cancel := subproc.CommandFor(ctx, restartBudget, "sh", "-c", command)
	defer cancel()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", command, err, tailLine(string(out)))
	}
	return nil
}

// tailLine is the last line of out that holds anything.
func tailLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// logTail reads the log from its end now on: each call the whole lines written since
// the last, a log cut or rotated (shorter than read) read again from its start.
func logTail(path string) (func() ([]string, error), error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	at, partial := fi.Size(), ""
	return func() ([]string, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() {
			// ignored: a file opened to read, whose lines are already read
			_ = f.Close()
		}()
		fi, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if fi.Size() < at {
			at, partial = 0, ""
		}
		if _, err := f.Seek(at, io.SeekStart); err != nil {
			return nil, err
		}
		var lines []string
		r := bufio.NewReader(f)
		for {
			s, err := r.ReadString('\n')
			at += int64(len(s))
			if err != nil {
				partial += s
				break
			}
			lines = append(lines, strings.TrimRight(partial+s, "\r\n"))
			partial = ""
		}
		return lines, nil
	}, nil
}

// selftestFlags is the flag set of selftest land and server switch, which touch no
// store: --json alone of the shared flags.
func selftestFlags(name string) (flagSet, *common) {
	fs := verbflag.New(name)
	c := &common{verb: name, epoch: -1}
	fs.BoolVar(&c.json, "json", false, "print one JSON object for a program instead of the lines")
	return fs, c
}

// selftestRoot is where selftest land makes its scratch directories when it is given no
// --dir: nova-sprint/selftest beside land's clones in the user's cache directory.
func (a *app) selftestRoot() (string, error) {
	land, err := a.landRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(land), "selftest"), nil
}
