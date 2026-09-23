package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// `nova-pulse sprint evaluate` is the production caller of sprint.RecordAcceptance
// (#2684). It runs `nova-work set check --file <work set> --evaluate` -- the live
// acceptance evaluation of #2664 -- and stores its SET OK / SET DONE on sprint:<name>,
// so done, units and percent follow the evaluated units and not closed tasks. Without
// --every it is one pass; with --every it is the reconciler, one pass per interval
// (--rounds bounds it, 0 runs until killed), so a task close that satisfies a unit is
// on the hash within one interval even when nobody runs the verb by hand.
//
// Exit 1 from set check is findings in the set's content: the SET OK and SET DONE lines
// still print and still count, so they are recorded and the findings go to stderr. Exit 2
// is a refusal (the file could not be read) and records nothing: the last evaluation
// stands rather than being replaced by a guess.
func sprintEvaluate(args []string, stdout, stderr io.Writer, now time.Time, deps sprintDeps) int {
	f := newFlags("sprint evaluate")
	sf := addStoreFlags(f)
	name := f.fs.String("name", "", "")
	workSet := f.fs.String("work-set", "", "")
	novaWork := f.fs.String("nova-work", "nova-work", "")
	base := f.fs.String("base", "", "")
	cache := f.fs.String("cache", "", "")
	every := f.fs.Duration("every", 0, "")
	rounds := f.fs.Int("rounds", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*workSet, "work-set", "the (work-set ...) file nova-work set check --evaluate reads")
	if *every < 0 {
		f.add(fmt.Sprintf("--every wants a positive duration, got %s", *every))
	}
	if *rounds < 0 {
		f.add(fmt.Sprintf("--rounds wants 0 (until killed) or a positive count, got %d", *rounds))
	}
	st, err := sf.open(f, deps)
	if f.refused(stderr) {
		return 2
	}
	if err != nil {
		return refuse(stderr, " sprint evaluate", err.Error())
	}
	defer st.Close()
	if deps.setCheck == nil {
		return refuse(stderr, " sprint evaluate", "no set check runner")
	}
	ctx := context.Background()
	sprintName, err := oneSprint(ctx, st, *name)
	if err != nil {
		return refuse(stderr, " sprint evaluate", err.Error())
	}
	checkArgs := []string{"set", "check", "--file", *workSet, "--evaluate"}
	if strings.TrimSpace(*base) != "" {
		checkArgs = append(checkArgs, "--base", *base)
	}
	if strings.TrimSpace(*cache) != "" {
		checkArgs = append(checkArgs, "--cache", *cache)
	}
	pass := func() error {
		out, code, err := deps.setCheck(ctx, *novaWork, checkArgs)
		if err != nil {
			return err
		}
		if code != 0 && code != 1 {
			return fmt.Errorf("nova-work set check exited %d; the last evaluation stands", code)
		}
		if code == 1 {
			fmt.Fprintf(stderr, "nova-pulse sprint evaluate: set check found problems in %s; its counts are recorded\n", *workSet)
		}
		if err := sprint.RecordAcceptance(ctx, st, sprintName, out); err != nil {
			return err
		}
		acc, _ := sprint.ParseAcceptance(out)
		fmt.Fprintf(stdout, "SPRINT EVALUATED %s %d/%d %d%%\n", sprintName, acc.Done, acc.Units, acc.Percent)
		return nil
	}
	if *every == 0 {
		if err := pass(); err != nil {
			return refuse(stderr, " sprint evaluate", err.Error())
		}
		return 0
	}
	// The reconciler: a failed pass is reported and the next interval tries again, so one
	// gh hiccup does not end it; the hash keeps the last good evaluation meanwhile.
	failed := 0
	for i := 0; *rounds == 0 || i < *rounds; i++ {
		if i > 0 {
			deps.sleep(*every)
		}
		if err := pass(); err != nil {
			failed++
			fmt.Fprintf(stderr, "nova-pulse sprint evaluate: %s\n", err)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// runSetCheck runs nova-work and returns its stdout and exit code. Stderr is kept for the
// error when the binary could not run at all.
func runSetCheck(ctx context.Context, bin string, args []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode(), nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s: %v %s", bin, err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), 0, nil
}
