package main

// handoff.go is how a card's work LEAVES the disposable place.
//
// The run verb's whole contract is that nothing survives: the volume is created,
// the command runs on it, and on every exit the volume is unmounted and deleted.
// That is exactly right for scratch and exactly wrong for the one thing a card
// is for. Measured 2026-09-18, dogfooding `nova-sandbox run` on a real card: the
// card cloned the repo, made the fix, committed it — and the commit died with
// the volume. There was no writable path out. A card that cannot hand back its
// commit is a card that has to be re-done outside the sandbox, which is the same
// as not having one.
//
// `--out <dir>` is the door, and it is narrow on purpose:
//
//   - It opens AFTER the command has finished and BEFORE the volume is deleted.
//     There is exactly one place in runDisposable where both are true.
//   - Only NAMED artifacts leave. The default set is the card's receipt
//     (RESULT.md), its token row (usage.tsv) and its git bundle (repo.bundle);
//     `--artifact <relpath>` names another, repeatable. A default that is not
//     there is skipped; an artifact the CALLER named and is not there is a
//     refusal (the run verb's rule 5, the same way --read refuses a path the
//     caller named).
//   - Every source path is resolved through safepath.ResolvedUnder against the
//     card's own working directory, so a `..`, a symlink or an absolute path
//     cannot make the handoff copy something off the volume.
//   - The whole set is MEASURED before a byte is written and refused over
//     `--out-max-bytes` (64 MiB by default). A handoff is a door, not a backup:
//     a card that wants to move gigabytes wants a bundle, or it wants a
//     different tool.
//
// The documented way a commit leaves is `git bundle create repo.bundle <branch>`
// as the card's last step: one file, complete history for that branch, and
// `git fetch ./repo.bundle <branch>` on the other side. docs/SPEC-SANDBOX.md
// says so and cmd/nova-pulse/testdata/templates/fix.md ends with it.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// defaultOutMaxBytes is the ceiling on everything one run hands back, 64 MiB. It
// is a door's size, not a disk's: a bundle of a card's branch is kilobytes, a
// RESULT.md is bytes, and a set that does not fit is a card copying its build
// tree by mistake.
const defaultOutMaxBytes int64 = 64 << 20

// defaultArtifacts is what leaves when the caller names nothing: the card's
// receipt, its token row, and the bundle that carries its commit. Each is taken
// IF PRESENT — a read card writes no bundle and a card on a bench with no token
// accounting writes no usage.tsv, and neither is a failure.
var defaultArtifacts = []string{"RESULT.md", "usage.tsv", "repo.bundle"}

// handoffResult is the one line this file prints: what left, how many files it
// was, and how many bytes.
type handoffResult struct {
	Name  string
	Files int
	Bytes int64
}

// Line is the receipt, in the run verb's own grammar.
func (h handoffResult) Line() string {
	return fmt.Sprintf("SANDBOX OUT name=%s files=%d bytes=%d", oneline.Field(h.Name), h.Files, h.Bytes)
}

// handoffInput is everything the copy needs, held apart from the run verb so a
// test drives it with two ordinary directories and no volume at all.
type handoffInput struct {
	Work      string   // the card's working directory on the volume: every artifact is relative to it
	Out       string   // where the run's directory is made: <Out>/<Name>/
	Name      string   // the run's --name, which is the directory the artifacts land in
	Artifacts []string // the caller's, when it named any; otherwise defaultArtifacts
	Named     bool     // true when the caller named the set, which makes a missing one a refusal
	MaxBytes  int64    // the ceiling on the whole set
}

// handoffSource is one thing that will be copied: where it is, where it goes,
// and how big it is. The whole set is built before anything is written.
type handoffSource struct {
	from string
	rel  string // the path under <Out>/<Name>/
	size int64
	mode fs.FileMode
}

// copyOut is the door. It returns the receipt, or a refusal naming what stopped
// it. Nothing is written unless the whole set is resolvable and fits.
func copyOut(in handoffInput) (handoffResult, error) {
	want := in.Artifacts
	if len(want) == 0 {
		want = defaultArtifacts
	}
	max := in.MaxBytes
	if max <= 0 {
		max = defaultOutMaxBytes
	}

	var sources []handoffSource
	var total int64
	for _, rel := range want {
		clean, err := cleanArtifactRel(rel)
		if err != nil {
			return handoffResult{}, err
		}
		abs := filepath.Join(in.Work, filepath.FromSlash(clean))
		resolved, err := safepath.ResolvedUnder(abs, in.Work)
		if err != nil {
			if os.IsNotExist(err) {
				if in.Named {
					return handoffResult{}, fmt.Errorf("--artifact %s is not on the volume; the card never wrote it", oneline.Field(clean))
				}
				continue
			}
			return handoffResult{}, fmt.Errorf("--out cannot take %s: %s", oneline.Field(clean), oneline.Err(err))
		}
		found, bytes, err := walkArtifact(resolved, clean)
		if err != nil {
			return handoffResult{}, err
		}
		sources = append(sources, found...)
		total += bytes
	}
	if total > max {
		return handoffResult{}, fmt.Errorf("the artifacts are %d bytes and --out-max-bytes is %d; a handoff is a door, not a backup -- name fewer artifacts, or bundle them (git bundle create repo.bundle <branch>)", total, max)
	}

	dest := filepath.Join(in.Out, in.Name)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return handoffResult{}, fmt.Errorf("--out %s could not be made: %s", oneline.Field(dest), oneline.Err(err))
	}
	var written int64
	for _, s := range sources {
		target := filepath.Join(dest, filepath.FromSlash(s.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return handoffResult{}, fmt.Errorf("--out %s could not be made: %s", oneline.Field(filepath.Dir(target)), oneline.Err(err))
		}
		n, err := copyFile(s.from, target, s.mode)
		if err != nil {
			return handoffResult{}, fmt.Errorf("--out could not take %s: %s", oneline.Field(s.rel), oneline.Err(err))
		}
		written += n
	}
	return handoffResult{Name: in.Name, Files: len(sources), Bytes: written}, nil
}

// cleanArtifactRel is the shape an artifact name may take: a relative path on
// the volume, with no `..` element and no leading separator. It is checked here
// as TEXT, before any filesystem call, so a refusal names the flag rather than
// an errno.
func cleanArtifactRel(rel string) (string, error) {
	s := strings.TrimSpace(rel)
	if s == "" {
		return "", fmt.Errorf("--artifact wants a path relative to the card's working directory: --artifact RESULT.md")
	}
	s = filepath.ToSlash(s)
	if strings.HasPrefix(s, "/") || (len(s) >= 2 && s[1] == ':') {
		return "", fmt.Errorf("--artifact %s is absolute; it names a path on the volume, relative to the card's working directory", oneline.Field(rel))
	}
	if safepath.HasDotDot(s) {
		return "", fmt.Errorf("--artifact %s has a .. element; what leaves is on the volume and nothing above it", oneline.Field(rel))
	}
	clean := filepath.ToSlash(filepath.Clean(s))
	if clean == "." {
		return "", fmt.Errorf("--artifact . is the whole working directory; name the files that leave")
	}
	return clean, nil
}

// walkArtifact turns one resolved source into the files it stands for: itself
// when it is a file, and every regular file under it when it is a directory. A
// symlink, a device and a socket are skipped -- a handoff copies BYTES, and a
// link copied out of a volume that is about to be deleted points at nothing.
func walkArtifact(resolved, rel string) ([]handoffSource, int64, error) {
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, 0, fmt.Errorf("--out could not read %s: %s", oneline.Field(rel), oneline.Err(err))
	}
	if info.Mode().IsRegular() {
		return []handoffSource{{from: resolved, rel: rel, size: info.Size(), mode: info.Mode().Perm()}}, info.Size(), nil
	}
	if !info.IsDir() {
		return nil, 0, fmt.Errorf("--out will not take %s: it is neither a file nor a directory, and a handoff copies bytes", oneline.Field(rel))
	}
	var out []handoffSource
	var total int64
	walkErr := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		sub, relErr := filepath.Rel(resolved, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, handoffSource{
			from: path,
			rel:  rel + "/" + filepath.ToSlash(sub),
			size: fi.Size(),
			mode: fi.Mode().Perm(),
		})
		total += fi.Size()
		return nil
	})
	if walkErr != nil {
		return nil, 0, fmt.Errorf("--out could not read %s: %s", oneline.Field(rel), oneline.Err(walkErr))
	}
	return out, total, nil
}

// copyFile copies one regular file and returns the bytes written. The
// destination is created fresh: a handoff never appends to whatever was in the
// out directory before it.
func copyFile(from, to string, mode fs.FileMode) (int64, error) {
	src, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	if mode == 0 {
		mode = 0o644
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return n, copyErr
	}
	return n, closeErr
}

// handoff is the run verb's own call: it does the copy, prints the receipt or
// the refusal, and answers whether the run's status must change.
//
// A handoff that fails after a command that SUCCEEDED turns the run into a
// refusal, because a zero exit would tell the caller the artifacts are in --out
// when they are not. A handoff that fails after a command that already failed
// leaves that status alone: the command's own failure is the more important
// truth, and it is almost always why nothing was there to hand back.
func handoff(stderr io.Writer, in handoffInput, code int) int {
	res, err := step(stderr, "out", func() (handoffResult, error) { return copyOut(in) })
	if err != nil {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=out_failed: %s\n%s\n", oneline.Escape(err.Error()), runRemedy)
		if code == 0 {
			return sandbox.ExitRefused
		}
		return code
	}
	fmt.Fprintln(stderr, res.Line())
	return code
}
