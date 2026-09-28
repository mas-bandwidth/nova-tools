package main

import (
	"bytes"
	"testing"
)

func TestFriendRegister(t *testing.T) {
	socket, requests := fakeSession(t, "FRIEND OK friend=alice registered=true rev=5")
	args := []string{"friend", "--session", socket, "--as", "Rowan", "--register", "alice", "--role", "peer", "--reason", "adding friend"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "FRIEND OK friend=alice registered=true rev=5\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "friend --session " + socket + " --as Rowan --register alice --role peer --reason adding\\x20friend"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestConfigExport(t *testing.T) {
	socket, requests := fakeSession(t, "CONFIG OK export=my-config into=/tmp/out rev=3")
	args := []string{"config", "--session", socket, "--export", "my-config", "--into", "/tmp/out"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "CONFIG OK export=my-config into=/tmp/out rev=3\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "config --session " + socket + " --export my-config --into /tmp/out"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestModelRegister(t *testing.T) {
	socket, requests := fakeSession(t, "MODEL OK model=gpt-4 provider=openai rev=7")
	args := []string{"model", "--session", socket, "--as", "Rowan", "--register", "gpt-4", "--provider", "openai", "--route", "default", "--billing", "metered", "--reason", "register model"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "MODEL OK model=gpt-4 provider=openai rev=7\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "model --session " + socket + " --as Rowan --register gpt-4 --provider openai --route default --billing metered --reason register\\x20model"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestObserveState(t *testing.T) {
	socket, requests := fakeSession(t, "OBSERVE OK friend=alice state=awake rev=2")
	args := []string{"observe", "--session", socket, "--as", "Rowan", "--friend", "alice", "--state", "awake", "--source", "pointer", "--reason", "state change"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "OBSERVE OK friend=alice state=awake rev=2\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "observe --session " + socket + " --as Rowan --friend alice --state awake --source pointer --reason state\\x20change"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestMachineRegister(t *testing.T) {
	socket, requests := fakeSession(t, "MACHINE OK machine=m1 registered=true rev=4")
	args := []string{"machine", "--session", socket, "--as", "Rowan", "--register", "m1", "--name", "test machine", "--fact", "cores=32", "--reason", "register machine"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "MACHINE OK machine=m1 registered=true rev=4\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "machine --session " + socket + " --as Rowan --register m1 --name test\\x20machine --fact cores\\x3d32 --reason register\\x20machine"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestGoalSet(t *testing.T) {
	socket, requests := fakeSession(t, "GOAL OK goal=n1 set=true rev=6")
	args := []string{"goal", "set", "--session", socket, "--as", "Rowan", "--expect", "5", "--goal", "n1", "--reason", "set goal"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "GOAL OK goal=n1 set=true rev=6\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "goal set --session " + socket + " --as Rowan --expect 5 --goal n1 --reason set\\x20goal"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestGoalShow(t *testing.T) {
	socket, requests := fakeSession(t, "GOAL ROW goal=n1 scope=Rowan rev=6")
	args := []string{"goal", "show", "--session", socket, "--as", "Rowan", "--max", "10"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "GOAL ROW goal=n1 scope=Rowan rev=6\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "goal show --session " + socket + " --as Rowan --max 10"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestGoalUpdate(t *testing.T) {
	socket, requests := fakeSession(t, "GOAL OK goal=n1 updated=true rev=7")
	args := []string{"goal", "update", "--session", socket, "--as", "Rowan", "--expect", "5", "--progress", "done", "--reason", "update goal"}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "GOAL OK goal=n1 updated=true rev=7\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	want := "goal update --session " + socket + " --as Rowan --expect 5 --progress done --reason update\\x20goal"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}
