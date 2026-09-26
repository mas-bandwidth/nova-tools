package card_test

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestQuackIssue: the rendered primary carries every header line the push
// reads (taskcard.ParseIssue completes it with nothing missing for a swarm
// route), its one fixture path and line name the sprint and the id, the DO
// and DONE-WHEN lines quote that same line, and a bad tier, id or base-sha is
// refused before anything is rendered.
func TestQuackIssue(t *testing.T) {
	t.Parallel()

	sha := strings.Repeat("ab", 20)
	in := card.QuackInput{Sprint: "quack-0926", Stream: "quack", ID: card.QuackID(7), Tier: "flash",
		Repo: "mas-bandwidth/quack", Base: "dev", BaseSHA: sha}
	text, err := card.QuackIssue(in)
	if err != nil {
		t.Fatal(err)
	}
	spec := taskcard.ParseIssue(text)
	if missing := spec.Complete("mas-bandwidth/nova-tools#4232", ""); len(missing) != 0 {
		t.Fatalf("a swarm card lacks %v:\n%s", missing, text)
	}
	path, line := card.QuackFixture("quack-0926", "quack-007")
	if path != "docs/fixtures/quack-quack-0926-quack-007.txt" || line != "quack quack-0926 quack-007" {
		t.Fatalf("fixture %q %q", path, line)
	}
	for k, want := range map[string]string{"Stream": spec.Stream, "Route": spec.Route, "Repo": spec.Repo, "Base": spec.Base,
		"BaseSHA": spec.BaseSHA, "Paths": spec.Paths, "Task": spec.Task, "Source": spec.Source, "Kind": spec.Kind, "Priority": spec.Priority} {
		got := map[string]string{"Stream": "quack", "Route": "flash", "Repo": "mas-bandwidth/quack", "Base": "dev", "BaseSHA": sha,
			"Paths": path, "Task": "quack-007", "Source": "quack", "Kind": "fix", "Priority": "100"}[k]
		if want != got {
			t.Fatalf("%s=%q, want %q", k, want, got)
		}
	}
	for _, s := range []string{"NO-SUBAGENTS: ", "UNATTENDED: ", "DO: create " + path + " containing exactly the line " + strconv.Quote(line),
		"DONE-WHEN: the file " + path + " exists in the commit and its whole content is the single line " + strconv.Quote(line)} {
		if !strings.Contains(text, s) {
			t.Fatalf("missing %q in:\n%s", s, text)
		}
	}
	if card.QuackTitle("quack-007", "flash") != "quack quack-007 flash" || card.QuackID(100) != "quack-100" {
		t.Fatal("title or id shape")
	}
	for name, bad := range map[string]card.QuackInput{
		"tier": {Sprint: "s", Stream: "q", ID: "quack-001", Tier: "max", Repo: "o/r", Base: "dev", BaseSHA: sha},
		"id":   {Sprint: "s", Stream: "q", ID: "probe-1", Tier: "pro", Repo: "o/r", Base: "dev", BaseSHA: sha},
		"sha":  {Sprint: "s", Stream: "q", ID: "quack-001", Tier: "pro", Repo: "o/r", Base: "dev", BaseSHA: "abc"},
		"repo": {Sprint: "s", Stream: "q", ID: "quack-001", Tier: "pro", Base: "dev", BaseSHA: sha},
	} {
		if _, err := card.QuackIssue(bad); err == nil {
			t.Fatalf("bad %s: want a refusal", name)
		}
	}
}

// TestMirrorBranchSHA reads the base-sha from this host's mirror with git,
// and refuses a repo with no mirror.
func TestMirrorBranchSHA(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NOVA_MIRROR_ROOT", root)
	if _, err := card.MirrorBranchSHA("mas-bandwidth/nova-tools", "dev"); err == nil {
		t.Fatal("no mirror: want a refusal")
	}
	work := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "dev", work},
		{"-C", work, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "base"},
		{"clone", "-q", "--bare", work, root + "/nova-tools.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	want, _ := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	got, err := card.MirrorBranchSHA("mas-bandwidth/nova-tools", "dev")
	if err != nil || got != strings.TrimSpace(string(want)) {
		t.Fatalf("got %q %v, want %s", got, err, want)
	}
	if _, err := card.MirrorBranchSHA("mas-bandwidth/nova-tools", "nope"); err == nil {
		t.Fatal("missing branch: want a refusal")
	}
}
