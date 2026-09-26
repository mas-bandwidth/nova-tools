package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// cardRenderFromIssuePush (nova-tools#3911) runs at the end of TestCardMovesCLI
// (already serial for its t.Setenv; the serial allowlist only shrinks): task push --issue fills the
// record from the issue text, card render --id prints a harness card card
// push's linter admits and --brief the friend brief from the same record, a
// flash push without PATHS is refused naming it, and a record with no swarm
// route is refused at render.
func cardRenderFromIssuePush(t *testing.T) {
	t.Helper()
	newSeat(t)
	t.Setenv("FRIEND_QUEUE_SPRINT", seatSprint)
	t.Setenv("NOVA_FRIEND", "")
	mirror := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirror, "nova-tools.git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", mirror)
	dir := t.TempDir()
	issue := filepath.Join(dir, "issue.md")
	text := "STREAM: swarm: cards\nWHO: any\nPATHS: internal/x/x.go\n\nBuild x.\n\nDONE-WHEN: `go test ./internal/x -run TestX` passes."
	if err := os.WriteFile(issue, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("ab", 20)
	push := func(id, route string, extra ...string) (int, string, string) {
		args := append([]string{"push", "--actor", "rowan", "--id", id, "--waiting", "--ref", "mas-bandwidth/nova-tools#3911",
			"--title", "card " + id, "--issue", issue, "--route", route, "--base", "dev", "--base-sha", sha}, extra...)
		return runTaskCLI(args...)
	}
	if code, out, errOut := push("f1", "flash"); code != 0 || !strings.Contains(out, "to=waiting") {
		t.Fatalf("push flash = %d %q %q", code, out, errOut)
	}
	render := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := runCard(context.Background(), append([]string{"render"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	code, out, errOut := render("--id", "f1")
	if code != 0 || !strings.HasPrefix(out, "RESULT: f1 sha="+sha[:12]+"\n") || !strings.Contains(errOut, "RENDERED card id=f1") {
		t.Fatalf("render = %d %q %q", code, out, errOut)
	}
	if err := card.Lint(context.Background(), []byte(out)); err != nil {
		t.Fatalf("card lint refused the render: %v\n%s", err, out)
	}
	for _, want := range []string{"\nPATHS: internal/x/x.go\n", "\nTEST: ./internal/x TestX\n", "\nSTREAM: swarm: cards\n", "\n> Build x.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	if code, out, _ := render("--id", "f1", "--brief", "--model", "sonnet"); code != 0 || !strings.HasPrefix(out, "CARD: f1\n") || !strings.Contains(out, "DONE-WHEN: `go test ./internal/x -run TestX` passes.\n") {
		t.Errorf("render --brief = %d %q", code, out)
	}
	// pro from an issue with no PATHS line: refused at push, naming it.
	bare := filepath.Join(dir, "bare.md")
	if err := os.WriteFile(bare, []byte("STREAM: swarm: cards\n\nDONE-WHEN: it works."), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runTaskCLI("push", "--actor", "rowan", "--id", "p1", "--waiting", "--ref", "mas-bandwidth/nova-tools#1",
		"--issue", bare, "--route", "pro", "--base", "dev", "--base-sha", sha)
	if code != 1 || !strings.Contains(out, "REFUSED") || !strings.Contains(out, "PATHS") {
		t.Errorf("pro push without PATHS = %d %q", code, out)
	}
	if code, out, _ := push("fr1", "friend"); code != 0 {
		t.Fatalf("push friend = %d %q", code, out)
	}
	if code, out, _ := render("--id", "fr1"); code != 1 || !strings.Contains(out, "CARD RENDER REFUSED id=fr1") || !strings.Contains(out, "ROUTE") {
		t.Errorf("render of a friend card = %d %q", code, out)
	}
	if code, out, _ := render("--id", "nope"); code != 1 || !strings.Contains(out, "NOTASK") {
		t.Errorf("render of no record = %d %q", code, out)
	}
	if code, _, _ := render(); code != 2 {
		t.Errorf("render with no --id = %d, want 2", code)
	}
}
