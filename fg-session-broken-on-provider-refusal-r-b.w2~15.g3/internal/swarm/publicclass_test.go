package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CARD-8390 red tests: a public-class worker never sees a card that clones an
// unlisted repo. A listed repo is admitted; an unlisted repo is refused naming
// it; a paid-class worker admits both.

func publicTestWorker(name, class string) Worker {
	return Worker{Name: name, Class: class}
}

// forgeClone and forgeMention spell a card's github URL through the package's
// own probe base (defaultProbeBase, admitrepo.go) instead of a test literal, so
// no unit test names a real network host. The card text the gate reads is
// byte-for-byte what it was, and the gate only reads it: no request is made.
func forgeClone(repo string) string {
	return "git clone " + defaultProbeBase + "/" + repo + ".git"
}

func forgeMention(repo string) string {
	return strings.TrimPrefix(defaultProbeBase, "https://") + "/" + repo
}

func writeAllowlist(t *testing.T, root string, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "public-repos.txt"), []byte(body), 0o644))
}

func TestPublicClassListedRepoIsAdmitted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAllowlist(t, root, "acme/public")
	card := "RESULT: x\nSTEP 1\n" + forgeClone("acme/public") + " repo\n"
	repo, refused := CheckPublicCard(publicTestWorker("muse", "public"), card, root)
	require.False(t, refused, "a public-class worker with a card cloning a listed repo is admitted, got refused repo=%s", repo)
}

func TestPublicClassUnlistedRepoIsRefusedNamingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAllowlist(t, root, "acme/public")
	card := "RESULT: x\nSTEP 1\n" + forgeClone("acme/secret") + " repo\n"
	repo, refused := CheckPublicCard(publicTestWorker("muse", "public"), card, root)
	require.True(t, refused, "an unlisted repo is refused, got admitted")
	require.Equal(t, "acme/secret", repo, "the refusal names the repo, got %q want %q", repo, "acme/secret")
	line := PublicRefusalLine(repo, "muse")
	want := "CARD REFUSED reason=private-source repo=acme/secret class=public worker=muse"
	require.Equal(t, want, line, "the refusal line is exact:\nwant %q\ngot  %q", want, line)
}

func TestPaidClassAdmitsBoth(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAllowlist(t, root, "acme/public")
	listed := "RESULT: x\n" + forgeClone("acme/public") + " repo\n"
	unlisted := "RESULT: x\n" + forgeMention("acme/secret") + "\n"
	_, refused := CheckPublicCard(publicTestWorker("w", "paid"), unlisted, root)
	require.False(t, refused, "a paid-class worker admits an unlisted repo, got refused")
	_, refused = CheckPublicCard(publicTestWorker("w", ""), unlisted, root)
	require.False(t, refused, "a default (paid) worker admits an unlisted repo, got refused")
	_, refused = CheckPublicCard(publicTestWorker("w", "paid"), listed, root)
	require.False(t, refused, "a paid-class worker admits a listed repo, got refused")
}
