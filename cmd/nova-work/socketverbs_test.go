package main

import (
	"bytes"
	"strings"
	"testing"
)

// The verbs beyond the four reach the socket and spell the spec's own request
// line. One case per shape the serializer has: a two-word verb, a mutation
// carrying <write flags>, a repeatable flag, a switch, and a flag whose value
// holds the two characters the field form exists for (a space and an equals).
func TestEverySocketVerbShapeSpellsItsRequestLine(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "a one-word mutation with write flags",
			args: []string{"state", "--session", "S", "--as", "Rowan", "--request", "r-7",
				"--expect", "41", "--node", "E01.11", "--to", "doing", "--reason", "the client lands"},
			want: "state --session S --as Rowan --request r-7 --expect 41 --node E01.11 --to doing --reason the\\x20client\\x20lands",
		},
		{
			name: "a two-word verb",
			args: []string{"operation", "wait", "--session", "S", "--id", "op-3", "--timeout", "30s"},
			want: "operation wait --session S --id op-3 --timeout 30s",
		},
		{
			name: "a repeatable flag keeps the caller's order",
			args: []string{"node", "add", "--session", "S", "--as", "Rowan", "--id", "n1", "--type", "task",
				"--under", "p1", "--acceptance", "c1:test:a:b", "--acceptance", "c2:test:c:d", "--reason", "r"},
			want: "node add --session S --as Rowan --id n1 --type task --under p1 --acceptance c1:test:a:b --acceptance c2:test:c:d --reason r",
		},
		{
			name: "a switch is spelled --name true and only when set",
			args: []string{"goal", "update", "--session", "S", "--as", "Rowan", "--expect", "9", "--stop", "--reason", "enough"},
			want: "goal update --session S --as Rowan --expect 9 --reason enough --stop true",
		},
		{
			name: "a value holding a space and an equals travels as one token",
			args: []string{"machine", "--session", "S", "--as", "Rowan", "--register", "m1",
				"--name", "hulk two", "--fact", "cores=32", "--reason", "r"},
			want: "machine --session S --as Rowan --register m1 --name hulk\\x20two --fact cores\\x3d32 --reason r",
		},
		{
			name: "dry-run is a switch of the write flags",
			args: []string{"dep", "--session", "S", "--as", "Rowan", "--dry-run", "--node", "a", "--add", "b", "--reason", "r"},
			want: "dep --session S --as Rowan --dry-run true --node a --add b --reason r",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			socket, requests := fakeSession(t, "OK ROW nothing=0")
			args := make([]string, len(c.args))
			copy(args, c.args)
			for i, a := range args {
				if a == "S" {
					args[i] = socket
				}
			}
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr, ""); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			want := strings.Replace(c.want, "--session S", "--session "+socket, 1)
			if got := awaitRequest(t, requests); got != want {
				t.Fatalf("request line =\n  %q\nwant\n  %q", got, want)
			}
		})
	}
}

// Every socket verb refuses to guess the socket, the same way the four did.
func TestEverySocketVerbRefusesToGuessTheSession(t *testing.T) {
	for _, verb := range allSocketVerbs() {
		if verb == "query" {
			continue // query's own refusal names --snapshot beside --session
		}
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(strings.Fields(verb), &stdout, &stderr, "")
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("wrote stdout: %q", stdout.String())
			}
			line := strings.TrimSuffix(stderr.String(), "\n")
			if strings.Count(stderr.String(), "\n") != 1 || !strings.HasSuffix(line, "run: nova-work help") {
				t.Fatalf("refusal = %q, want one line ending in the remedy", stderr.String())
			}
			if !strings.Contains(line, "refusing to guess") {
				t.Fatalf("refusal = %q, want it refusing to guess the socket", line)
			}
		})
	}
}

// Every socket verb classifies the session's answer by the second token and by
// nothing else, so the verb table cannot have grown a second print path.
func TestEverySocketVerbClassifiesTheReplyByTheSecondToken(t *testing.T) {
	for _, verb := range allSocketVerbs() {
		t.Run(verb, func(t *testing.T) {
			for _, c := range []struct {
				reply string
				code  int
				out   bool
			}{
				{"NODE OK id=n1 rev=4", 0, true},
				{"NODE REFUSED id=n1: rule 15", 1, false},
			} {
				socket, _ := fakeSession(t, c.reply)
				args := append(strings.Fields(verb), "--session", socket)
				if verb == "query" {
					args = append(args, "--ask", "done", "--branch", "open")
				}
				var stdout, stderr bytes.Buffer
				if code := run(args, &stdout, &stderr, ""); code != c.code {
					t.Fatalf("%q exit = %d, want %d (stdout %q stderr %q)", c.reply, code, c.code, stdout.String(), stderr.String())
				}
				got, other := stdout.String(), stderr.String()
				if !c.out {
					got, other = other, got
				}
				if got != c.reply+"\n" {
					t.Fatalf("%q was printed as %q", c.reply, got)
				}
				if other != "" {
					t.Fatalf("%q also wrote %q to the other stream", c.reply, other)
				}
			}
		})
	}
}

// The block's lines this client does not send are refused BY NAME, with the
// reason, rather than as an unknown verb or as a stray positional argument.
func TestTheLinesTheClientDoesNotSendAreRefusedByName(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"state", "load", "--from", "x", "--into", "y"}, "local reader"},
		{[]string{"savepoint", "restore", "--savepoint", "x", "--into", "y"}, "local reader"},
		{[]string{"report", "--session", "x", "--as", "r"}, "SPEC-AHEAD"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args[:2], " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr, ""); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			line := strings.TrimSuffix(stderr.String(), "\n")
			if !strings.HasSuffix(line, "run: nova-work help") {
				t.Fatalf("refusal = %q, want the remedy", line)
			}
			if !strings.Contains(line, c.want) {
				t.Fatalf("refusal = %q, want it naming %q", line, c.want)
			}
			if strings.Contains(line, "unknown verb") || strings.Contains(line, "positional") {
				t.Fatalf("refusal = %q: the line is in the spec and the refusal should say so", line)
			}
		})
	}
}

// A family named with no sub-verb, or with one the spec does not spell, is
// refused naming the sub-verbs there are.
func TestAVerbFamilyRefusalNamesItsSubVerbs(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"session"}, []string{"export", "handoff", "replay", "start", "status", "stop"}},
		{[]string{"node"}, []string{"add", "edit", "move", "remove", "require"}},
		{[]string{"operation", "bogus"}, []string{"cancel", "list", "status", "wait"}},
		{[]string{"roadmap"}, []string{"configure", "create", "projection", "row"}},
		{[]string{"execution"}, []string{"correct", "pause", "reconcile", "resume", "status", "stop"}},
		{[]string{"goal"}, []string{"set", "show", "update"}},
		{[]string{"savepoint"}, []string{"compare", "create", "list", "verify"}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr, ""); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			line := strings.TrimSuffix(stderr.String(), "\n")
			for _, sub := range c.want {
				if !strings.Contains(line, sub) {
					t.Fatalf("refusal = %q does not name the sub-verb %q", line, sub)
				}
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("refusal is not one line: %q", stderr.String())
			}
		})
	}
}

// The query ask list grew two the spec added and the client had not: routes,
// which is the fleet's route registry, and reports. Their constraints come
// with them.
func TestTheTwoAsksTheSpecAddedAreCarriedWithTheirConstraints(t *testing.T) {
	socket, requests := fakeSession(t, "ROUTE ROW id=r1")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"query", "--session", socket, "--ask", "routes", "--branch", "open", "--class", "cheap"}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("--ask routes exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query --session "+socket+" --ask routes --branch open --class cheap"; got != want {
		t.Fatalf("routes request = %q, want %q", got, want)
	}

	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"--class names nothing outside routes", "only --ask routes admits it",
			[]string{"query", "--session", "s", "--ask", "fleet", "--branch", "open", "--class", "cheap"}},
		{"reports requires --since", "requires --since",
			[]string{"query", "--session", "s", "--ask", "reports", "--branch", "open"}},
		{"reports is --branch open only", "--branch open only",
			[]string{"query", "--session", "s", "--ask", "reports", "--branch", "closed", "--since", "4", "--from", "a", "--to", "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr, ""); code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), c.want) {
				t.Fatalf("refusal = %q, want it naming %q", stderr.String(), c.want)
			}
		})
	}
}
