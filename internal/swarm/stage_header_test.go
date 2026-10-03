package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
	require.True(t, ok, "ParseCardBase: ok=false on the REPO:/BASE:/base-sha: header every pushed card carries")
	want := defaultProbeBase + "/mas-bandwidth/nova-tools.git"
	require.Equal(t, want, repo, "repo = %q, want %q", repo, want)
	require.Equal(t, sha, gotSha, "sha = %q, want %q", gotSha, sha)
	cb := ReadCardBase(pushedHeader(sha))
	require.Equal(t, "dev", cb.Ref, "ReadCardBase = %+v, want Ref=dev Named=mas-bandwidth/nova-tools", cb)
	require.Equal(t, "mas-bandwidth/nova-tools", cb.Named, "ReadCardBase = %+v, want Ref=dev Named=mas-bandwidth/nova-tools", cb)
	require.True(t, CardNamesRepo(pushedHeader(sha)), "CardNamesRepo = false on a card with a REPO: line")
	got := CardStageBranch(pushedHeader(sha))
	require.Equal(t, "rowan/s00-0302-quack-hulk-flash", got, "CardStageBranch = %q", got)

	// The owner/name resolves through the bench mirror exactly as a base-repo URL does.
	home := t.TempDir()
	mirror := filepath.Join(home, "nova-bench", "mirror", "nova-tools.git")
	require.NoError(t, os.MkdirAll(mirror, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mirror, "HEAD"), []byte("ref: refs/heads/dev\n"), 0o644))
	got = FindBenchMirror(filepath.Dir(mirror), repo)
	require.Equal(t, mirror, got, "FindBenchMirror(%q) = %q, want %q", repo, got, mirror)
}

func TestParseCardBasePrecedenceAndAbsence(t *testing.T) {
	t.Parallel()

	const sha = "09fbedc9052145b20677501a1dbcb5f5ba9c87d4"

	// base-repo: wins over REPO: (unchanged behaviour for base-repo cards).
	both := []byte("RESULT: c1 sha=09fbedc90521\nREPO: mas-bandwidth/other\nbase-repo: https://example.com/mas-bandwidth/nova-tools.git\nbase-sha: " + sha + "\n")
	repo, gotSha, ok := ParseCardBase(both)
	require.True(t, ok, "base-repo card: repo=%q sha=%q ok=%v", repo, gotSha, ok)
	require.Equal(t, "https://example.com/mas-bandwidth/nova-tools.git", repo, "base-repo card: repo=%q sha=%q ok=%v", repo, gotSha, ok)
	require.Equal(t, sha, gotSha, "base-repo card: repo=%q sha=%q ok=%v", repo, gotSha, ok)

	// A card with neither names nothing: nothing to stage, and not a staging failure.
	neither := []byte("RESULT: c2 sha=09fbedc90521\nKIND: read\nBASE: dev\nbase-sha: " + sha + "\n")
	repo, _, ok = ParseCardBase(neither)
	require.False(t, ok, "card with no repo: repo=%q ok=%v, want \"\" false", repo, ok)
	require.Empty(t, repo, "card with no repo: repo=%q ok=%v, want \"\" false", repo, ok)
	require.False(t, CardNamesRepo(neither), "CardNamesRepo = true on a card with no repo line")
	none := []byte("RESULT: c3 sha=09fbedc90521\nREPO: -\n")
	require.False(t, CardNamesRepo(none), "CardNamesRepo = true on REPO: -")

	// A REPO: line no reader can resolve still names a repo: ok=false, CardNamesRepo true,
	// so native refuses it (STAGE FAIL reason=no-repo-staged) instead of launching.
	bad := []byte("RESULT: c4 sha=09fbedc90521\nREPO: nova-tools\nbase-sha: " + sha + "\n")
	_, _, ok = ParseCardBase(bad)
	require.False(t, ok, "ParseCardBase: ok=true on REPO: nova-tools (no owner)")
	require.True(t, CardNamesRepo(bad), "CardNamesRepo = false on REPO: nova-tools")
}
