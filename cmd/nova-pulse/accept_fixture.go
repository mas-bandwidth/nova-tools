package main

// The accept gate's fixture repository (SPEC-TOOLWORK §1 rule 6, nova-tools#2222).
//
// It is shipped as data, not as a git directory: `testdata/accept/base/` holds the base
// tree (every file carries an `.in` suffix, so no Go tool and no linter reads the
// fixture's own Go files as this repository's), `fix.patch` is the known-good fix as
// one commit on top, and `card.txt` is the card the gate reads for it. The builder
// writes a fresh repository from those on every call, so the gate's selftest and its
// tests run against the same bytes and neither can drift from the other.

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

//go:embed testdata/accept
var acceptFixtureFS embed.FS

// acceptFixtureIdentity is the one keyboard every fixture commit is made at, and the
// identity the gate is pointed at when it judges the fixture.
var acceptFixtureIdentity = hygiene.Identity{Name: "Nova Fixture", Email: "fixture@example.invalid"}

// acceptBuildFixture writes the fixture repository into dir (which must not exist yet)
// and returns the base and head commits: base is the tree under base/, head is base
// plus fix.patch, committed by acceptFixtureIdentity. The branch is `dev` and the base
// is also tagged `base`, so a caller can name either.
func acceptBuildFixture(ctx context.Context, dir string) (base, head string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	if _, err := acceptGit(ctx, dir, "init", "-q", "-b", "dev"); err != nil {
		return "", "", err
	}
	root := "testdata/accept/base"
	err = fs.WalkDir(acceptFixtureFS, root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		raw, rerr := acceptFixtureFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".in")
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
			return merr
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		return "", "", fmt.Errorf("fixture base: %v", err)
	}
	if base, err = acceptFixtureCommit(ctx, dir, "fixture base"); err != nil {
		return "", "", err
	}
	if _, err := acceptGit(ctx, dir, "tag", "base"); err != nil {
		return "", "", err
	}
	patch, err := acceptFixtureFS.ReadFile(path.Join("testdata/accept", "fix.patch"))
	if err != nil {
		return "", "", err
	}
	if err := acceptApplyPatch(ctx, dir, patch); err != nil {
		return "", "", fmt.Errorf("fixture fix.patch: %v", err)
	}
	if head, err = acceptFixtureCommit(ctx, dir, "fixture fix: Sign(0) is 0"); err != nil {
		return "", "", err
	}
	return base, head, nil
}

// acceptFixtureCard is the known-good fix's card text.
func acceptFixtureCard() (string, error) {
	raw, err := acceptFixtureFS.ReadFile("testdata/accept/card.txt")
	return string(raw), err
}

// acceptApplyPatch applies a unified diff to dir's working tree with `git apply`.
func acceptApplyPatch(ctx context.Context, dir string, patch []byte) error {
	f, err := os.CreateTemp("", "nova-pulse-accept-patch-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(patch); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, err = acceptGit(ctx, dir, "apply", "--whitespace=nowarn", f.Name())
	return err
}

// acceptFixtureCommit stages everything in dir and commits it as the fixture identity.
func acceptFixtureCommit(ctx context.Context, dir, msg string) (string, error) {
	if _, err := acceptGit(ctx, dir, "add", "-A"); err != nil {
		return "", err
	}
	who := acceptFixtureIdentity
	if _, err := acceptGit(ctx, dir, "-c", "user.name="+who.Name, "-c", "user.email="+who.Email,
		"commit", "-q", "--no-verify", "-m", msg); err != nil {
		return "", err
	}
	return acceptGit(ctx, dir, "rev-parse", "HEAD")
}
