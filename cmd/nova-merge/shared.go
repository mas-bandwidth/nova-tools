package main

// shared.go holds the helpers the kept verbs (batch, gate, read, classify, fold) took
// from files that went with the per-PR lander role (stream-is-the-unit): simulate's
// check runner and conflict reader, react's quiet Redis logger, and the poison seam
// classify reads.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// poisonHost is the poison detector's seam: the failing tests, the changed packages and
// the issue a park names.
type poisonHost interface {
	PoisonFailures(pr int) []merge.Failure
	ChangedPackages(pr int) []string
	IssueFor(pr int) string
}

// quietRedis is the logger go-redis's internals write through. It drops every line: a
// library's pool diagnostics are not this tool's output grammar, and five of them ahead
// of a one-line refusal is a reader reading the library instead of the tool (edge 16).
// A connection this tool could not make is reported by the verb, on the verb's own line.
type quietRedis struct{}

func (quietRedis) Printf(context.Context, string, ...interface{}) {}

// silenceRedis installs it, before the first dial of every verb that dials -- and
// ONCE FOR THE PROCESS (#1609). `redis.SetLogger` writes a package-level variable
// inside go-redis, so a second verb in the same process writing it again is a data
// race with the first: `go test -race ./cmd/nova-merge/` caught two `react` runs on
// it, one run in six on vision, and ten tests failed behind that one race. The Once
// also orders the write before every dial: a caller returns from Do only after the
// first caller's write has completed.
var silenceRedisOnce sync.Once

func extraCheckSecret(name string) bool {
	up := strings.ToUpper(strings.TrimSpace(name))
	return strings.Contains(up, "PASSWORD") || strings.Contains(up, "WEBHOOK")
}

// withSaneSHLVL returns env with exactly one SHLVL=1. A missing or zero SHLVL
// makes a child bash -u a top-level shell; the coordinator's adopted fix is
// SHLVL=1, not dropping SSH_CLIENT (#2499 item 4).
func withSaneSHLVL(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "SHLVL") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "SHLVL=1")
}

// checkChildEnv is the environment a simulate or batch check child is started with.
// A nil env is this process's own. Clean drops GOFLAGS-class names and any NAME
// carrying KEY, TOKEN or SECRET; this also drops PASSWORD and WEBHOOK, which are
// credentials this tree holds (SMTP_PASSWORD, BSKY_APP_PASSWORD, DISCORD_*_WEBHOOK)
// and which Clean still keeps. The drop is by NAME, never by value (#1836).
//
// SHLVL=1: a child bash -u that inherits SHLVL=0 is a top-level shell
// (shell_level < 2). Under SSH_CLIENT, Debian/Ubuntu bash sources
// /etc/bash.bashrc, which expands $PS1 under `set -u` and dies
// (`PS1: unbound variable`). The coordinator exports SHLVL=1 before exec;
// the tests this verb runs get the same floor. SSH_CLIENT is not stripped
// (#2499 item 4).
func checkChildEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	cleaned := goenv.Clean(env)
	out := make([]string, 0, len(cleaned)+1)
	for _, entry := range cleaned {
		name, _, ok := strings.Cut(entry, "=")
		if ok && extraCheckSecret(name) {
			continue
		}
		out = append(out, entry)
	}
	return withSaneSHLVL(out)
}

// runCheckUntil is runCheck on an injected clock: deadline is handed the --timeout once the
// check has started and returns the channel that says it expired. A nil deadline is a real
// timer of that length, which is every production caller (Deps.CheckDeadline).
func runCheckUntil(dir, check string, timeout time.Duration, env []string, deadline func(time.Duration) <-chan time.Time) (string, error) {
	name, args := shellCommand(check)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = checkChildEnv(env)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	configureCheckProcess(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var expired <-chan time.Time
	if deadline != nil {
		expired = deadline(timeout)
	} else {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}
	select {
	case err := <-done:
		return buf.String(), err
	case <-expired:
		killCheckProcess(cmd)
		<-done
		return buf.String(), fmt.Errorf("no answer within the %s --timeout", timeout)
	}
}

// runCheck runs one check in the scratch worktree under its own deadline. The child is
// its own process group so that a check which spawns children is killed whole when
// --timeout expires; a deadline that kills only the shell leaves the tree running.
//
// A nil env is this process's own, which is what `simulate` hands it; `batch` hands it
// an environment whose temp directory is the batch's own, so that two gates running on
// one bench cannot write over each other's scratch. Both go through checkChildEnv:
// goenv.Clean plus the credential names Clean still keeps, because the child runs
// code from the tree under test (#1836).
func runCheck(dir, check string, timeout time.Duration, env []string) (string, error) {
	return runCheckUntil(dir, check, timeout, env, nil)
}

// firstLine is the first line of a check's output that says something, or the error
// itself when the check printed nothing at all. It is what SIMULATE POISON names.
func firstLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	if err != nil {
		return err.Error()
	}
	return "(no output)"
}

// hasConflicts reports whether the index holds unmerged paths, which is how git says
// the squash-merge stopped on a conflict rather than on some other failure.
func hasConflicts(g *merge.Git) (bool, error) {
	out, err := g.Run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func silenceRedis() { silenceRedisOnce.Do(func() { redis.SetLogger(quietRedis{}) }) }
