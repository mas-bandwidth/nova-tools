package benchrole_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
)

func TestParseRole(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{"": "fleet", "fleet": "fleet", "friends": "friends", " friends ": "friends"} {
		got, err := benchrole.Parse(raw)
		if err != nil || got != want {
			t.Fatalf("Parse(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"ci", "swarm", "Friends", "friend", "fleet,friends"} {
		if _, err := benchrole.Parse(raw); err == nil || !strings.Contains(err.Error(), "want friends or fleet") {
			t.Fatalf("Parse(%q) err = %v; want a refusal naming friends or fleet", raw, err)
		}
	}
	if got, err := benchrole.ParseFlag(""); err != nil || got != "" {
		t.Fatalf("ParseFlag(\"\") = %q, %v; want keep", got, err)
	}
	if _, err := benchrole.ParseFlag("coordinator"); err == nil {
		t.Fatal("ParseFlag(coordinator) accepted")
	}
}

func TestRefusedIsOneLineExitOne(t *testing.T) {
	t.Parallel()

	var err error = benchrole.Refused("studio", "friends", "no CI claim on a friends bench")
	var re *benchrole.Error
	if !errors.As(err, &re) || re.ExitCode() != 1 {
		t.Fatalf("Refused: %v; want a *benchrole.Error with exit 1", err)
	}
	line := err.Error()
	if !strings.HasPrefix(line, "REFUSED bench=studio role=friends: no CI claim") || strings.Contains(line, "\n") ||
		!strings.Contains(line, "--role fleet") {
		t.Fatalf("line %q: want one REFUSED line naming bench, role and the remedy", line)
	}
}
