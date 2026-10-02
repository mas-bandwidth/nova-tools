package main

// handoff.go is how a card's work LEAVES the disposable place.
//
// The run verb's whole contract is that nothing survives: the volume is created,
// the command runs on it, and on every exit the volume is unmounted and deleted.
// That is exactly right for scratch and exactly wrong for the one thing a card
// is for: a card that cannot hand back its commit is a card that has to be re-done
// outside the sandbox, which is the same as not having one.
//
// `--out <dir>` is the door, and it is narrow on purpose:
//
//   - It opens AFTER the command has finished and BEFORE the volume is deleted.
//     There is exactly one place in runDisposable where both are true.
//   - Only NAMED artifacts leave. The default set is the card's receipt
//     (RESULT.md), its token row (usage.tsv) and its git bundle (repo.bundle);
//     `--artifact <relpath>` names another, repeatable. A default that is not
//     there is skipped; an artifact the CALLER named and is not there is a
//     refusal (the run verb's rule 5; --read also refuses a path the
//     caller named).
//   - Every source is reached by descriptor from the card's working directory:
//     openat with O_NOFOLLOW on each path component, O_NONBLOCK so a FIFO cannot
//     hold the open, and an fstat of each descriptor that refuses anything but a
//     regular file or a directory on the volume's own device. The bytes are read
//     from the descriptor that was checked, never from a path opened again, so a
//     process that outlived the command cannot swap a symlink in and make the
//     handoff copy something off the volume.
//   - The whole set is MEASURED before a byte is written and refused over
//     `--out-max-bytes` (64 MiB by default). A handoff is a door, not a backup:
//     a card that wants to move gigabytes wants a bundle, or it wants a
//     different tool.
//
// The documented way a commit leaves is `git bundle create repo.bundle <branch>`
// as the card's last step: one file carrying the full history for that branch,
// and `git fetch ./repo.bundle <branch>` on the other side restores it.
// docs/SPEC-SANDBOX.md documents this pattern.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	Mount     string   // the volume's mount point: every byte that leaves is read from its device; empty means Work's own device
	Work      string   // the card's working directory on the volume: every artifact is relative to it
	Out       string   // where the run's directory is made: <Out>/<Name>/
	Name      string   // the run's --name, which is the directory the artifacts land in
	Artifacts []string // the caller's, when it named any; otherwise defaultArtifacts
	Named     bool     // true when the caller named the set, which makes a missing one a refusal
	MaxBytes  int64    // the ceiling on the whole set

	hooks *handoffHooks // a test's own system calls; nil is the real ones
}

// volumeReader is what every checked open needs: the volume's device and the
// system calls it is made with.
type volumeReader struct {
	dev   uint64
	hooks *handoffHooks
}

// handoffSource is one thing that will be copied: the path components under the
// card's working directory, where it goes, and how big it was when measured. It
// carries no host path: the copy reaches it again one checked descriptor at a
// time from the working directory's own descriptor.
type handoffSource struct {
	comps []string
	rel   string // the path under <Out>/<Name>/
	size  int64
}

// entryKind is what a checked descriptor holds.
type entryKind int

const (
	entryOther entryKind = iota
	entryFile
	entryDir
)

// handoffEntry is one checked descriptor: opened with no link followed and no
// wait on a FIFO, and fstat'd to be a regular file or a directory whose device
// is the volume's. What the copy reads is this descriptor and nothing reopened.
type handoffEntry struct {
	f    *os.File
	kind entryKind
	size int64
	mode fs.FileMode
}

// copyOut is the door. It returns the receipt, or a refusal naming what stopped
// it. Nothing is written unless the whole set is resolvable and fits.
//
// The card's own processes may outlive the command (a setsid child is outside
// the process group supervise kills), so the volume can change under the copy.
// Every step is therefore taken by descriptor: the working directory is opened
// from the mount, each path component below it with openat and O_NOFOLLOW, and
// each descriptor is fstat'd for its type and for the volume's device before it
// is used. A component swapped to a symlink fails the open; one swapped to a
// FIFO is opened without waiting and refused by type; anything whose device is
// not the volume's is refused.
func copyOut(in handoffInput) (handoffResult, error) {
	want := in.Artifacts
	if len(want) == 0 {
		want = defaultArtifacts
	}
	max := in.MaxBytes
	if max <= 0 {
		max = defaultOutMaxBytes
	}
	mount := in.Mount
	if mount == "" {
		mount = in.Work
	}
	root, vr, err := handoffOpenRoot(mount, in.Work, in.hooks)
	if err != nil {
		return handoffResult{}, fmt.Errorf("--out cannot read the card's working directory: %s", oneline.Err(err))
	}
	defer root.Close()

	var sources []handoffSource
	var total int64
	for _, rel := range want {
		clean, err := cleanArtifactRel(rel)
		if err != nil {
			return handoffResult{}, err
		}
		found, bytes, err := measureArtifact(root, vr, clean)
		if err != nil {
			if os.IsNotExist(err) {
				if in.Named {
					return handoffResult{}, fmt.Errorf("--artifact %s is not on the volume; the card never wrote it", oneline.Field(clean))
				}
				continue
			}
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
		n, err := copyChecked(root, vr, s, target, max-written)
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

// openComps walks from the working directory's descriptor to the entry the
// components name, one checked openat per component. Every component but the
// last must be a directory. The caller closes what it returns.
func openComps(root *os.File, vr volumeReader, comps []string) (handoffEntry, error) {
	dir := root
	for i, c := range comps {
		e, err := handoffOpenAt(dir, c, vr)
		if dir != root {
			dir.Close()
		}
		if err != nil {
			return handoffEntry{}, err
		}
		if i == len(comps)-1 {
			return e, nil
		}
		if e.kind != entryDir {
			e.f.Close()
			return handoffEntry{}, fmt.Errorf("%s is not a directory", c)
		}
		dir = e.f
	}
	return handoffEntry{}, fmt.Errorf("an artifact names no path")
}

// measureArtifact turns one artifact into the files it stands for: itself when
// it is a file, and every regular file under it when it is a directory. A
// symlink, a device and a socket inside a directory are skipped -- a handoff
// copies BYTES, and a link copied out of a volume that is about to be deleted
// points at nothing. The artifact itself being one of those is a refusal.
func measureArtifact(root *os.File, vr volumeReader, clean string) ([]handoffSource, int64, error) {
	comps := strings.Split(clean, "/")
	e, err := openComps(root, vr, comps)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, err
		}
		return nil, 0, fmt.Errorf("--out cannot take %s: %s", oneline.Field(clean), oneline.Err(err))
	}
	defer e.f.Close()
	switch e.kind {
	case entryFile:
		return []handoffSource{{comps: comps, rel: clean, size: e.size}}, e.size, nil
	case entryDir:
		var out []handoffSource
		var total int64
		if err := walkChecked(e.f, vr, comps, clean, &out, &total); err != nil {
			return nil, 0, fmt.Errorf("--out could not read %s: %s", oneline.Field(clean), oneline.Err(err))
		}
		return out, total, nil
	default:
		return nil, 0, fmt.Errorf("--out will not take %s: it is neither a file nor a directory, and a handoff copies bytes", oneline.Field(clean))
	}
}

// walkChecked lists one checked directory by its descriptor and descends into
// each subdirectory by a checked openat from it. An entry the listing calls a
// regular file or a directory is opened and checked; one that has become
// anything else by then is a refusal, because the volume changed under the copy.
func walkChecked(dir *os.File, vr volumeReader, comps []string, rel string, out *[]handoffSource, total *int64) error {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, d := range entries {
		t := d.Type()
		if !t.IsDir() && !t.IsRegular() {
			continue
		}
		name := d.Name()
		e, err := handoffOpenAt(dir, name, vr)
		if err != nil {
			return fmt.Errorf("%s/%s: %s", rel, name, oneline.Err(err))
		}
		sub := append(append([]string{}, comps...), name)
		subRel := rel + "/" + name
		switch {
		case t.IsDir() && e.kind == entryDir:
			err = walkChecked(e.f, vr, sub, subRel, out, total)
		case t.IsRegular() && e.kind == entryFile:
			*out = append(*out, handoffSource{comps: sub, rel: subRel, size: e.size})
			*total += e.size
		default:
			err = fmt.Errorf("%s changed type while the handoff read it", subRel)
		}
		e.f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// copyChecked copies one source and returns the bytes written. The source is
// reached again from the working directory's descriptor, checked at every
// component, and the bytes are read from the descriptor that was checked. At
// most budget bytes (what is left of the cap) are written; a file that grew past
// that after it was measured is refused, and the partial destination is removed,
// so nothing past the cap is ever in --out. The destination is created fresh: a
// handoff never appends to whatever was in the out directory before it.
func copyChecked(root *os.File, vr volumeReader, s handoffSource, to string, budget int64) (int64, error) {
	e, err := openComps(root, vr, s.comps)
	if err != nil {
		return 0, err
	}
	defer e.f.Close()
	if e.kind != entryFile {
		return 0, fmt.Errorf("it is no longer a regular file")
	}
	mode := e.mode
	if mode == 0 {
		mode = 0o644
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	n, over, copyErr := copyBounded(dst, e.f, budget)
	closeErr := dst.Close()
	switch {
	case copyErr != nil:
		err = copyErr
	case over:
		err = fmt.Errorf("it grew past --out-max-bytes after it was measured")
	default:
		err = closeErr
	}
	if err != nil {
		// ignored: a best-effort cleanup of the partial copy; the copy error is the one returned
		_ = os.Remove(to)
		return 0, err
	}
	return n, nil
}

// copyBounded writes at most budget bytes of src to dst, then reads one more
// byte into a scratch buffer and never writes it: over is whether src held more
// than budget.
func copyBounded(dst io.Writer, src io.Reader, budget int64) (n int64, over bool, err error) {
	budget = max(budget, 0)
	n, err = io.Copy(dst, io.LimitReader(src, budget))
	if err != nil {
		return n, false, err
	}
	var extra [1]byte
	k, rerr := io.ReadFull(src, extra[:])
	if k > 0 {
		return n, true, nil
	}
	if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
		return n, false, rerr
	}
	return n, false, nil
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
