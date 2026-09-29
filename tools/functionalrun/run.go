package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// runTier is the run verb: reap, image, caches, the networked module step,
// the test container, the leftover check, one receipt line.
func runTier(ctx context.Context, eng engine, c runConfig, now func() time.Time, stdout, stderr io.Writer) int {
	t0 := now()
	runID := newRunID(t0)
	logf := func(format string, a ...any) {
		fmt.Fprintf(stderr, "functionalrun: "+format+"\n", a...)
	}

	// 1. Anything an earlier run left past its deadline goes first.
	if _, err := reap(ctx, eng, now(), c.grace, false, stderr); err != nil {
		logf("the reaper could not list containers: %v", err)
		return setupExit(ctx)
	}

	// 2. The image: the one given, or the context's, built only when absent.
	image, buildSecs, err := ensureImage(ctx, eng, c, now, stderr)
	if err != nil {
		logf("%v", err)
		return setupExit(ctx)
	}

	// 3. The two cache volumes, this user's own.
	for _, v := range []struct{ name, kind string }{{c.gocache, "gocache"}, {c.gomod, "gomod"}} {
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
	mt := now()
	pre := prefillArgs(c, image, runID, stamp, moduleProxy(os.Getenv), mt)
	code, ended := runContainer(ctx, eng, pre, containerName(runID+"-mod"), mt.Add(prefillDeadline+clientGrace), stderr, stderr)
	left := leftovers(eng, runID+"-mod")
	if code != 0 || ended != "finished" || left != 0 {
		logf("the module cache step ended %s with exit %d, %d container(s) left", ended, code, left)
		if ended == "interrupted" {
			return exitInterrupted
		}
		return setupExit(ctx)
	}
	modSecs := now().Sub(mt).Seconds()

	// 5. The run itself.
	start := now()
	deadline := start.Add(c.deadline)
	logf("run=%s image=%s packages=%q deadline=%s (%s) cpus=%d memory=%s",
		runID, short(strings.TrimPrefix(image, "sha256:")), strings.Join(c.packages, " "), c.deadline, deadline.Format(time.RFC3339), c.cpus, c.memory)
	code, ended = runContainer(ctx, eng, testArgs(c, image, runID, start), containerName(runID), deadline.Add(clientGrace), stdout, stderr)
	if ended == "finished" && code != 0 && !now().Before(deadline.Add(-time.Second)) {
		// The runtime's own --timeout ended it: the bound held without us.
		ended = "deadline"
	}
	if ended == "finished" && code == exitDeadline {
		ended = "inner-timeout"
	}

	// 6. Nothing of the run may be left.
	left = leftovers(eng, runID)
	wall := now().Sub(start).Seconds()

	exit := code
	switch ended {
	case "deadline":
		exit = exitDeadline
	case "interrupted":
		exit = exitInterrupted
	}
	if left != 0 {
		exit = exitCannotRun
	}
	fmt.Fprintf(stderr, "FUNCTIONAL RUN run=%s ended=%s exit=%d wall=%.1fs build=%.1fs modcache=%.1fs total=%.1fs containers_left=%d\n",
		runID, ended, exit, wall, buildSecs, modSecs, now().Sub(t0).Seconds(), left)
	return exit
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
	timer := time.NewTimer(time.Until(clientDeadline))
	defer timer.Stop()
	var ended string
	select {
	case r := <-done:
		if r.err != nil {
			fmt.Fprintf(stderr, "functionalrun: waiting for the runtime's client: %v\n", r.err)
			return exitCannotRun, "finished"
		}
		// Belt and braces: --rm has removed it; this is a no-op then.
		removeContainer(eng, name, stderr)
		return r.code, "finished"
	case <-timer.C:
		ended = "deadline"
		fmt.Fprintf(stderr, "functionalrun: %s passed its deadline; removing it\n", name)
	case <-ctx.Done():
		ended = "interrupted"
		fmt.Fprintf(stderr, "functionalrun: interrupted; removing %s\n", name)
	}
	removeContainer(eng, name, stderr)
	// The client returns once its container is gone. If it does not, it is
	// ended: it is the one process here this tool started.
	select {
	case <-done:
	case <-time.After(15 * time.Second):
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

// leftovers counts the containers of one run still present, in any state,
// after giving the runtime's removal a bounded moment to finish; any still
// there are removed by id and counted again.
func leftovers(eng engine, runID string) int {
	count := func() (int, []string) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := eng.Output(ctx, leftoverArgs(runID)...)
		if err != nil {
			return -1, nil
		}
		ids := strings.Fields(out)
		return len(ids), ids
	}
	var n int
	var ids []string
	for i := 0; i < 20; i++ {
		n, ids = count()
		if n == 0 {
			return 0
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, id := range ids {
		removeContainer(eng, id, io.Discard)
	}
	n, _ = count()
	if n < 0 {
		return 1
	}
	return n
}

// ensureImage returns the image id to run: --image as given, or the image
// built from --context, tagged by the context's hash and built only when no
// image carries that tag.
func ensureImage(ctx context.Context, eng engine, c runConfig, now func() time.Time, stderr io.Writer) (string, float64, error) {
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
			t := now()
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
			secs = now().Sub(t).Seconds()
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

// ensureVolume creates a cache volume labelled with its owner, or checks that
// an existing one is this user's. A volume of another user is refused, never
// written.
func ensureVolume(ctx context.Context, eng engine, name, kind, ownerID string) error {
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := eng.Output(vctx, volumeInspectArgs(name)...)
	if err != nil {
		if _, cerr := eng.Output(vctx, volumeCreateArgs(name, kind, ownerID)...); cerr != nil {
			return fmt.Errorf("creating the %s volume %s: %v", kind, name, cerr)
		}
		return nil
	}
	if owner := strings.TrimSpace(out); owner != ownerID {
		return fmt.Errorf("the %s volume %s belongs to owner %q, not to uid %s; pass --%s-volume <name>", kind, name, owner, ownerID, kind)
	}
	return nil
}
