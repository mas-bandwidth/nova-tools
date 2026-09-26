package benchrole_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
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

func TestListReadsTheRoleColumn(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if rows, err := benchrole.List(ctx, c); err != nil || len(rows) != 0 {
		t.Fatalf("empty registry: %v %v", rows, err)
	}
	c.SAdd(ctx, "benches", "studio", "hulk", "odd")
	c.HSet(ctx, "bench:studio:desired", "role", "friends", "machine", "studio", "slots", "0")
	c.HSet(ctx, "bench:hulk:desired", "machine", "hulk", "slots", "64", "legs", "go,schema")
	c.HSet(ctx, "bench:odd:desired", "role", "ci")
	rows, err := benchrole.List(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, r.Line())
	}
	want := []string{
		"BENCH hulk role=fleet machine=hulk slots=64 legs=go,schema paused=-",
		"BENCH odd role=ci machine=- slots=- legs=- paused=-",
		"BENCH studio role=friends machine=studio slots=0 legs=- paused=-",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !rows[0].Valid || rows[1].Valid || !rows[2].Valid {
		t.Fatalf("validity %v %v %v; want only odd invalid", rows[0].Valid, rows[1].Valid, rows[2].Valid)
	}
}
