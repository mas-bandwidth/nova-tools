package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD-8390 red tests: a public-class worker never sees a card that clones an
// unlisted repo. A listed repo is admitted; an unlisted repo is refused naming
// it; a paid-class worker admits both.

func publicTestWorker(name, class string) Worker {
	return Worker{Name: name, Class: class}
}

func writeAllowlist(t *testing.T, root string, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	if err := os.WriteFile(filepath.Join(root, "public-repos.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublicClassListedRepoIsAdmitted(t *testing.T) {
	root := t.TempDir()
	writeAllowlist(t, root, "acme/public")
	card := "RESULT: x\nSTEP 1\ngit clone https://github.com/acme/public.git repo\n"
	if repo, refused := CheckPublicCard(publicTestWorker("muse", "public"), card, root); refused {
		t.Fatalf("a public-class worker with a card cloning a listed repo is admitted, got refused repo=%s", repo)
	}
}

func TestPublicClassUnlistedRepoIsRefusedNamingIt(t *testing.T) {
	root := t.TempDir()
	writeAllowlist(t, root, "acme/public")
	card := "RESULT: x\nSTEP 1\ngit clone https://github.com/acme/secret.git repo\n"
	repo, refused := CheckPublicCard(publicTestWorker("muse", "public"), card, root)
	if !refused {
		t.Fatal("an unlisted repo is refused, got admitted")
	}
	if repo != "acme/secret" {
		t.Fatalf("the refusal names the repo, got %q want %q", repo, "acme/secret")
	}
	line := PublicRefusalLine(repo, "muse")
	want := "CARD REFUSED reason=private-source repo=acme/secret class=public worker=muse"
	if line != want {
		t.Fatalf("the refusal line is exact:\nwant %q\ngot  %q", want, line)
	}
}

func TestPaidClassAdmitsBoth(t *testing.T) {
	root := t.TempDir()
	writeAllowlist(t, root, "acme/public")
	listed := "RESULT: x\ngit clone https://github.com/acme/public.git repo\n"
	unlisted := "RESULT: x\ngithub.com/acme/secret\n"
	if _, refused := CheckPublicCard(publicTestWorker("w", "paid"), unlisted, root); refused {
		t.Fatal("a paid-class worker admits an unlisted repo, got refused")
	}
	if _, refused := CheckPublicCard(publicTestWorker("w", ""), unlisted, root); refused {
		t.Fatal("a default (paid) worker admits an unlisted repo, got refused")
	}
	if _, refused := CheckPublicCard(publicTestWorker("w", "paid"), listed, root); refused {
		t.Fatal("a paid-class worker admits a listed repo, got refused")
	}
}
