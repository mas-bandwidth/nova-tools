package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRouteRegister proves the route verb with --register reaches the socket
// and spells its request line according to the spec's grammar.
func TestRouteRegister(t *testing.T) {
	socket, requests := fakeSession(t, "ROUTE OK id=ev-route-1 request=r-1 route=r1 rev=3 pushed=- changed=1 emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--session", socket, "--as", "Rowan", "--request", "r-1",
		"--register", "r1", "--provider", "openai", "--endpoint", "https://api.example.invalid/v1",
		"--key-location", "path", "--plan", "gpt-4o", "--cost-per-mtok", "2.50",
		"--capabilities", "chat:completion", "--owner", "mas-bandwidth", "--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ROUTE OK") {
		t.Fatalf("stdout = %q, want ROUTE OK", stdout.String())
	}
	want := "route --session " + socket + " --as Rowan --request r-1 --register r1 --provider openai --endpoint https://api.example.invalid/v1 --key-location path --plan gpt-4o --cost-per-mtok 2.50 --capabilities chat:completion --owner mas-bandwidth --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestRouteProbe proves the route verb with --probe reaches the socket and
// spells its request line.
func TestRouteProbe(t *testing.T) {
	socket, requests := fakeSession(t, "PROBE OK route=r1 pass=true")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--session", socket, "--as", "Rowan", "--request", "r-1",
		"--probe", "r1", "--card", "card-42", "--pass", "true", "--wall", "1.2s",
		"--usd", "0.05", "--source", "bench", "--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "PROBE OK") {
		t.Fatalf("stdout = %q, want PROBE OK", stdout.String())
	}
	want := "route --session " + socket + " --as Rowan --request r-1 --probe r1 --card card-42 --pass true --wall 1.2s --usd 0.05 --source bench --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestOfferSend proves the offer verb reaches the socket and spells its
// request line.
func TestOfferSend(t *testing.T) {
	socket, requests := fakeSession(t, "OFFER OK id=ev-1 request=r-1 node=E01.11 offer=o1 attempt=a1 to=alice effect=dispatched reserved=10 until=2026-09-20T00:00:00Z rev=5 pushed=- emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"offer", "--session", socket, "--as", "Rowan", "--request", "r-1",
		"--node", "E01.11", "--offer", "o1", "--to", "alice", "--profile", "gpt-4o",
		"--attempt", "a1", "--generation", "3", "--reserve", "10",
		"--until", "2026-09-20T00:00:00Z", "--requested-model", "gpt-4o",
		"--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OFFER OK") {
		t.Fatalf("stdout = %q, want OFFER OK", stdout.String())
	}
	want := "offer --session " + socket + " --as Rowan --request r-1 --node E01.11 --offer o1 --to alice --profile gpt-4o --attempt a1 --generation 3 --reserve 10 --until 2026-09-20T00:00:00Z --requested-model gpt-4o --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestProfileWrite proves the profile verb with --write reaches the socket
// and spells its request line.
func TestProfileWrite(t *testing.T) {
	socket, requests := fakeSession(t, "PROFILE OK id=ev-1 request=r-1 profile=p1 rev=2 pushed=- emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"profile", "--session", socket, "--as", "Rowan", "--request", "r-1",
		"--write", "p1", "--model", "gpt-4o", "--harness", "openai", "--work-type", "go-fix",
		"--pointer", "v1", "--policy", "default", "--evidence", "sha256:abc",
		"--expiry", "2026-12-31T00:00:00Z", "--owner", "mas-bandwidth",
		"--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "PROFILE OK") {
		t.Fatalf("stdout = %q, want PROFILE OK", stdout.String())
	}
	want := "profile --session " + socket + " --as Rowan --request r-1 --write p1 --model gpt-4o --harness openai --work-type go-fix --pointer v1 --policy default --evidence sha256:abc --expiry 2026-12-31T00:00:00Z --owner mas-bandwidth --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestAcknowledgeStage proves the acknowledge verb with --stage reaches the
// socket and spells its request line.
func TestAcknowledgeStage(t *testing.T) {
	socket, requests := fakeSession(t, "ACKNOWLEDGE OK id=ev-1 request=r-1 node=E01.11 offer=o1 attempt=a1 stage=received effect=received lease=- reserved=1 committed=0 rev=5 pushed=- emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"acknowledge", "--session", socket, "--as", "Rowan", "--request", "r-1",
		"--offer", "o1", "--reply", "r1", "--stage", "received",
		"--provenance", "data", "--provenance-sha256", "sha256:def",
		"--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ACKNOWLEDGE OK") {
		t.Fatalf("stdout = %q, want ACKNOWLEDGE OK", stdout.String())
	}
	want := "acknowledge --session " + socket + " --as Rowan --request r-1 --offer o1 --reply r1 --stage received --provenance data --provenance-sha256 sha256:def --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestDecline proves the decline verb reaches the socket and spells its
// request line.
func TestDecline(t *testing.T) {
	socket, requests := fakeSession(t, "DECLINE OK id=ev-1 request=r-1 node=E01.11 offer=o1 attempt=a1 effect=declined released=10 rev=5 pushed=- emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"decline", "--session", socket, "--as", "Rowan", "--request", "r-1",
		"--offer", "o1", "--reply", "r1",
		"--provenance", "data", "--provenance-sha256", "sha256:def",
		"--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "DECLINE OK") {
		t.Fatalf("stdout = %q, want DECLINE OK", stdout.String())
	}
	want := "decline --session " + socket + " --as Rowan --request r-1 --offer o1 --reply r1 --provenance data --provenance-sha256 sha256:def --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestExecutionPause proves the execution pause verb reaches the socket and
// spells its request line.
func TestExecutionPause(t *testing.T) {
	socket, requests := fakeSession(t, "EXECUTION OK id=ev-1 request=r-1 control=ctl-1 change=pause scope=node operation=op-1 selected=3 rev=2 pushed=- emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"execution", "pause", "--session", socket, "--as", "Rowan",
		"--request", "r-1", "--node", "E01.11", "--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "EXECUTION OK") {
		t.Fatalf("stdout = %q, want EXECUTION OK", stdout.String())
	}
	want := "execution pause --session " + socket + " --as Rowan --request r-1 --node E01.11 --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestExecutionResume proves the execution resume verb reaches the socket and
// spells its request line.
func TestExecutionResume(t *testing.T) {
	socket, requests := fakeSession(t, "EXECUTION OK id=ev-1 request=r-1 control=ctl-1 change=resume scope=control operation=op-1 selected=1 rev=2 pushed=- emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"execution", "resume", "--session", socket, "--as", "Rowan",
		"--request", "r-1", "--control", "ctl-1", "--action", "release-hold",
		"--reason", "test"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "EXECUTION OK") {
		t.Fatalf("stdout = %q, want EXECUTION OK", stdout.String())
	}
	want := "execution resume --session " + socket + " --as Rowan --request r-1 --control ctl-1 --action release-hold --reason test"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}

// TestExecutionStatus proves the execution status verb reaches the socket and
// spells its request line.
func TestExecutionStatus(t *testing.T) {
	socket, requests := fakeSession(t, "EXECUTION OK control=ctl-1 change=status selected=3 pending-delivery=1 acknowledged=0 confirmed=2 unsupported=0 unresolved=0 rev=2 pushed=- shown=3 emitted=0")
	var stdout, stderr bytes.Buffer
	code := run([]string{"execution", "status", "--session", socket, "--control", "ctl-1", "--max", "10"},
		&stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "EXECUTION OK") {
		t.Fatalf("stdout = %q, want EXECUTION OK", stdout.String())
	}
	want := "execution status --session " + socket + " --control ctl-1 --max 10"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
	}
}
