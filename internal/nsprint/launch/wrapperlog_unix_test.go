//go:build unix

package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A wrapper log that cannot be opened is a refusal naming the path, and the
// wrapper is not started: its output would have had nowhere to go.
func TestLaunchRefusesWhenTheWrapperLogCannotBeOpened(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrapper := filepath.Join(dir, WrapperName)
	started := filepath.Join(dir, "started")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nread line\ntouch "+started+"\necho LAUNCHED >&3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// LogDir is a FILE, so the log's parent directory cannot be made.
	logDir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(logDir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l := fixtureLines(1)[0]
	var out, errOut strings.Builder
	res, err := Launch(strings.NewReader(l.String()+"\n"), &out, Config{Wrapper: wrapper, LogDir: logDir, Err: &errOut})
	if err != nil || res.Started != 0 || res.Refused != 1 {
		t.Fatalf("Launch = %+v err %v:\n%s", res, err, out.String())
	}
	want := "REFUSED line=1 start " + l.Card() + ": wrapper log " + filepath.Join(logDir, l.Sprint, l.Label, "1.log") + ": "
	if !strings.Contains(out.String(), want) || !strings.Contains(errOut.String(), want) {
		t.Fatalf("out %q stderr %q, want the refusal %q on both", out.String(), errOut.String(), want)
	}
	if _, err := os.Stat(started); err == nil {
		t.Fatal("the wrapper was started with nowhere for its output to go")
	}
}

// A copy's REFUSED acknowledgement names the wrapper log beside it, and the
// log holds what the wrapper printed on the way to refusing.
func TestLaunchCopyNamesTheWrapperLogInARefusal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrapper := filepath.Join(dir, WrapperName)
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nread line\necho 'nova-card copy: not dealt to this bench' >&2\necho 'REFUSED not dealt' >&3\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(dir, "results")
	env := []string{WrapperLogEnv + "=" + results}
	ack, err := LaunchCopyEnv(wrapper, CopyLine{Copy: "task-7~1", Token: "1.deadbeef"}, 5*time.Second, env)
	if err == nil || ack != "REFUSED not dealt" {
		t.Fatalf("ack %q err %v, want the REFUSED line and an error", ack, err)
	}
	logPath := CopyLogPath(wrapper, env, "task-7~1")
	if want := "REFUSED not dealt (wrapper log " + logPath + ")"; err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
	if logPath != filepath.Join(results, WrapperLogDir, CopyArg, "task-7~1.log") {
		t.Fatalf("copy log = %s, not under the results root", logPath)
	}
	body, rerr := os.ReadFile(logPath)
	if rerr != nil || !strings.Contains(string(body), "not dealt to this bench") {
		t.Fatalf("the wrapper's stderr is not in its log %s: %q (%v)", logPath, body, rerr)
	}
}
