package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// STAGE (issue #2882). Of 2,396 cards launched on 2026-09-22, 1,093 failed before any model
// saw them, and 566 of those were a full `git clone` from GitHub hanging on the house benches
// (193 clones stuck 43-65 minutes on hulk at once) while every bench already held a local
// mirror. `nova-swarm stage` is the one way a card's repository is staged:
//
//	1. from the bench mirror, never from GitHub: `git clone --shared --no-checkout <mirror>`,
//	   then origin is pointed at the card's URL so a later push or fetch goes where the card says;
//	2. when the mirror does not yet carry the card's sha, ONE `git fetch origin <sha>` -- the
//	   one ref the card names, a delta on top of the mirror's objects, never a whole clone;
//	3. a detached checkout of that sha, then REVPARSE and PORCELAIN checked;
//	4. every step under one hard --timeout. A stage that does not finish in time ends the card
//	   `RESULT: BLOCKED stage-timeout <bench> <secs>`, written to <job>/RESULT.md -- the end
//	   record every card leaves -- and exits 1, so no fill loop counts it as launched.
//
// A bench with no mirror is refused (exit 1), never quietly cloned from GitHub: the missing
// mirror is the bench defect to fix, and a silent fallback is how 566 cards died.

// stageGit is the git binary the stage runs; a test points it at a wrapper.
var stageGit = "git"

// stageWaitDelay bounds how long a killed git may hold its pipes open after the deadline
// (a git-remote-https child can outlive its parent), so a timeout always returns.
const stageWaitDelay = 2 * time.Second

var stageSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func cmdStage(args []string, stdout, stderr io.Writer) int {
	f := newFlags("stage")
	url := f.fs.String("url", "", "")
	sha := f.fs.String("sha", "", "")
	mirror := f.fs.String("mirror", "", "")
	dest := f.fs.String("dest", "", "")
	job := f.fs.String("job", "", "")
	bench := f.fs.String("bench", "", "")
	label := f.fs.String("label", "", "")
	timeout := f.fs.Duration("timeout", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*url, "url", "the repository URL the card names; origin points here after the stage")
	f.want(*sha, "sha", "the full 40-character commit the card is staged at")
	f.want(*mirror, "mirror", "the bench's bare mirror of that repository, e.g. $HOME/nova-bench/mirror/<repo>.git")
	f.want(*dest, "dest", "the directory the staged checkout is written to; it must not exist yet")
	f.want(*job, "job", "the card's job directory, where RESULT.md is written when the stage ends the card")
	f.want(*bench, "bench", "this bench's name, for the STAGE and RESULT lines")
	f.want(*label, "label", "the card's label, for the STAGE lines")
	if *timeout <= 0 {
		f.add("--timeout is required: the hard limit on the whole stage, e.g. 120s; a stage with no limit is how 193 clones hung for an hour")
	}
	if *sha != "" && !stageSHA.MatchString(*sha) {
		f.add(fmt.Sprintf("--sha %s is not a full 40-character lowercase commit", oneline.Field(*sha)))
	}
	if f.refused(stderr) {
		return 2
	}

	who := fmt.Sprintf("%s on %s", oneline.Field(*label), oneline.Field(*bench))
	refused := func(why string) int {
		fmt.Fprintf(stdout, "STAGE REFUSED %s: %s\n", who, why)
		return 1
	}
	if fi, err := os.Stat(*mirror); err != nil || !fi.IsDir() {
		return refused(fmt.Sprintf("no bench mirror at %s; the stage never clones from GitHub, so provision the mirror (nova-pulse fleet mirror)", oneline.Field(*mirror)))
	}
	if _, err := os.Lstat(*dest); err == nil {
		return refused(fmt.Sprintf("%s already exists; a stage writes a fresh checkout", oneline.Field(*dest)))
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	git := func(dir string, gitArgs ...string) (string, error) {
		if dir != "" {
			gitArgs = append([]string{"-C", dir}, gitArgs...)
		}
		cmd := exec.CommandContext(ctx, stageGit, gitArgs...)
		cmd.WaitDelay = stageWaitDelay
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		if err != nil {
			if msg := strings.TrimSpace(errb.String()); msg != "" {
				err = fmt.Errorf("%w: %s", err, msg)
			}
		}
		return strings.TrimSpace(out.String()), err
	}
	// ended turns a failed step into the card's end: a timeout is BLOCKED stage-timeout with
	// the RESULT.md record, anything else a STAGE REFUSED line naming the step.
	ended := func(step string, err error) int {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			secs := int(time.Since(start).Seconds() + 0.5)
			line := fmt.Sprintf("RESULT: BLOCKED stage-timeout %s %d", oneline.Field(*bench), secs)
			record := fmt.Sprintf("%s\n\nThe stage of %s at %s from the bench mirror %s did not finish within %s (step: %s); no model saw this card.\n",
				line, oneline.Field(*url), *sha, oneline.Field(*mirror), timeout.String(), step)
			if err := os.MkdirAll(*job, 0o755); err == nil {
				err = os.WriteFile(filepath.Join(*job, "RESULT.md"), []byte(record), 0o644)
				if err != nil {
					fmt.Fprintf(stderr, "nova-swarm stage: RESULT.md could not be written to %s: %s\n", oneline.Field(*job), oneline.Err(err))
				}
			} else {
				fmt.Fprintf(stderr, "nova-swarm stage: the job directory %s could not be made: %s\n", oneline.Field(*job), oneline.Err(err))
			}
			fmt.Fprintln(stdout, line)
			return 1
		}
		return refused(fmt.Sprintf("%s failed: %s", step, oneline.Err(err)))
	}

	if _, err := git("", "clone", "--quiet", "--shared", "--no-checkout", "--", *mirror, *dest); err != nil {
		return ended("clone from the bench mirror", err)
	}
	if _, err := git(*dest, "remote", "set-url", "origin", *url); err != nil {
		return ended("pointing origin at the card's URL", err)
	}
	source := "mirror"
	if _, err := git(*dest, "cat-file", "-e", *sha+"^{commit}"); err != nil {
		if _, err := git(*dest, "fetch", "--quiet", "--no-tags", "origin", *sha); err != nil {
			return ended("fetch of "+*sha+" (the bench mirror does not carry it)", err)
		}
		source = "mirror+fetch"
	}
	if _, err := git(*dest, "checkout", "--quiet", "--detach", *sha); err != nil {
		return ended("checkout of "+*sha, err)
	}
	rev, err := git(*dest, "rev-parse", "HEAD")
	if err != nil {
		return ended("rev-parse HEAD", err)
	}
	if rev != *sha {
		return refused(fmt.Sprintf("REVPARSE=%s want %s", oneline.Field(rev), *sha))
	}
	porcelain, err := git(*dest, "status", "--porcelain")
	if err != nil {
		return ended("status --porcelain", err)
	}
	if porcelain != "" {
		return refused(fmt.Sprintf("PORCELAIN=%d, the staged tree is dirty", len(strings.Split(porcelain, "\n"))))
	}
	fmt.Fprintf(stdout, "STAGE OK %s repo=%s REVPARSE=%s source=%s secs=%d\n",
		who, oneline.Field(*url), rev, source, int(time.Since(start).Seconds()+0.5))
	return 0
}
