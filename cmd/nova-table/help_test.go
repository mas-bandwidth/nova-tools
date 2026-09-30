package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEveryCommandHasEquivalentDiscoverableHelp(t *testing.T) {
	t.Parallel()
	code, banner, errout := runTable("help")
	if code != 0 || errout != "" {
		t.Fatalf("top help: %d %q", code, errout)
	}
	for _, c := range commands {
		if !strings.Contains(banner, "nova-table "+strings.TrimSpace(c.name+" "+c.syntax)+"\n") {
			t.Errorf("not discoverable: %s", c.name)
		}
		words := strings.Fields(c.name)
		code, want, errout := runTable(append([]string{"help"}, words...)...)
		if code != 0 || errout != "" || !strings.Contains(want, "usage: nova-table "+strings.TrimSpace(c.name+" "+c.syntax)+"\n") || !strings.Contains(want, "example:\n  nova-table "+c.example) {
			t.Fatalf("help %s: %d %q %q", c.name, code, want, errout)
		}
		for _, flag := range []string{"--help", "-h"} {
			code, got, errout := runTable(append(append([]string{}, words...), flag)...)
			if code != 0 || got != want || errout != "" {
				t.Errorf("%s %s differs: %d\n%s\n%s", c.name, flag, code, got, errout)
			}
		}
	}
	for _, group := range []string{"row", "col", "cell", "member", "view"} {
		_, want, _ := runTable("help", group)
		for _, alias := range []string{"--help", "-h", "help"} {
			code, got, errout := runTable(group, alias)
			if code != 0 || got != want || errout != "" {
				t.Errorf("%s %s: %d %q %q", group, alias, code, got, errout)
			}
		}
	}
	_, create, _ := runTable("help", "create")
	if strings.Index(create, "--columns <string>") > strings.Index(create, "--actor <string>") {
		t.Fatal("receipt metadata precedes product flags")
	}
	_, show, _ := runTable("help", "view", "show")
	if strings.Contains(show, "--summary") || strings.Contains(show, "--title") {
		t.Fatal("view show advertises view set flags")
	}
}

// TestEveryVerbAnswersHelpAndTouchesNothing verifies that every verb answers
// -h and --help with its own help on stdout at exit 0 and touches nothing.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	for _, c := range commands {
		words := strings.Fields(c.name)
		for _, flag := range []string{"-h", "--help"} {
			var stdout, stderr bytes.Buffer
			code := run(append(append([]string{}, words...), flag), &stdout, &stderr)
			assert.Equal(t, 0, code, "%s %s exit code", c.name, flag)
			assert.Empty(t, stderr.String(), "%s %s stderr", c.name, flag)
			assert.Contains(t, stdout.String(), "usage: nova-table "+strings.TrimSpace(c.name+" "+c.syntax))
		}
	}
}
