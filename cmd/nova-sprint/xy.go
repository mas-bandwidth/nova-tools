package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintline"
)

// xy registers itself, as every verb file does; main.go is not edited
// (registry.go). Its flags and sources are in docs/CLI.md under nova-sprint.
func init() {
	register(Verb{
		Name:    "xy",
		Summary: "print x/y z% -> ~eta from nova-work set check --evaluate and the sprint calibration",
		Run: func(_ context.Context, args []string, out, errOut io.Writer) int {
			return cmdXY(args, out, errOut)
		},
	})
}

func cmdXY(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("xy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	set := fs.String("set", "", "work-set sexp passed to nova-work; its :status is not a count")
	evaluateOut := fs.String("evaluate-out", "", "stdout of nova-work set check --evaluate")
	calibrationOut := fs.String("calibration-out", "", "stdout of nova-pulse sprint calibration")
	openPath := fs.String("open", "", "TASK lines for the still-open sprint tasks")
	base := fs.String("base", "", "passed to nova-work --base when nova-work is run")
	name := fs.String("name", "", "passed to nova-pulse sprint --name when the sprint verb is run")
	store := fs.String("store", "", "host:port passed to nova-pulse --store when the sprint verb is run")
	novaWork := fs.String("nova-work", "nova-work", "nova-work binary")
	novaPulse := fs.String("nova-pulse", "nova-pulse", "nova-pulse binary")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "xy", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "xy", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	needEval := strings.TrimSpace(*evaluateOut) == ""
	needCal := strings.TrimSpace(*calibrationOut) == ""
	needOpen := strings.TrimSpace(*openPath) == ""
	haveStore := strings.TrimSpace(*store) != ""
	var missing []string
	if needEval && strings.TrimSpace(*set) == "" {
		missing = append(missing, "--evaluate-out, or --set so nova-work set check --evaluate can be run")
	}
	if needCal && !haveStore {
		missing = append(missing, "--calibration-out, or --store so nova-pulse sprint calibration can be run")
	}
	if needOpen && !haveStore {
		missing = append(missing, "--open, or --store so nova-pulse sprint status --verbose can be read for its open rows")
	}
	if len(missing) > 0 {
		return refuse(stderr, "xy", "needs "+strings.Join(missing, "; ")+"; refusing to count :status and refusing the hand eta formula")
	}

	evalText, err := evaluateText(*evaluateOut, *set, *base, *novaWork)
	if err != nil {
		return refuse(stderr, "xy", err.Error())
	}
	calText, err := calibrationText(*calibrationOut, *name, *store, *novaPulse)
	if err != nil {
		return refuse(stderr, "xy", err.Error())
	}
	tasks, err := openTasks(*openPath, *name, *store, *novaPulse)
	if err != nil {
		return refuse(stderr, "xy", err.Error())
	}
	line, err := sprintline.Compose(evalText, calText, tasks)
	if err != nil {
		return refuse(stderr, "xy", err.Error())
	}
	fmt.Fprintln(stdout, line)
	return 0
}

func evaluateText(outPath, setPath, base, bin string) (string, error) {
	if strings.TrimSpace(outPath) != "" {
		b, err := os.ReadFile(outPath)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	args := []string{"set", "check", "--file", setPath, "--evaluate"}
	if strings.TrimSpace(base) != "" {
		args = append(args, "--base", base)
	}
	stdout, stderr, exit, err := runTool(bin, args...)
	if err != nil {
		return "", fmt.Errorf("nova-work: %w", err)
	}
	if _, perr := sprintline.ParseEvaluate(stdout); perr != nil {
		return "", fmt.Errorf("nova-work set check --evaluate exit %d (%s); %s", exit, oneLine(stderr), perr)
	}
	return stdout, nil
}

func calibrationText(outPath, name, store, bin string) (string, error) {
	if strings.TrimSpace(outPath) != "" {
		b, err := os.ReadFile(outPath)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	args := []string{"sprint", "calibration", "--store", store}
	if strings.TrimSpace(name) != "" {
		args = append(args, "--name", name)
	}
	stdout, stderr, exit, err := runTool(bin, args...)
	if err != nil {
		return "", fmt.Errorf("nova-pulse sprint calibration: %w", err)
	}
	if _, perr := sprintline.ParseSuggest(stdout); perr != nil {
		return "", fmt.Errorf("nova-pulse sprint calibration exit %d (%s); %s", exit, oneLine(stderr), perr)
	}
	return stdout, nil
}

func openTasks(openPath, name, store, bin string) ([]sprintline.Task, error) {
	if strings.TrimSpace(openPath) != "" {
		b, err := os.ReadFile(openPath)
		if err != nil {
			return nil, err
		}
		return sprintline.ParseOpenFile(string(b))
	}
	args := []string{"sprint", "status", "--verbose", "--store", store}
	if strings.TrimSpace(name) != "" {
		args = append(args, "--name", name)
	}
	stdout, stderr, exit, err := runTool(bin, args...)
	if err != nil {
		return nil, fmt.Errorf("nova-pulse sprint status: %w", err)
	}
	tasks, perr := sprintline.ParseStatus(stdout)
	if perr != nil {
		return nil, fmt.Errorf("nova-pulse sprint status exit %d (%s); %s", exit, oneLine(stderr), perr)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("nova-pulse sprint status named no open task; eta wants each row's id, owner and est, and the status fraction is not this line's x/y")
	}
	return tasks, nil
}

func runTool(bin string, args ...string) (stdout, stderr string, exit int, err error) {
	if strings.TrimSpace(bin) == "" {
		return "", "", -1, errors.New("the tool binary is empty; refusing to guess")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader("")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()
	stdout, stderr = out.String(), errb.String()
	if runErr == nil {
		return stdout, stderr, 0, nil
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		return stdout, stderr, ee.ExitCode(), nil
	}
	return stdout, stderr, -1, runErr
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "no stderr"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return oneline.Cap(s, oneline.TailBytes)
}
