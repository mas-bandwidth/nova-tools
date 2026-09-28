package main

// dogfood_test.go is the red-test half of the merge lane's dogfood pass, 2026-09-18: a
// non-author drove `nova-merge batch`, `nova-merge queue` and `nova-merge react` against
// this repository and wrote down every edge they fell off. Each test below names the
// edge it pins and the thing that went wrong, so the day one comes back a reader knows
// what it cost the first time.
//
// This file holds the unit half: the edges pinned without a process -- the go notice
// and version readers, the help text, the redis logger. The edges that drive run()
// with the lab's deps (a real git against a bare fixture repository) are in
// dogfood_functional_test.go under the functional build tag.

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// EDGE 1. With go1.22 on PATH and a go.mod asking for 1.26, the whole gate ran and the
// failure surfaced as `step=build reason="go: downloading go1.26 (linux/amd64)"` -- a
// progress NOTICE naming nothing to fix. The notice is never the news.
func TestGoNoticesAreNotTheFailure(t *testing.T) {
	t.Parallel()
	out := "go: downloading go1.26 (linux/amd64)\ngo: downloading golang.org/x/mod v0.1.0\n# example.com/batch/pkg/c\npkg/c/c.go:3: undefined: X\n"
	if got := firstLine(dropGoNotices(out), nil); got != "# example.com/batch/pkg/c" {
		t.Errorf("the reason is %q; a `go: downloading` line is a notice and never the failure", got)
	}
	_, _, reason := stepFailure(batchStep{name: "build", command: "go build ./..."}, out, nil)
	if strings.Contains(reason, "go: downloading") {
		t.Errorf("BATCH FAIL reason = %q; a `go: downloading` line is a notice and never the failure", reason)
	}
	if !strings.Contains(reason, "undefined: X") {
		t.Errorf("BATCH FAIL reason = %q; the compiler line must survive once the notices are dropped (#2499 item 3)", reason)
	}
	// A step whose output is ONLY notices still says what it said, rather than nothing.
	only := "go: downloading go1.26 (linux/amd64)\n"
	if got := firstLine(dropGoNotices(only), nil); got == "" || got == "(no output)" {
		t.Errorf("an output of nothing but notices printed %q; a reader must still see what the step said", got)
	}
}

// EDGE 1, the version comparison behind the refusal: go1.9 is older than go1.22, which a
// string compare gets backwards.
func TestGoVersionsCompareNumerically(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		have, want string
		older      bool
	}{
		{"1.22", "1.26", true},
		{"1.9", "1.22", true},
		{"1.26.5", "1.26", false},
		{"1.26", "1.26.5", true},
		{"1.26.5", "1.26.5", false},
		{"2.0", "1.26", false},
	} {
		if got := olderThan(c.have, c.want); got != c.older {
			t.Errorf("olderThan(%q, %q) = %v, want %v", c.have, c.want, got, c.older)
		}
	}
}

// EDGE 3. The help said the test step runs `go test -json -count=1 -timeout 5m` and the
// step has carried no -timeout since integration-4: the Makefile's target does not set
// one, so neither does the gate. A help that describes a command the tool does not run is
// a document that sends a reader looking for a flag that is not there.
func TestTheHelpDescribesTheCommandTheGateRuns(t *testing.T) {
	t.Parallel()
	command := strings.Join(ciTestArgs(), " ")
	if !strings.Contains(usage, command) {
		t.Errorf("the help does not carry the gate's own test command %q; it describes a command this tool does not run", command)
	}
	if strings.Contains(usage, "-timeout 5m") {
		t.Error("the help still says the test step runs with -timeout 5m, and it has not since integration-4")
	}
}

// EDGE 16. Five raw `redis: ... pool.go` lines landed on stderr ahead of this verb's own
// clean refusal. The library's diagnostics are not this tool's output grammar, and the
// silencer is installed before the first dial of the verb that dials.
func TestTheRedisLoggerIsSilencedBeforeTheFirstDial(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("verbs.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	silence := strings.Index(body, "silenceRedis()")
	dial := strings.Index(body, "deps.Dial(")
	switch {
	case silence < 0:
		t.Fatal("verbs.go (read --redis) no longer silences go-redis's own logger; five raw pool.go lines came out ahead of a one-line refusal the day it did not")
	case dial < 0:
		t.Fatal("verbs.go (read --redis) no longer dials; if the verb moved, move this check with it")
	case silence > dial:
		t.Error("verbs.go (read --redis) dials before it silences the logger; the chatter this exists to stop is written while the connection is being made")
	}
}

// #1609. `silenceRedis` was `redis.SetLogger(quietRedis{})` run on every `react`, and
// `redis.SetLogger` writes a package-level variable inside go-redis. Two `react` runs in
// one process -- which is what this package's own tests are, `reactOnce` starting the
// verb in a goroutine while the package runs in parallel -- both write it, and
// `go test -race ./cmd/nova-merge/` caught it one run in six on vision. `-race` is a
// certification leg, and ten tests failed behind that one race.
//
// The property is the tool's, not the harness's: NO TWO VERBS IN ONE PROCESS MAY RACE ON
// THE LIBRARY'S LOGGER. This drives the silencer itself from several goroutines at once,
// so the detector fires on every -race run rather than one in six.
func TestSilencingTheRedisLoggerIsNotARaceBetweenTwoVerbs(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			silenceRedis()
		}()
	}
	wg.Wait()
}
