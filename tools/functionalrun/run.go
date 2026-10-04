package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// runTier is the run verb: reap, image, caches, the networked module step,
// the test container, the leftover check, one receipt line.
func runTier(ctx context.Context, eng engine, c runConfig, stdout, stderr io.Writer) int {
	t0 := time.Now()
	runID := newRunID(t0)
	logf := func(format string, a ...any) {
		fmt.Fprintf(stderr, "functionalrun: "+format+"\n", a...)
	}

	// 1. Anything an earlier run left past its deadline goes first.
	if _, _, err := reap(ctx, eng, time.Now(), c.grace, c.ownerID, false, stderr); err != nil {
		logf("the reaper could not list containers: %v", err)
		return setupExit(ctx)
	}

	// 2. The image: the one given, or the context's, built only when absent.
	image, buildSecs, err := ensureImage(ctx, eng, c, stderr)
	if err != nil {
		logf("%v", err)
		return setupExit(ctx)
	}

	// 3. The two cache volumes, this user's own.
	for _, v := range []struct{ name, kind string }{{c.gocache, "gocache"}, {c.gomod, "gomod"}} {
		if v.kind == "gocache" && c.freshGocache {
			continue
		}
		if err := ensureVolume(ctx, eng, v.name, v.kind, c.ownerID); err != nil {
			logf("%v", err)
			return setupExit(ctx)
		}
	}

	// 4. The module cache, filled with the network before the run without it.
	stamp, err := modHash(c.src)
	if err != nil {
		logf("hashing go.mod and go.sum: %v", err)
		return setupExit(ctx)
	}
	mt := time.Now()
	pre := prefillArgs(c, image, runID, stamp, moduleProxy(os.Getenv), mt)
	code, ended := runContainer(ctx, eng, pre, containerName(runID+"-mod"), mt.Add(prefillDeadline+clientGrace), stderr, stderr)
	left := leftovers(eng, runID+"-mod", leftoverBudget)
	if code != 0 || ended != "finished" || left != 0 {
		logf("the module cache step ended %s with exit %d, %d container(s) left", ended, code, left)
		if ended == "interrupted" {
			return exitInterrupted
		}
		return setupExit(ctx)
	}
	modSecs := time.Since(mt).Seconds()

	// 5. The run itself.
	start := time.Now()
	deadline := start.Add(c.deadline)
	logf("run=%s image=%s packages=%q deadline=%s (%s) cpus=%d memory=%s",
		runID, short(strings.TrimPrefix(image, "sha256:")), strings.Join(c.packages, " "), c.deadline, deadline.Format(time.RFC3339), c.cpus, c.memory)
	code, ended = runContainer(ctx, eng, testArgs(c, image, runID, start), containerName(runID), deadline.Add(clientGrace), stdout, stderr)
	ended, exit := classify(code, ended, time.Since(start), c.deadline)

	// 6. Nothing of the run may be left.
	left = leftovers(eng, runID, leftoverBudget)
	wall := time.Since(start).Seconds()
	if left != 0 {
		exit = exitCannotRun
	}
	fmt.Fprintf(stderr, "FUNCTIONAL RUN run=%s ended=%s exit=%d wall=%.1fs build=%.1fs modcache=%.1fs total=%.1fs containers_left=%s\n",
		runID, ended, exit, wall, buildSecs, modSecs, time.Since(t0).Seconds(), leftText(left))
	return exit
}

// classify turns how the test container's client returned into the run's
// ended and this tool's exit code. elapsed is measured from the container's
// start; deadline is the run's bound.
//
//   - interrupted: 130; this tool's own client deadline: 124;
//   - the client lost (ended by a signal, code < 0): 125, client-lost;
//   - a non-zero exit at the deadline: the runtime's --timeout ended it, 124;
//   - 124, or 137 at or after the inner bound: the in-container timeout (137
//     when its -k KILL fired at what ignored TERM), 124, inner-timeout;
//   - anything else is the container's own code, finished.
func classify(code int, ended string, elapsed, deadline time.Duration) (string, int) {
	switch ended {
	case "interrupted":
		return ended, exitInterrupted
	case "deadline":
		return ended, exitDeadline
	}
	switch {
	case code < 0:
		return "client-lost", exitCannotRun
	case code == 0:
		return "finished", 0
	case elapsed >= deadline-time.Second:
		return "deadline", exitDeadline
	case code == exitDeadline:
		return "inner-timeout", exitDeadline
	case code == 137 && elapsed >= deadline-innerMargin:
		return "inner-timeout", exitDeadline
	}
	return "finished", code
}

// leftText is the receipt's count of leftovers: -1 is a count that could not
// be read.
func leftText(n int) string {
	if n < 0 {
		return "unknown"
	}
	return strconv.Itoa(n)
}

// setupExit is the exit code of a run that ended before its test container:
// 130 when this process was interrupted, 125 otherwise.
func setupExit(ctx context.Context) int {
	if ctx.Err() != nil {
		return exitInterrupted
	}
	return exitCannotRun
}

// runContainer starts one container attached, streams its output, and ends it
// by the first of: its own exit; the client deadline (removed by this process;
// the runtime's own --timeout has already fired by then); an interrupt of this
// process (removed at once). ended is "finished", "deadline" or "interrupted";
// code is the client's exit code when finished.
func runContainer(ctx context.Context, eng engine, args []string, name string, clientDeadline time.Time, stdout, stderr io.Writer) (int, string) {
	p, err := eng.Start(args, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "functionalrun: %v\n", err)
		return exitCannotRun, "finished"
	}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := p.Wait()
		done <- result{code, err}
	}()
	timer := time.After(time.Until(clientDeadline))
	var ended string
	// Every ending removes the container first and logs after, so a log line
	// that cannot be written never stands between a run and its removal.
	select {
	case r := <-done:
		// --rm has removed it when the client saw the container exit; this is
		// then a no-op. A lost client (code < 0) leaves it to this removal.
		removeContainer(eng, name, stderr)
		if r.err != nil {
			fmt.Fprintf(stderr, "functionalrun: waiting for the runtime's client: %v\n", r.err)
			return exitCannotRun, "finished"
		}
		return r.code, "finished"
	case <-timer:
		ended = "deadline"
	case <-ctx.Done():
		ended = "interrupted"
	}
	removeContainer(eng, name, stderr)
	fmt.Fprintf(stderr, "functionalrun: %s: %s removed\n", ended, name)
	// The client returns once its container is gone. If it does not, it is
	// ended: it is the one process here this tool started.
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		// ignored: the container outlived its grace; the wait on done is the proof it ended
		_ = p.Kill()
		<-done
	}
	return -1, ended
}

func removeContainer(eng engine, name string, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := eng.Output(ctx, removeArgs(name)...); err != nil {
		fmt.Fprintf(stderr, "functionalrun: removing %s: %v\n", name, err)
	}
}

// leftoverBudget bounds the whole leftover check: every listing and removal
// in it, so a hung runtime cannot hold the tool past it.
const leftoverBudget = 30 * time.Second

// leftovers counts the containers of one run still present, in any state,
// after giving the runtime's removal a moment to finish; any still there are
// removed by id and counted again. All of it within budget. -1: the count
// could not be read.
func leftovers(eng engine, runID string, budget time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	count := func() (int, []string) {
		out, err := eng.Output(ctx, leftoverArgs(runID)...)
		if err != nil {
			return -1, nil
		}
		ids := strings.Fields(out)
		return len(ids), ids
	}
	var n int
	var ids []string
	for i := 0; i < 20 && ctx.Err() == nil; i++ {
		n, ids = count()
		if n == 0 {
			return 0
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, id := range ids {
		// ignored: a removal of leftover containers; the next run's leftover pass lists and removes them again
		_, _ = eng.Output(ctx, removeArgs(id)...)
	}
	n, _ = count()
	return n
}

// ensureImage returns the image id to run: --image as given, or the image
// built from --context, tagged by the context's hash and built only when no
// image carries that tag.
func ensureImage(ctx context.Context, eng engine, c runConfig, stderr io.Writer) (string, float64, error) {
	ref := c.image
	var secs float64
	if ref == "" {
		hash, err := contextHash(c.context)
		if err != nil {
			return "", 0, fmt.Errorf("hashing the build context %s: %v", c.context, err)
		}
		ref = imageTag(hash)
		if _, err := inspectImage(ctx, eng, ref); err != nil {
			fmt.Fprintf(stderr, "functionalrun: building %s from %s\n", ref, c.context)
			t := time.Now()
			p, err := eng.Start(buildArgs(c.context, ref), stderr, stderr)
			if err != nil {
				return "", 0, err
			}
			bctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			code, ended := waitOrKill(bctx, p)
			if ended != "" || code != 0 {
				return "", 0, fmt.Errorf("building the image ended %s with exit %d", orFinished(ended), code)
			}
			secs = time.Since(t).Seconds()
		} else {
			fmt.Fprintf(stderr, "functionalrun: reusing %s\n", ref)
		}
	}
	id, err := inspectImage(ctx, eng, ref)
	if err != nil {
		return "", 0, fmt.Errorf("image %s: %v", ref, err)
	}
	return id, secs, nil
}

func inspectImage(ctx context.Context, eng engine, ref string) (string, error) {
	ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := eng.Output(ictx, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("no image id for %s", ref)
	}
	return id, nil
}

// waitOrKill waits for a started command, killing its client when ctx ends.
func waitOrKill(ctx context.Context, p process) (int, string) {
	done := make(chan int, 1)
	go func() {
		code, _ := p.Wait()
		done <- code
	}()
	select {
	case code := <-done:
		return code, ""
	case <-ctx.Done():
		// ignored: the run was interrupted; the wait on done is the proof it ended, and the word interrupted is returned
		_ = p.Kill()
		return <-done, "interrupted"
	}
}

func orFinished(s string) string {
	if s == "" {
		return "finished"
	}
	return s
}

// ensureVolume creates a cache volume labelled with its owner and its kind,
// or checks that an existing one is this user's and of this kind. A volume of
// another owner, or of the other kind (the two caches swapped), is refused,
// never written. Two first runs at once may both find it missing: the one
// whose create fails looks again and checks what the other created.
func ensureVolume(ctx context.Context, eng engine, name, kind, ownerID string) error {
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := eng.Output(vctx, volumeInspectArgs(name)...)
	if err != nil {
		_, cerr := eng.Output(vctx, volumeCreateArgs(name, kind, ownerID)...)
		if cerr == nil {
			return nil
		}
		out, err = eng.Output(vctx, volumeInspectArgs(name)...)
		if err != nil {
			return fmt.Errorf("creating the %s volume %s: %v", kind, name, cerr)
		}
	}
	owner, got, _ := strings.Cut(strings.TrimSpace(out), "|")
	if owner != ownerID {
		return fmt.Errorf("the %s volume %s belongs to owner %q, not to uid %s; pass --%s-volume <name>", kind, name, owner, ownerID, kind)
	}
	if got != kind {
		return fmt.Errorf("the volume %s is a %q cache, not a %s cache; pass --%s-volume <name>", name, got, kind, kind)
	}
	return nil
}
