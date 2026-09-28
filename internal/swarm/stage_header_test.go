package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// pushedHeader is the header every card `nova-sprint card push` pushes today, verbatim from
// the quack-0925b run (nova-tools#3711): REPO: owner/name, BASE: <ref>, base-sha: <sha40>.
// Before #3711 ParseCardBase read none of it and the model was launched with no repo.
func pushedHeader(sha string) []byte {
	return []byte("RESULT: s00-0302-quack-hulk-flash sha=" + sha[:12] + "\n" +
		"KIND: fix\n" +
		"TYPE: code\n" +
		"REPO: mas-bandwidth/nova-tools\n" +
		"BASE: dev\n" +
		"base-sha: " + sha + "\n" +
		"PATHS: docs/quack/s00-0302-quack-hulk-flash.txt\n")
}

func TestParseCardBaseReadsThePushedHeader(t *testing.T) {
	t.Parallel()

	const sha = "ac1dfd2ea24f90af179121f26d71a2f8bfb85df6"
	repo, gotSha, ok := ParseCardBase(pushedHeader(sha))
	if !ok {
		t.Fatal("ParseCardBase: ok=false on the REPO:/BASE:/base-sha: header every pushed card carries")
	}
	if want := defaultProbeBase + "/mas-bandwidth/nova-tools.git"; repo != want {
		t.Fatalf("repo = %q, want %q", repo, want)
	}
	if gotSha != sha {
		t.Fatalf("sha = %q, want %q", gotSha, sha)
	}
	cb := ReadCardBase(pushedHeader(sha))
	if cb.Ref != "dev" || cb.Named != "mas-bandwidth/nova-tools" {
		t.Fatalf("ReadCardBase = %+v, want Ref=dev Named=mas-bandwidth/nova-tools", cb)
	}
	if !CardNamesRepo(pushedHeader(sha)) {
		t.Fatal("CardNamesRepo = false on a card with a REPO: line")
	}
	if got := CardStageBranch(pushedHeader(sha)); got != "rowan/s00-0302-quack-hulk-flash" {
		t.Fatalf("CardStageBranch = %q", got)
	}

	// The owner/name resolves through the bench mirror exactly as a base-repo URL does.
	home := t.TempDir()
	mirror := filepath.Join(home, "nova-bench", "mirror", "nova-tools.git")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindBenchMirror(home, repo); got != mirror {
		t.Fatalf("FindBenchMirror(%q) = %q, want %q", repo, got, mirror)
	}
}

func TestParseCardBasePrecedenceAndAbsence(t *testing.T) {
	t.Parallel()

	const sha = "09fbedc9052145b20677501a1dbcb5f5ba9c87d4"

	// base-repo: wins over REPO: (unchanged behaviour for base-repo cards).
	both := []byte("RESULT: c1 sha=09fbedc90521\nREPO: mas-bandwidth/other\nbase-repo: https://example.com/mas-bandwidth/nova-tools.git\nbase-sha: " + sha + "\n")
	repo, gotSha, ok := ParseCardBase(both)
	if !ok || repo != "https://example.com/mas-bandwidth/nova-tools.git" || gotSha != sha {
		t.Fatalf("base-repo card: repo=%q sha=%q ok=%v", repo, gotSha, ok)
	}

	// A card with neither names nothing: nothing to stage, and not a staging failure.
	neither := []byte("RESULT: c2 sha=09fbedc90521\nKIND: read\nBASE: dev\nbase-sha: " + sha + "\n")
	if repo, _, ok := ParseCardBase(neither); ok || repo != "" {
		t.Fatalf("card with no repo: repo=%q ok=%v, want \"\" false", repo, ok)
	}
	if CardNamesRepo(neither) {
		t.Fatal("CardNamesRepo = true on a card with no repo line")
	}
	none := []byte("RESULT: c3 sha=09fbedc90521\nREPO: -\n")
	if CardNamesRepo(none) {
		t.Fatal("CardNamesRepo = true on REPO: -")
	}

	// A REPO: line no reader can resolve still names a repo: ok=false, CardNamesRepo true,
	// so native refuses it (STAGE FAIL reason=no-repo-staged) instead of launching.
	bad := []byte("RESULT: c4 sha=09fbedc90521\nREPO: nova-tools\nbase-sha: " + sha + "\n")
	if _, _, ok := ParseCardBase(bad); ok {
		t.Fatal("ParseCardBase: ok=true on REPO: nova-tools (no owner)")
	}
	if !CardNamesRepo(bad) {
		t.Fatal("CardNamesRepo = false on REPO: nova-tools")
	}
}
